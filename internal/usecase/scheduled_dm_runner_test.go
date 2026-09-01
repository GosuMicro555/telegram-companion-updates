package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"telegram-companion/internal/domain"
)

type scheduledDMRunnerRepositoryFake struct {
	delivery  *domain.ScheduledDMDelivery
	tasks     []domain.ScheduledDMTask
	completed []domain.ScheduledDMDeliveryResult
	delays    []scheduledDMRunnerDelay
}

type scheduledDMRunnerDelay struct {
	deliveryID domain.ID
	leaseToken string
	reason     string
	next       time.Time
}

func (f *scheduledDMRunnerRepositoryFake) ListTasks(context.Context) ([]domain.ScheduledDMTask, error) {
	return append([]domain.ScheduledDMTask(nil), f.tasks...), nil
}
func (*scheduledDMRunnerRepositoryFake) SaveTask(context.Context, domain.ScheduledDMTask) error {
	return nil
}
func (*scheduledDMRunnerRepositoryFake) SetTaskStatus(context.Context, domain.ID, domain.ScheduledDMStatus, time.Time) error {
	return nil
}
func (f *scheduledDMRunnerRepositoryFake) ClaimDueDelivery(context.Context, time.Time, time.Duration) (*domain.ScheduledDMDelivery, error) {
	return f.delivery, nil
}
func (f *scheduledDMRunnerRepositoryFake) CompleteDelivery(_ context.Context, result domain.ScheduledDMDeliveryResult) error {
	f.completed = append(f.completed, result)
	return nil
}
func (f *scheduledDMRunnerRepositoryFake) DelayDelivery(_ context.Context, id domain.ID, leaseToken, reason string, next time.Time) error {
	f.delays = append(f.delays, scheduledDMRunnerDelay{deliveryID: id, leaseToken: leaseToken, reason: reason, next: next})
	return nil
}

type scheduledDMRunnerAccountsFake struct {
	accounts       []domain.Account
	activeAccounts []domain.Account
}

func (f scheduledDMRunnerAccountsFake) ListActive(context.Context) ([]domain.Account, error) {
	if f.activeAccounts != nil {
		return f.activeAccounts, nil
	}
	return f.accounts, nil
}
func (f scheduledDMRunnerAccountsFake) List(context.Context) ([]domain.Account, error) {
	return f.accounts, nil
}
func (scheduledDMRunnerAccountsFake) Save(context.Context, domain.Account) error { return nil }

type scheduledDMRunnerSenderFake struct {
	resolveErr error
	sendErr    error
	resolved   []domain.Account
	sent       []domain.Account
	texts      []string
}

func (f *scheduledDMRunnerSenderFake) ResolveUsername(_ context.Context, account domain.Account, _ string) error {
	f.resolved = append(f.resolved, account)
	return f.resolveErr
}
func (f *scheduledDMRunnerSenderFake) SendScheduledPrivateMessage(_ context.Context, account domain.Account, _ domain.ScheduledDMDelivery, messageText string) error {
	f.sent = append(f.sent, account)
	f.texts = append(f.texts, messageText)
	return f.sendErr
}

type scheduledDMRetryError struct{ after time.Duration }

func (e scheduledDMRetryError) Error() string             { return "temporary Telegram failure" }
func (e scheduledDMRetryError) RetryAfter() time.Duration { return e.after }

func TestScheduledDMRunnerResolvesAndSendsWithAssignedAccountAndTaskText(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	repository := scheduledDMRunnerRepository(now)
	accounts := scheduledDMRunnerAccountsFake{accounts: []domain.Account{
		{ID: "assigned", Role: domain.AccountRoleSpammer, Status: domain.AccountActive},
		{ID: "other", Role: domain.AccountRoleSpammer, Status: domain.AccountActive},
	}}
	sender := &scheduledDMRunnerSenderFake{}
	runner := NewScheduledDMRunner(repository, accounts, sender, func() time.Time { return now })

	require.NoError(t, runner.RunOnce(context.Background()))
	require.Equal(t, []domain.Account{accounts.accounts[0]}, sender.resolved)
	require.Equal(t, []domain.Account{accounts.accounts[0]}, sender.sent)
	require.Equal(t, []string{"task-specific text"}, sender.texts)
	require.Equal(t, []domain.ScheduledDMDeliveryResult{{
		DeliveryID: "delivery-1", LeaseToken: "lease-1", Status: domain.ScheduledDMDeliveryStatusSent, CompletedAt: now,
	}}, repository.completed)
	require.Empty(t, repository.delays)
}

func TestScheduledDMRunnerCompletesClosedRecipientWithoutAccountReassignment(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	repository := scheduledDMRunnerRepository(now)
	accounts := scheduledDMRunnerAccountsFake{accounts: []domain.Account{
		{ID: "assigned", Role: domain.AccountRoleSpammer, Status: domain.AccountActive},
		{ID: "other", Role: domain.AccountRoleSpammer, Status: domain.AccountActive},
	}}
	sender := &scheduledDMRunnerSenderFake{resolveErr: domain.PrivateMessageClosed(errors.New("USER_PRIVACY_RESTRICTED"))}
	runner := NewScheduledDMRunner(repository, accounts, sender, func() time.Time { return now })

	require.NoError(t, runner.RunOnce(context.Background()))
	require.Equal(t, []domain.Account{accounts.accounts[0]}, sender.resolved)
	require.Empty(t, sender.sent)
	require.Equal(t, []domain.ScheduledDMDeliveryResult{{
		DeliveryID: "delivery-1", LeaseToken: "lease-1", Status: domain.ScheduledDMDeliveryStatusClosed, CompletedAt: now, ErrorCode: "recipient_closed",
	}}, repository.completed)
	require.Empty(t, repository.delays)
}

func TestScheduledDMRunnerCompletesInvalidUsernameAsTerminal(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	repository := scheduledDMRunnerRepository(now)
	sender := &scheduledDMRunnerSenderFake{resolveErr: domain.PermanentDeliveryFailure(errors.New("USERNAME_NOT_OCCUPIED"))}
	runner := NewScheduledDMRunner(repository, scheduledDMRunnerAccountsFake{accounts: []domain.Account{{
		ID: "assigned", Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
	}}}, sender, func() time.Time { return now })

	require.NoError(t, runner.RunOnce(context.Background()))
	require.Equal(t, []domain.ScheduledDMDeliveryResult{{
		DeliveryID: "delivery-1", LeaseToken: "lease-1", Status: domain.ScheduledDMDeliveryStatusFailed, CompletedAt: now, ErrorCode: "recipient_invalid",
	}}, repository.completed)
	require.Empty(t, repository.delays)
}

func TestScheduledDMRunnerDelaysFloodWaitForAssignedDelivery(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	repository := scheduledDMRunnerRepository(now)
	sender := &scheduledDMRunnerSenderFake{sendErr: scheduledDMRetryError{after: 47 * time.Second}}
	runner := NewScheduledDMRunner(repository, scheduledDMRunnerAccountsFake{accounts: []domain.Account{{
		ID: "assigned", Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
	}}}, sender, func() time.Time { return now })

	require.NoError(t, runner.RunOnce(context.Background()))
	require.Empty(t, repository.completed)
	require.Equal(t, []scheduledDMRunnerDelay{{deliveryID: "delivery-1", leaseToken: "lease-1", reason: "send_failed", next: now.Add(47 * time.Second)}}, repository.delays)
}

func TestScheduledDMRunnerDelaysAssignedFloodWaitUntilPersistedDeadline(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	until := now.Add(47 * time.Second)
	repository := scheduledDMRunnerRepository(now)
	sender := &scheduledDMRunnerSenderFake{}
	runner := NewScheduledDMRunner(repository, scheduledDMRunnerAccountsFake{accounts: []domain.Account{{
		ID: "assigned", Role: domain.AccountRoleSpammer, Status: domain.AccountFloodWait, FloodWaitUntil: &until,
	}}}, sender, func() time.Time { return now })

	require.NoError(t, runner.RunOnce(context.Background()))
	require.Empty(t, sender.resolved)
	require.Empty(t, sender.sent)
	require.Empty(t, repository.completed)
	require.Equal(t, []scheduledDMRunnerDelay{{
		deliveryID: "delivery-1", leaseToken: "lease-1", reason: "assigned_account_unavailable", next: until,
	}}, repository.delays)
}

func TestScheduledDMRunnerSendsWithAssignedAccountAfterFloodWaitExpires(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	expired := now.Add(-time.Second)
	repository := scheduledDMRunnerRepository(now)
	accounts := scheduledDMRunnerAccountsFake{
		accounts: []domain.Account{{
			ID: "assigned", Role: domain.AccountRoleSpammer, Status: domain.AccountFloodWait, FloodWaitUntil: &expired,
		}},
		activeAccounts: []domain.Account{{
			ID: "assigned", Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
		}},
	}
	sender := &scheduledDMRunnerSenderFake{}
	runner := NewScheduledDMRunner(repository, accounts, sender, func() time.Time { return now })

	require.NoError(t, runner.RunOnce(context.Background()))
	require.Equal(t, []domain.Account{accounts.activeAccounts[0]}, sender.resolved)
	require.Equal(t, []domain.Account{accounts.activeAccounts[0]}, sender.sent)
	require.Empty(t, repository.delays)
}

func scheduledDMRunnerRepository(now time.Time) *scheduledDMRunnerRepositoryFake {
	return &scheduledDMRunnerRepositoryFake{
		delivery: &domain.ScheduledDMDelivery{ID: "delivery-1", TaskID: "task-1", Recipient: "consenting_contact", AccountID: "assigned", LeaseToken: "lease-1", TelegramRandomID: 991},
		tasks:    []domain.ScheduledDMTask{{ID: "task-1", MessageText: "task-specific text", StartAt: now, MaxRuns: 1}},
	}
}
