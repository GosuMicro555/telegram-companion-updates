package gotd

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"telegram-companion/internal/domain"
	"telegram-companion/internal/usecase/runtimeconfig"
)

var (
	ErrScoutSendRejected   = errors.New("scout accounts cannot send Telegram messages")
	ErrOutboundRateLimited = errors.New("outbound Telegram rate limit reached")
	ErrOutboundEmptyReply  = errors.New("selected reply is empty")
)

type RuntimeSnapshotSource interface {
	Current() runtimeconfig.Snapshot
}

type accountDeliveryStore interface {
	WithAccountLease(context.Context, domain.ID, func(domain.Account) error) error
	RecordFloodWait(context.Context, domain.ID, time.Time, time.Time) error
}

type scheduledPrivateMessageSender interface {
	SendScheduledPrivateMessage(context.Context, domain.Account, domain.ScheduledDMDelivery, string) error
}

type scheduledUsernameResolver interface {
	ResolveUsername(context.Context, domain.Account, string) error
}

// Outbound is the final policy boundary before a Telegram send. Selection is
// intentionally checked again here so a stale queued job can never use a scout.
type Outbound struct {
	config   RuntimeSnapshotSource
	accounts domain.AccountRepository
	api      OutboundSender
	now      func() time.Time

	mu         sync.Mutex
	lastGlobal time.Time
	history    []time.Time
}

func NewOutbound(config RuntimeSnapshotSource, accounts domain.AccountRepository, api OutboundSender, now func() time.Time) *Outbound {
	return &Outbound{config: config, accounts: accounts, api: api, now: now}
}

func (s *Outbound) SendPublicReply(ctx context.Context, account domain.Account, job domain.OutgoingMessageJob) error {
	return s.send(ctx, account, job, false)
}

func (s *Outbound) SendPrivateMessage(ctx context.Context, account domain.Account, job domain.OutgoingMessageJob) error {
	return s.send(ctx, account, job, true)
}

func (s *Outbound) ResolveUsername(ctx context.Context, account domain.Account, username string) error {
	if s == nil || s.api == nil {
		return domain.TransientDeliveryFailure(errors.New("outbound sender is not configured"))
	}
	resolver, ok := s.api.(scheduledUsernameResolver)
	if !ok {
		return domain.PermanentDeliveryFailure(errors.New("scheduled username resolver is not configured"))
	}
	return s.sendWithLease(ctx, account, false, func(authoritative domain.Account, _ runtimeconfig.Snapshot) error {
		return resolver.ResolveUsername(ctx, authoritative, username)
	})
}

func (s *Outbound) SendScheduledPrivateMessage(ctx context.Context, account domain.Account, delivery domain.ScheduledDMDelivery, messageText string) error {
	if s == nil || s.api == nil {
		return domain.PermanentDeliveryFailure(errors.New("outbound sender is not configured"))
	}
	if strings.TrimSpace(messageText) == "" {
		return domain.PermanentDeliveryFailure(ErrOutboundEmptyReply)
	}
	api, ok := s.api.(scheduledPrivateMessageSender)
	if !ok {
		return domain.PermanentDeliveryFailure(errors.New("scheduled private message sender is not configured"))
	}
	return s.sendWithLease(ctx, account, true, func(authoritative domain.Account, _ runtimeconfig.Snapshot) error {
		return api.SendScheduledPrivateMessage(ctx, authoritative, delivery, messageText)
	})
}

func (s *Outbound) send(ctx context.Context, account domain.Account, job domain.OutgoingMessageJob, private bool) error {
	return s.sendWithLease(ctx, account, true, func(authoritative domain.Account, snapshot runtimeconfig.Snapshot) error {
		if private {
			job.Text = snapshot.PrivateReply
			if job.Text == "" && !snapshot.PrivateReplyPresent {
				job.Text = snapshot.SharedReply
			}
		} else {
			job.Text = snapshot.SharedReply
		}
		if job.Text == "" {
			return domain.PermanentDeliveryFailure(ErrOutboundEmptyReply)
		}
		if private {
			return s.api.SendPrivateMessage(ctx, authoritative, job)
		}
		return s.api.SendPublicReply(ctx, authoritative, job)
	})
}

func (s *Outbound) sendWithLease(ctx context.Context, account domain.Account, reserveMessageRate bool, send func(domain.Account, runtimeconfig.Snapshot) error) error {
	if s == nil || s.config == nil || s.accounts == nil || s.api == nil || s.now == nil {
		return domain.PermanentDeliveryFailure(errors.New("outbound sender is not configured"))
	}
	if account.Role == domain.AccountRoleScoutAnalyst {
		return domain.PermanentDeliveryFailure(ErrScoutSendRejected)
	}
	delivery, ok := s.accounts.(accountDeliveryStore)
	if !ok {
		return domain.PermanentDeliveryFailure(errors.New("outbound account repository does not support account send leases"))
	}
	err := delivery.WithAccountLease(ctx, account.ID, func(authoritative domain.Account) error {
		if authoritative.Role != domain.AccountRoleSpammer {
			return domain.PermanentDeliveryFailure(ErrScoutSendRejected)
		}
		if !authoritative.Eligible() {
			return domain.TransientDeliveryFailure(errors.New("outbound account is not active"))
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		snapshot := s.config.Current()
		now := s.now().UTC()
		if reserveMessageRate && !s.reserve(now, snapshot.RateLimits) {
			return domain.TransientDeliveryFailure(ErrOutboundRateLimited)
		}
		err := send(authoritative, snapshot)
		if err != nil {
			var flood *FloodWaitError
			if errors.As(err, &flood) {
				until := now.Add(flood.Duration)
				if saveErr := delivery.RecordFloodWait(context.WithoutCancel(ctx), authoritative.ID, until, now); saveErr != nil {
					return errors.Join(err, saveErr)
				}
			}
			return err
		}
		return nil
	})
	if err == nil || domain.IsPermanentDeliveryFailure(err) || domain.IsTransientDeliveryFailure(err) {
		return err
	}
	var retry interface{ RetryAfter() time.Duration }
	if errors.As(err, &retry) && retry.RetryAfter() > 0 {
		return err
	}
	return domain.TransientDeliveryFailure(err)
}

func (s *Outbound) reserve(now time.Time, limits runtimeconfig.RateLimits) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	maxPerMinute := limits.RepliesPerMinute
	if maxPerMinute <= 0 || maxPerMinute > 19 {
		maxPerMinute = 19
	}
	interval := time.Duration(limits.MinIntervalSeconds) * time.Second
	if interval < 2*time.Second {
		interval = 2 * time.Second
	}
	if !s.lastGlobal.IsZero() && now.Sub(s.lastGlobal) < interval {
		return false
	}
	window := now.Add(-time.Minute)
	kept := s.history[:0]
	for _, sentAt := range s.history {
		if sentAt.After(window) {
			kept = append(kept, sentAt)
		}
	}
	s.history = kept
	if len(s.history) >= maxPerMinute {
		return false
	}
	s.history = append(s.history, now)
	s.lastGlobal = now
	return true
}

var _ OutboundSender = (*Outbound)(nil)
