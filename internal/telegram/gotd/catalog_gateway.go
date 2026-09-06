package gotd

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/tgerr"

	"telegram-companion/internal/domain"
	"telegram-companion/internal/usecase/runtimeconfig"
)

var ErrUnsupportedCatalog = errors.New("unsupported catalog")

type CatalogRPC interface {
	ResolveChannel(context.Context, string) (domain.Channel, error)
	CheckMembership(context.Context, domain.Account, domain.Channel) (domain.ChannelMembership, error)
	JoinChannel(context.Context, domain.Account, domain.Channel) (domain.ChannelMembership, error)
}

type CatalogAssignmentResolver interface {
	Assigned(runtimeconfig.Snapshot, domain.ID, domain.SourceCatalog, domain.ID) bool
}

type FloodWaitError struct {
	AccountID domain.ID
	Duration  time.Duration
	Err       error
}

func (e *FloodWaitError) Error() string {
	if e.AccountID == "" {
		return fmt.Sprintf("Telegram FloodWait for %s", e.Duration)
	}
	return fmt.Sprintf("Telegram FloodWait for account %s: %s", e.AccountID, e.Duration)
}

func (e *FloodWaitError) Unwrap() error             { return e.Err }
func (e *FloodWaitError) RetryAfter() time.Duration { return e.Duration }

type CatalogGateway struct {
	accounts domain.AccountRepository
	catalogs domain.CatalogRepository
	channels domain.ChannelRepository
	rpc      CatalogRPC
	resolver CatalogAssignmentResolver

	mu       sync.RWMutex
	snapshot runtimeconfig.Snapshot
}

func NewCatalogGateway(accounts domain.AccountRepository, catalogs domain.CatalogRepository, channels domain.ChannelRepository, rpc CatalogRPC, resolvers ...CatalogAssignmentResolver) *CatalogGateway {
	var resolver CatalogAssignmentResolver
	if len(resolvers) > 0 {
		resolver = resolvers[0]
	}
	return &CatalogGateway{accounts: accounts, catalogs: catalogs, channels: channels, rpc: rpc, resolver: resolver}
}

func (g *CatalogGateway) ApplySnapshot(snapshot runtimeconfig.Snapshot) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if snapshot.Revision < g.snapshot.Revision {
		return
	}
	g.snapshot = cloneRuntimeSnapshot(snapshot)
}

func (g *CatalogGateway) currentSnapshot() runtimeconfig.Snapshot {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return cloneRuntimeSnapshot(g.snapshot)
}

func (g *CatalogGateway) ResolveAndJoin(ctx context.Context, catalog domain.SourceCatalog, rawLink string) (domain.Channel, error) {
	role, err := catalogRole(catalog)
	if err != nil {
		return domain.Channel{}, err
	}
	link, err := normalizeCatalogLink(rawLink)
	if err != nil {
		return domain.Channel{}, err
	}
	channel, err := g.rpc.ResolveChannel(ctx, link)
	if err != nil {
		return domain.Channel{}, err
	}
	channel.Link = link
	if err := g.catalogs.Save(ctx, catalog, channel); err != nil {
		return domain.Channel{}, err
	}
	snapshot := g.currentSnapshot()
	if !catalogContainsChannel(snapshot, catalog, channel.ID) || g.resolver == nil {
		return channel, nil
	}
	accounts, err := g.accounts.ListActive(ctx)
	if err != nil {
		return domain.Channel{}, err
	}
	for _, account := range accounts {
		if account.Role != role || !account.Eligible() || !g.resolver.Assigned(snapshot, account.ID, catalog, channel.ID) {
			continue
		}
		membership, err := g.rpc.CheckMembership(ctx, account, channel)
		if err != nil {
			return domain.Channel{}, g.persistAccountError(ctx, account, channel, err)
		}
		if !membership.IsMember && membership.Status != membershipPendingApproval {
			membership, err = g.rpc.JoinChannel(ctx, account, channel)
			if err != nil {
				if isInviteRequestSent(err) {
					membership = pendingApprovalMembership(account.ID, channel.ID, time.Now().UTC())
					if saveErr := g.channels.SaveMembership(ctx, membership); saveErr != nil {
						return domain.Channel{}, saveErr
					}
					continue
				}
				return domain.Channel{}, g.persistAccountError(ctx, account, channel, err)
			}
		}
		if membership.AccountID == "" {
			membership.AccountID = account.ID
		}
		if membership.ChannelID == "" {
			membership.ChannelID = channel.ID
		}
		if err := g.channels.SaveMembership(ctx, membership); err != nil {
			return domain.Channel{}, err
		}
	}
	return channel, nil
}

func catalogContainsChannel(snapshot runtimeconfig.Snapshot, catalog domain.SourceCatalog, channelID domain.ID) bool {
	for _, assignedID := range snapshot.CatalogAssignments[catalog] {
		if assignedID == channelID {
			return true
		}
	}
	return false
}

func (g *CatalogGateway) persistAccountError(ctx context.Context, account domain.Account, channel domain.Channel, rpcErr error) error {
	scoped := accountRPCError(account.ID, rpcErr)
	var flood *FloodWaitError
	if !errors.As(scoped, &flood) {
		return scoped
	}
	membership := domain.ChannelMembership{
		AccountID: account.ID,
		ChannelID: channel.ID,
		Status:    "flood_wait",
		LastError: rpcErr.Error(),
	}
	if persistErr := g.channels.SaveMembership(ctx, membership); persistErr != nil {
		return errors.Join(scoped, persistErr)
	}
	return scoped
}

func catalogRole(catalog domain.SourceCatalog) (domain.AccountRole, error) {
	switch catalog {
	case domain.SourceCatalogOutbound:
		return domain.AccountRoleSpammer, nil
	case domain.SourceCatalogScout:
		return domain.AccountRoleScoutAnalyst, nil
	default:
		return "", ErrUnsupportedCatalog
	}
}

func normalizeCatalogLink(raw string) (string, error) {
	link := strings.TrimSuffix(strings.TrimSpace(raw), "/")
	if link == "" {
		return "", errors.New("catalog link is required")
	}
	if strings.HasPrefix(link, "@") {
		return "https://t.me/" + strings.TrimPrefix(link, "@"), nil
	}
	if strings.HasPrefix(link, "tg://join?") {
		parsed, err := url.Parse(link)
		if err != nil || parsed.Query().Get("invite") == "" {
			return "", errors.New("invalid Telegram invite link")
		}
		return "https://t.me/+" + parsed.Query().Get("invite"), nil
	}
	parsed, err := url.Parse(link)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("invalid Telegram catalog link")
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "t.me" && host != "telegram.me" && host != "telegram.dog" {
		return "", errors.New("catalog link must use a Telegram host")
	}
	path := strings.Trim(parsed.EscapedPath(), "/")
	if path == "" {
		return "", errors.New("telegram catalog link has no target")
	}
	if strings.HasPrefix(path, "joinchat/") {
		path = "+" + strings.TrimPrefix(path, "joinchat/")
	}
	return "https://t.me/" + path, nil
}

type floodWaitCarrier interface {
	FloodWait() time.Duration
}

func accountRPCError(accountID domain.ID, err error) error {
	var existing *FloodWaitError
	if errors.As(err, &existing) {
		copy := *existing
		copy.AccountID = accountID
		return &copy
	}
	if duration, ok := tgerr.AsFloodWait(err); ok {
		return &FloodWaitError{AccountID: accountID, Duration: duration, Err: err}
	}
	var flood floodWaitCarrier
	if errors.As(err, &flood) {
		return &FloodWaitError{AccountID: accountID, Duration: flood.FloodWait(), Err: err}
	}
	return err
}
