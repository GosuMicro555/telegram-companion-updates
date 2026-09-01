package usecase

import (
	"context"
	"errors"
	"strings"
	"time"

	"telegram-companion/internal/domain"
)

type MessageSender interface {
	SendPublicReply(context.Context, domain.Account, domain.OutgoingMessageJob) error
	SendPrivateMessage(context.Context, domain.Account, domain.OutgoingMessageJob) error
}

type AccountAvailability interface {
	Available(domain.ID) bool
}

type GroupRestEnabledResolver func(context.Context) (bool, error)

type durableJobRepository interface {
	Claim(context.Context, domain.ID, domain.ID) (domain.ID, error)
	Complete(context.Context, domain.ID, domain.OutgoingMessageEvent) (bool, error)
}

type dispatchGuardRepository interface {
	CanDispatch(context.Context, domain.ID, domain.ID) (bool, error)
}

type keywordDeliveryRepository interface {
	RecordPrivateClosed(context.Context, domain.ID, domain.ID, time.Time) error
	CompleteKeywordResponse(context.Context, domain.KeywordDeliveryOutcome) (bool, error)
	DelayKeywordFailure(context.Context, domain.KeywordDeliveryFailure) error
	DelayTransientUntil(context.Context, domain.ID, string, time.Time) error
}

type Scheduler struct {
	accounts    domain.AccountRepository
	jobs        domain.JobRepository
	groupRests  domain.GroupRestRepository
	telegram    MessageSender
	selector    *RoundRobinSelector
	limiter     *MemoryLimiter
	available   AccountAvailability
	restEnabled GroupRestEnabledResolver
}

func (s *Scheduler) SetGroupRestEnabledResolver(resolver GroupRestEnabledResolver) {
	s.restEnabled = resolver
}

func (s *Scheduler) groupRestIsEnabled(ctx context.Context) (bool, error) {
	if s.restEnabled == nil {
		return true, nil
	}
	return s.restEnabled(ctx)
}

func NewScheduler(
	accounts domain.AccountRepository,
	jobs domain.JobRepository,
	groupRests domain.GroupRestRepository,
	telegram MessageSender,
	selector *RoundRobinSelector,
	limiter *MemoryLimiter,
	availability ...AccountAvailability,
) *Scheduler {
	var available AccountAvailability
	if len(availability) > 0 {
		available = availability[0]
	}
	return &Scheduler{
		accounts: accounts, jobs: jobs, groupRests: groupRests,
		telegram: telegram, selector: selector, limiter: limiter, available: available,
	}
}

func (s *Scheduler) RunOnce(ctx context.Context, now time.Time) error {
	job, err := s.jobs.NextDue(ctx)
	if err != nil || job == nil {
		return err
	}

	accounts, err := s.accounts.ListActive(ctx)
	if err != nil {
		return err
	}
	if s.available != nil {
		filtered := accounts[:0]
		for _, account := range accounts {
			if s.available.Available(account.ID) {
				filtered = append(filtered, account)
			}
		}
		accounts = filtered
	}
	durable, ok := s.jobs.(durableJobRepository)
	if !ok {
		return errors.New("outgoing job repository does not support durable claims")
	}
	var keywordJobs keywordDeliveryRepository
	if job.Type == domain.JobKeywordResponse {
		var supported bool
		keywordJobs, supported = s.jobs.(keywordDeliveryRepository)
		if !supported {
			return errors.New("outgoing job repository does not support keyword delivery")
		}
	}
	var account *domain.Account
	if job.AccountID != nil {
		account = findAccount(accounts, *job.AccountID)
		if account == nil {
			if keywordJobs != nil {
				return s.delayKeywordFailure(ctx, *job, now, keywordJobs, "claimed_account_unavailable", nil, nil)
			}
			return s.jobs.Delay(ctx, job.ID, "claimed_account_unavailable")
		}
	} else {
		candidates, earliestRestUntil, err := s.filterRestingPublicCandidates(ctx, *job, accounts, now)
		if err != nil {
			return err
		}
		if len(candidates) == 0 && earliestRestUntil != nil {
			return s.delayForGroupRest(ctx, *job, keywordJobs, *earliestRestUntil)
		}
		account, err = s.selector.Next(candidates)
		if err != nil {
			if keywordJobs != nil {
				return s.delayKeywordFailure(ctx, *job, now, keywordJobs, "no_eligible_account", nil, nil)
			}
			return s.jobs.Delay(ctx, job.ID, "no_eligible_account")
		}
		claimedID, err := durable.Claim(ctx, job.ID, account.ID)
		if err != nil {
			return err
		}
		account = findAccount(accounts, claimedID)
		if account == nil {
			if keywordJobs != nil {
				return s.delayKeywordFailure(ctx, *job, now, keywordJobs, "claimed_account_unavailable", nil, nil)
			}
			return s.jobs.Delay(ctx, job.ID, "claimed_account_unavailable")
		}
		job.AccountID = &claimedID
	}
	if s.limiter != nil && !s.limiter.Allow(account.ID, now) {
		if keywordJobs != nil {
			return s.delayKeywordFailure(ctx, *job, now, keywordJobs, "rate_limited", account, nil)
		}
		return s.jobs.Delay(ctx, job.ID, "rate_limited")
	}

	switch job.Type {
	case domain.JobKeywordResponse:
		return s.runKeywordResponse(ctx, now, *job, *account, keywordJobs)
	case domain.JobPublicReply:
		var delayed bool
		delayed, err = s.delayClaimedPublicForRest(ctx, *job, *account, now, nil)
		if err != nil || delayed {
			return err
		}
		if allowed, err := s.canDispatch(ctx, job.ID, account.ID); err != nil || !allowed {
			return err
		}
		err = s.telegram.SendPublicReply(ctx, *account, *job)
	case domain.JobPrivateMessage:
		if allowed, err := s.canDispatch(ctx, job.ID, account.ID); err != nil || !allowed {
			return err
		}
		err = s.telegram.SendPrivateMessage(ctx, *account, *job)
	default:
		return s.jobs.Delay(ctx, job.ID, "unsupported_job_type")
	}
	if err != nil {
		return s.delaySendFailure(ctx, job.ID, now, err)
	}

	event := domain.OutgoingMessageEvent{
		ID:        domain.ID(randomID()),
		JobID:     job.ID,
		AccountID: account.ID,
		ChannelID: job.ChannelID,
		Type:      job.Type,
		Success:   true,
		CreatedAt: now.UTC(),
	}
	_, err = durable.Complete(ctx, job.ID, event)
	return err
}

func (s *Scheduler) runKeywordResponse(ctx context.Context, now time.Time, job domain.OutgoingMessageJob, account domain.Account, jobs keywordDeliveryRepository) error {
	privateTurn := keywordPrivateTurn(job, account)

	actualType := domain.JobPublicReply
	var advanceTo *domain.DeliveryTarget
	var err error
	if privateTurn {
		actualType = domain.JobPrivateMessage
		if allowed, err := s.canDispatch(ctx, job.ID, account.ID); err != nil || !allowed {
			return err
		}
		err = s.telegram.SendPrivateMessage(ctx, account, job)
		if domain.IsPrivateMessageClosed(err) {
			if err := jobs.RecordPrivateClosed(ctx, job.ID, account.ID, now.UTC()); err != nil {
				return err
			}
			if job.KeywordDeliveryMode == domain.KeywordDeliveryModePrivate && !job.FallbackToPublic {
				return nil
			}
			return jobs.DelayTransientUntil(ctx, job.ID, "private_message_closed", now.UTC().Add(2*time.Second))
		}
		if err == nil && job.KeywordDeliveryMode == "" {
			next := domain.DeliveryTargetPublic
			advanceTo = &next
		}
	} else {
		delayed, restErr := s.delayClaimedPublicForRest(ctx, job, account, now, jobs)
		if restErr != nil || delayed {
			return restErr
		}
		if allowed, err := s.canDispatch(ctx, job.ID, account.ID); err != nil || !allowed {
			return err
		}
		err = s.telegram.SendPublicReply(ctx, account, job)
		if err == nil && job.KeywordDeliveryMode == "" && job.AllowPrivate {
			next := domain.DeliveryTargetPrivate
			advanceTo = &next
		}
	}
	if err != nil {
		actualTypePointer := actualType
		return s.delayKeywordFailure(ctx, job, now, jobs, "send_failed", &account, &actualTypePointer, err)
	}

	_, err = jobs.CompleteKeywordResponse(ctx, domain.KeywordDeliveryOutcome{
		JobID: job.ID, AccountID: account.ID, ChannelID: job.ChannelID, AccountTitleSnapshot: account.DisplayName,
		Type: actualType, AdvanceTo: advanceTo, CompletedAt: now.UTC(),
	})
	return err
}

func (s *Scheduler) canDispatch(ctx context.Context, jobID, accountID domain.ID) (bool, error) {
	guard, ok := s.jobs.(dispatchGuardRepository)
	if !ok {
		return false, errors.New("outgoing job repository does not support dispatch checks")
	}
	return guard.CanDispatch(ctx, jobID, accountID)
}

func (s *Scheduler) delayKeywordFailure(ctx context.Context, job domain.OutgoingMessageJob, now time.Time, jobs keywordDeliveryRepository, errorCode string, account *domain.Account, deliveryType *domain.JobType, sendErr ...error) error {
	nextAttemptAt := now.UTC().Add(2 * time.Second)
	if len(sendErr) > 0 {
		var retry interface{ RetryAfter() time.Duration }
		if errors.As(sendErr[0], &retry) && retry.RetryAfter() > 0 {
			nextAttemptAt = now.UTC().Add(retry.RetryAfter())
		}
	}
	failure := domain.KeywordDeliveryFailure{
		JobID: job.ID, Type: deliveryType, ErrorCode: errorCode, FailedAt: now.UTC(), NextAttemptAt: nextAttemptAt,
	}
	if account != nil {
		accountID := account.ID
		failure.AccountID = &accountID
		failure.AccountTitleSnapshot = account.DisplayName
	}
	return jobs.DelayKeywordFailure(ctx, failure)
}

func (s *Scheduler) delaySendFailure(ctx context.Context, jobID domain.ID, now time.Time, sendErr error) error {
	var retry interface{ RetryAfter() time.Duration }
	if errors.As(sendErr, &retry) && retry.RetryAfter() > 0 {
		if delayed, ok := s.jobs.(domain.DelayedJobRepository); ok {
			return delayed.DelayUntil(ctx, jobID, "send_failed", now.UTC().Add(retry.RetryAfter()))
		}
	}
	return s.jobs.Delay(ctx, jobID, "send_failed")
}

func (s *Scheduler) filterRestingPublicCandidates(
	ctx context.Context,
	job domain.OutgoingMessageJob,
	accounts []domain.Account,
	now time.Time,
) ([]domain.Account, *time.Time, error) {
	candidates := make([]domain.Account, 0, len(accounts))
	publicIDs := make([]domain.ID, 0, len(accounts))
	for _, account := range accounts {
		if !account.Eligible() || account.Role != domain.AccountRoleSpammer {
			continue
		}
		candidates = append(candidates, account)
		if deliveryRequiresPublic(job, account) {
			publicIDs = append(publicIDs, account.ID)
		}
	}
	if len(publicIDs) == 0 || s.groupRests == nil {
		return candidates, nil, nil
	}
	enabled, err := s.groupRestIsEnabled(ctx)
	if err != nil {
		return nil, nil, err
	}
	if !enabled {
		return candidates, nil, nil
	}
	active, err := s.groupRests.ActiveGroupRestUntil(ctx, publicIDs, now.UTC())
	if err != nil {
		return nil, nil, err
	}
	eligible := make([]domain.Account, 0, len(candidates))
	var earliest *time.Time
	for _, account := range candidates {
		until, resting := active[account.ID]
		if !deliveryRequiresPublic(job, account) || !resting || !until.After(now) {
			eligible = append(eligible, account)
			continue
		}
		until = until.UTC()
		if earliest == nil || until.Before(*earliest) {
			value := until
			earliest = &value
		}
	}
	return eligible, earliest, nil
}

func (s *Scheduler) delayClaimedPublicForRest(
	ctx context.Context,
	job domain.OutgoingMessageJob,
	account domain.Account,
	now time.Time,
	keywordJobs keywordDeliveryRepository,
) (bool, error) {
	if s.groupRests == nil {
		return false, nil
	}
	enabled, err := s.groupRestIsEnabled(ctx)
	if err != nil || !enabled {
		return false, err
	}
	active, err := s.groupRests.ActiveGroupRestUntil(ctx, []domain.ID{account.ID}, now.UTC())
	if err != nil {
		return false, err
	}
	until, resting := active[account.ID]
	if !resting || !until.After(now) {
		return false, nil
	}
	return true, s.delayForGroupRest(ctx, job, keywordJobs, until)
}

func (s *Scheduler) delayForGroupRest(
	ctx context.Context,
	job domain.OutgoingMessageJob,
	keywordJobs keywordDeliveryRepository,
	until time.Time,
) error {
	const reason = "account_group_rest"
	enabled, err := s.groupRestIsEnabled(ctx)
	if err != nil || !enabled {
		return err
	}
	if keywordJobs != nil {
		err = keywordJobs.DelayTransientUntil(ctx, job.ID, reason, until.UTC())
	} else if jobs, ok := s.jobs.(interface {
		DelayTransientUntil(context.Context, domain.ID, string, time.Time) error
	}); ok {
		err = jobs.DelayTransientUntil(ctx, job.ID, reason, until.UTC())
	} else if jobs, ok := s.jobs.(domain.DelayedJobRepository); ok {
		err = jobs.DelayUntil(ctx, job.ID, reason, until.UTC())
	} else {
		return errors.New("outgoing job repository does not support exact rest delays")
	}
	if err != nil {
		return err
	}
	enabled, err = s.groupRestIsEnabled(ctx)
	if err != nil || enabled {
		return err
	}
	if releaser, ok := s.jobs.(interface {
		ReleaseGroupRestDelays(context.Context) error
	}); ok {
		return releaser.ReleaseGroupRestDelays(ctx)
	}
	return nil
}

func deliveryRequiresPublic(job domain.OutgoingMessageJob, account domain.Account) bool {
	switch job.Type {
	case domain.JobPublicReply:
		return true
	case domain.JobKeywordResponse:
		return !keywordPrivateTurn(job, account)
	default:
		return false
	}
}

func keywordPrivateTurn(job domain.OutgoingMessageJob, account domain.Account) bool {
	switch job.KeywordDeliveryMode {
	case domain.KeywordDeliveryModePrivate:
		return strings.TrimSpace(job.TargetTelegramID) != ""
	case domain.KeywordDeliveryModeComments:
		return false
	}
	return job.AllowPrivate &&
		strings.TrimSpace(job.TargetTelegramID) != "" &&
		account.EffectiveNextDelivery() == domain.DeliveryTargetPrivate
}

func findAccount(accounts []domain.Account, accountID domain.ID) *domain.Account {
	for i := range accounts {
		if accounts[i].ID == accountID {
			return &accounts[i]
		}
	}
	return nil
}

type DeliveryRunner struct {
	scheduler *Scheduler
	now       func() time.Time
	interval  time.Duration
}

func NewDeliveryRunner(scheduler *Scheduler, now func() time.Time) *DeliveryRunner {
	if now == nil {
		now = time.Now
	}
	return &DeliveryRunner{scheduler: scheduler, now: now, interval: 250 * time.Millisecond}
}

func (r *DeliveryRunner) Run(ctx context.Context) error {
	if r == nil || r.scheduler == nil {
		return errors.New("delivery runner is not configured")
	}
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		if err := r.scheduler.RunOnce(ctx, r.now().UTC()); err != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
