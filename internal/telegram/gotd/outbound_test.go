package gotd

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"telegram-companion/internal/domain"
	"telegram-companion/internal/repository/sqlite"
	"telegram-companion/internal/usecase"
	"telegram-companion/internal/usecase/runtimeconfig"
)

type outboundConfigFake struct{ snapshot runtimeconfig.Snapshot }

func (f *outboundConfigFake) Current() runtimeconfig.Snapshot { return f.snapshot }

type outboundAPIFake struct {
	texts          []string
	err            error
	scheduled      []domain.ScheduledDMDelivery
	scheduledTexts []string
	resolved       []domain.Account
}

type blockingOutboundAPI struct {
	started chan struct{}
	release chan struct{}
}

func (f *blockingOutboundAPI) SendPublicReply(context.Context, domain.Account, domain.OutgoingMessageJob) error {
	close(f.started)
	<-f.release
	return nil
}
func (f *blockingOutboundAPI) SendPrivateMessage(context.Context, domain.Account, domain.OutgoingMessageJob) error {
	return errors.New("unexpected private send")
}

func (f *outboundAPIFake) SendPublicReply(_ context.Context, _ domain.Account, job domain.OutgoingMessageJob) error {
	f.texts = append(f.texts, job.Text)
	return f.err
}

func (f *outboundAPIFake) SendPrivateMessage(_ context.Context, _ domain.Account, job domain.OutgoingMessageJob) error {
	f.texts = append(f.texts, job.Text)
	return f.err
}

func (f *outboundAPIFake) SendScheduledPrivateMessage(_ context.Context, _ domain.Account, delivery domain.ScheduledDMDelivery, messageText string) error {
	f.scheduled = append(f.scheduled, delivery)
	f.scheduledTexts = append(f.scheduledTexts, messageText)
	return f.err
}

func (f *outboundAPIFake) ResolveUsername(_ context.Context, account domain.Account, _ string) error {
	f.resolved = append(f.resolved, account)
	return f.err
}

type outboundAccountRepo struct {
	accounts   map[domain.ID]domain.Account
	leaseCalls []domain.ID
}

func (r *outboundAccountRepo) ListActive(context.Context) ([]domain.Account, error) {
	return r.List(context.Background())
}
func (r *outboundAccountRepo) List(context.Context) ([]domain.Account, error) {
	result := make([]domain.Account, 0, len(r.accounts))
	for _, account := range r.accounts {
		result = append(result, account)
	}
	return result, nil
}
func (r *outboundAccountRepo) Save(_ context.Context, account domain.Account) error {
	r.accounts[account.ID] = account
	return nil
}
func (r *outboundAccountRepo) WithAccountLease(_ context.Context, accountID domain.ID, action func(domain.Account) error) error {
	r.leaseCalls = append(r.leaseCalls, accountID)
	account, ok := r.accounts[accountID]
	if !ok {
		return errors.New("outbound account not found")
	}
	return action(account)
}
func (r *outboundAccountRepo) RecordFloodWait(_ context.Context, accountID domain.ID, until, at time.Time) error {
	account := r.accounts[accountID]
	account.Status = domain.AccountFloodWait
	account.FloodWaitUntil = &until
	account.LastError = "telegram flood wait"
	account.UpdatedAt = at
	r.accounts[accountID] = account
	return nil
}

func TestOutboundReadsExactHotReplyImmediatelyBeforeEachSend(t *testing.T) {
	config := &outboundConfigFake{snapshot: runtimeconfig.Snapshot{SharedReply: "old", RateLimits: runtimeconfig.RateLimits{RepliesPerMinute: 19, MinIntervalSeconds: 2}}}
	api := &outboundAPIFake{}
	repo := activeSpammerRepo("spam")
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	sender := NewOutbound(config, repo, api, func() time.Time { return now })
	account := repo.accounts["spam"]

	require.NoError(t, sender.SendPublicReply(context.Background(), account, domain.OutgoingMessageJob{Text: "queued"}))
	config.snapshot.SharedReply = "  new\nreply  "
	now = now.Add(2 * time.Second)
	require.NoError(t, sender.SendPublicReply(context.Background(), account, domain.OutgoingMessageJob{Text: "stale"}))
	require.Equal(t, []string{"old", "  new\nreply  "}, api.texts)
}

func TestOutboundSelectsSharedReplyForPublicAndFallbackDelivery(t *testing.T) {
	config := &outboundConfigFake{snapshot: runtimeconfig.Snapshot{
		SharedReply: "comment reply", PrivateReply: "private reply",
		RateLimits: runtimeconfig.RateLimits{RepliesPerMinute: 19, MinIntervalSeconds: 2},
	}}
	api := &outboundAPIFake{}
	repo := activeSpammerRepo("spam")
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	sender := NewOutbound(config, repo, api, func() time.Time { return now })

	require.NoError(t, sender.SendPrivateMessage(context.Background(), repo.accounts["spam"], domain.OutgoingMessageJob{Text: "queued"}))
	now = now.Add(2 * time.Second)
	require.NoError(t, sender.SendPublicReply(context.Background(), repo.accounts["spam"], domain.OutgoingMessageJob{Text: "fallback"}))

	require.Equal(t, []string{"private reply", "comment reply"}, api.texts)
}

func TestOutboundUsesSharedReplyForLegacyPrivateDeliveryWithoutPrivateReply(t *testing.T) {
	config := &outboundConfigFake{snapshot: runtimeconfig.Snapshot{
		SharedReply: "legacy shared reply",
		RateLimits:  runtimeconfig.RateLimits{RepliesPerMinute: 19, MinIntervalSeconds: 2},
	}}
	api := &outboundAPIFake{}
	repo := activeSpammerRepo("spam")
	sender := NewOutbound(config, repo, api, time.Now)

	require.NoError(t, sender.SendPrivateMessage(context.Background(), repo.accounts["spam"], domain.OutgoingMessageJob{Text: "queued"}))
	require.Equal(t, []string{"legacy shared reply"}, api.texts)
}

func TestOutboundSendsScheduledTaskTextWithoutGlobalReplySubstitution(t *testing.T) {
	config := &outboundConfigFake{snapshot: runtimeconfig.Snapshot{
		SharedReply: "global public reply", PrivateReply: "global private reply",
		RateLimits: runtimeconfig.RateLimits{RepliesPerMinute: 19, MinIntervalSeconds: 2},
	}}
	api := &outboundAPIFake{}
	repo := activeSpammerRepo("spam")
	sender := NewOutbound(config, repo, api, time.Now)
	delivery := domain.ScheduledDMDelivery{ID: "delivery-1", AccountID: "spam", Recipient: "consenting_contact", TelegramRandomID: 991}

	require.NoError(t, sender.SendScheduledPrivateMessage(context.Background(), repo.accounts["spam"], delivery, "task-specific text"))
	require.Equal(t, []domain.ScheduledDMDelivery{delivery}, api.scheduled)
	require.Equal(t, []string{"task-specific text"}, api.scheduledTexts)
	require.Empty(t, api.texts)
}

func TestOutboundRejectsEmptyScheduledTaskText(t *testing.T) {
	api := &outboundAPIFake{}
	repo := activeSpammerRepo("spam")
	sender := NewOutbound(&outboundConfigFake{}, repo, api, time.Now)

	err := sender.SendScheduledPrivateMessage(context.Background(), repo.accounts["spam"], domain.ScheduledDMDelivery{}, " \n ")

	require.ErrorIs(t, err, ErrOutboundEmptyReply)
	require.True(t, domain.IsPermanentDeliveryFailure(err))
	require.Empty(t, api.scheduled)
}

func TestOutboundResolvesScheduledUsernameWithAssignedAccount(t *testing.T) {
	api := &outboundAPIFake{}
	repo := activeSpammerRepo("assigned")
	sender := NewOutbound(&outboundConfigFake{}, repo, api, time.Now)

	require.NoError(t, sender.ResolveUsername(context.Background(), repo.accounts["assigned"], "consenting_contact"))
	require.Equal(t, []domain.Account{repo.accounts["assigned"]}, api.resolved)
}

func TestOutboundResolveUsernameUsesAccountLeaseAndPersistsFloodWait(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	api := &outboundAPIFake{err: &FloodWaitError{Duration: 47 * time.Second, Err: errors.New("FLOOD_WAIT")}}
	repo := activeSpammerRepo("assigned")
	sender := NewOutbound(&outboundConfigFake{}, repo, api, func() time.Time { return now })
	assigned := repo.accounts["assigned"]

	err := sender.ResolveUsername(context.Background(), assigned, "consenting_contact")

	require.Error(t, err)
	require.Equal(t, []domain.ID{"assigned"}, repo.leaseCalls)
	require.Equal(t, []domain.Account{assigned}, api.resolved)
	require.Equal(t, domain.AccountFloodWait, repo.accounts["assigned"].Status)
	require.Equal(t, now.Add(47*time.Second), *repo.accounts["assigned"].FloodWaitUntil)
}

func TestOutboundDoesNotCallTelegramForEmptySelectedReply(t *testing.T) {
	config := &outboundConfigFake{snapshot: runtimeconfig.Snapshot{
		SharedReply: "comment reply", PrivateReply: "", PrivateReplyPresent: true,
		RateLimits: runtimeconfig.RateLimits{RepliesPerMinute: 19, MinIntervalSeconds: 2},
	}}
	api := &outboundAPIFake{}
	repo := activeSpammerRepo("spam")
	sender := NewOutbound(config, repo, api, time.Now)

	err := sender.SendPrivateMessage(context.Background(), repo.accounts["spam"], domain.OutgoingMessageJob{Text: "queued"})

	require.True(t, domain.IsPermanentDeliveryFailure(err))
	require.NotContains(t, err.Error(), "queued")
	require.Empty(t, api.texts)
}

func TestOutboundRejectsScoutAndDoesNotCallTelegram(t *testing.T) {
	config := &outboundConfigFake{snapshot: runtimeconfig.Snapshot{SharedReply: "reply", RateLimits: runtimeconfig.RateLimits{RepliesPerMinute: 19, MinIntervalSeconds: 2}}}
	api := &outboundAPIFake{}
	repo := activeSpammerRepo("spam")
	sender := NewOutbound(config, repo, api, time.Now)

	err := sender.SendPublicReply(context.Background(), domain.Account{ID: "scout", Role: domain.AccountRoleScoutAnalyst, Status: domain.AccountActive}, domain.OutgoingMessageJob{})

	require.ErrorIs(t, err, ErrScoutSendRejected)
	require.Empty(t, api.texts)
}

func TestOutboundReloadsAuthoritativeRoleImmediatelyBeforeTelegramCall(t *testing.T) {
	config := &outboundConfigFake{snapshot: runtimeconfig.Snapshot{SharedReply: "reply", RateLimits: runtimeconfig.RateLimits{RepliesPerMinute: 19, MinIntervalSeconds: 2}}}
	api := &outboundAPIFake{}
	repo := activeSpammerRepo("changed")
	authoritative := repo.accounts["changed"]
	authoritative.Role = domain.AccountRoleScoutAnalyst
	repo.accounts["changed"] = authoritative
	sender := NewOutbound(config, repo, api, time.Now)

	err := sender.SendPublicReply(context.Background(), domain.Account{ID: "changed", Role: domain.AccountRoleSpammer, Status: domain.AccountActive}, domain.OutgoingMessageJob{})

	require.ErrorIs(t, err, ErrScoutSendRejected)
	require.Empty(t, api.texts)
}

func TestConcurrentScoutRoleTransitionWaitsForSendLeaseAndCannotBeOverwritten(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store := sqlite.NewProductionStore(db)
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.EnsureAccounts(ctx, []domain.Account{{
		ID: "account", Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
		SessionPath: "/sessions/opaque.session", CreatedAt: now, UpdatedAt: now,
	}}))
	api := &blockingOutboundAPI{started: make(chan struct{}), release: make(chan struct{})}
	outbound := NewOutbound(
		&outboundConfigFake{snapshot: runtimeconfig.Snapshot{SharedReply: "reply", RateLimits: runtimeconfig.RateLimits{RepliesPerMinute: 19, MinIntervalSeconds: 2}}},
		store.Accounts(), api, func() time.Time { return now },
	)
	sendDone := make(chan error, 1)
	go func() {
		sendDone <- outbound.SendPublicReply(ctx, domain.Account{ID: "account", Role: domain.AccountRoleSpammer}, domain.OutgoingMessageJob{ID: "job"})
	}()
	<-api.started

	roleDone := make(chan error, 1)
	go func() {
		_, err := usecase.NewAccountService(store).SetRole(ctx, "account", domain.AccountRoleScoutAnalyst)
		roleDone <- err
	}()
	select {
	case err := <-roleDone:
		t.Fatalf("role transition bypassed active send lease: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(api.release)
	require.NoError(t, <-sendDone)
	require.NoError(t, <-roleDone)

	accounts, err := store.ListAccounts(ctx)
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.Equal(t, domain.AccountRoleScoutAnalyst, accounts[0].Role)
	require.Zero(t, accounts[0].PublicRepliesSent, "delivery accounting belongs to atomic job completion")
}

func TestOutboundEnforcesSpacingAndPerMinuteLimit(t *testing.T) {
	config := &outboundConfigFake{snapshot: runtimeconfig.Snapshot{SharedReply: "reply", RateLimits: runtimeconfig.RateLimits{RepliesPerMinute: 2, MinIntervalSeconds: 2}}}
	api := &outboundAPIFake{}
	repo := activeSpammerRepo("spam")
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	sender := NewOutbound(config, repo, api, func() time.Time { return now })
	account := repo.accounts["spam"]

	require.NoError(t, sender.SendPublicReply(context.Background(), account, domain.OutgoingMessageJob{}))
	now = now.Add(time.Second)
	require.ErrorIs(t, sender.SendPublicReply(context.Background(), account, domain.OutgoingMessageJob{}), ErrOutboundRateLimited)
	now = now.Add(time.Second)
	require.NoError(t, sender.SendPublicReply(context.Background(), account, domain.OutgoingMessageJob{}))
	now = now.Add(2 * time.Second)
	require.ErrorIs(t, sender.SendPublicReply(context.Background(), account, domain.OutgoingMessageJob{}), ErrOutboundRateLimited)
	require.Len(t, api.texts, 2)
}

func TestOutboundFloodWaitIsolatesOnlyAffectedAccount(t *testing.T) {
	config := &outboundConfigFake{snapshot: runtimeconfig.Snapshot{SharedReply: "reply", RateLimits: runtimeconfig.RateLimits{RepliesPerMinute: 19, MinIntervalSeconds: 2}}}
	api := &outboundAPIFake{err: &FloodWaitError{Duration: time.Minute, Err: errors.New("FLOOD_WAIT")}}
	repo := activeSpammerRepo("spam-1", "spam-2")
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	sender := NewOutbound(config, repo, api, func() time.Time { return now })

	err := sender.SendPublicReply(context.Background(), repo.accounts["spam-1"], domain.OutgoingMessageJob{})
	require.Error(t, err)
	require.Equal(t, domain.AccountFloodWait, repo.accounts["spam-1"].Status)
	require.NotNil(t, repo.accounts["spam-1"].FloodWaitUntil)
	require.Equal(t, now.Add(time.Minute), *repo.accounts["spam-1"].FloodWaitUntil)
	require.Equal(t, domain.AccountActive, repo.accounts["spam-2"].Status)

	api.err = nil
	now = now.Add(2 * time.Second)
	require.NoError(t, sender.SendPublicReply(context.Background(), repo.accounts["spam-2"], domain.OutgoingMessageJob{}))
	require.Zero(t, repo.accounts["spam-2"].PublicRepliesSent, "outbound RPC must not account before durable completion")
}

func TestOutboundClassifiesUnmarkedAPIFailureAsTransient(t *testing.T) {
	config := &outboundConfigFake{snapshot: runtimeconfig.Snapshot{SharedReply: "reply", RateLimits: runtimeconfig.RateLimits{RepliesPerMinute: 19, MinIntervalSeconds: 2}}}
	api := &outboundAPIFake{err: errors.New("temporary transport failure")}
	repo := activeSpammerRepo("spam")
	sender := NewOutbound(config, repo, api, time.Now)

	err := sender.SendPublicReply(context.Background(), repo.accounts["spam"], domain.OutgoingMessageJob{})

	require.True(t, domain.IsTransientDeliveryFailure(err))
}

func activeSpammerRepo(ids ...domain.ID) *outboundAccountRepo {
	repo := &outboundAccountRepo{accounts: make(map[domain.ID]domain.Account, len(ids))}
	for _, id := range ids {
		repo.accounts[id] = domain.Account{ID: id, Role: domain.AccountRoleSpammer, Status: domain.AccountActive}
	}
	return repo
}
