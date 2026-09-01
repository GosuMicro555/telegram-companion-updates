package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"telegram-companion/internal/domain"

	"github.com/stretchr/testify/require"
)

var _ domain.ScheduledDMRepository = (*ScheduledDMRepository)(nil)

func TestMigrationBackupLifecyclePausesPersistedActiveScheduledDMTasksUntilExplicitStart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "scheduled-dm.sqlite")
	db, err := openScheduledDMTestDatabase(ctx, path)
	require.NoError(t, err)

	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	repo := NewScheduledDMRepository(db, func() time.Time { return now })
	require.NoError(t, NewProductionStore(db).EnsureAccounts(ctx, []domain.Account{
		{ID: "account-1", Role: domain.AccountRoleSpammer, Status: domain.AccountActive, SessionPath: "/sessions/account-1", CreatedAt: now, UpdatedAt: now},
		{ID: "account-2", Role: domain.AccountRoleSpammer, Status: domain.AccountActive, SessionPath: "/sessions/account-2", CreatedAt: now, UpdatedAt: now},
	}))
	first := scheduledDMTask(now)
	second := scheduledDMTask(now)
	second.ID = "task-2"
	require.NoError(t, repo.SaveTask(ctx, first))
	require.NoError(t, repo.SaveTask(ctx, second))
	require.NoError(t, repo.SetTaskStatus(ctx, first.ID, domain.ScheduledDMStatusActive, now))
	require.NoError(t, repo.SetTaskStatus(ctx, second.ID, domain.ScheduledDMStatusActive, now))
	require.NoError(t, db.Close())

	db, err = sql.Open("sqlite", path)
	require.NoError(t, err)
	require.NoError(t, Configure(ctx, db))
	require.NoError(t, MigrateWithBackup(ctx, db, nil))
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	repo = NewScheduledDMRepository(db, func() time.Time { return now })
	tasks, err := repo.ListTasks(ctx)
	require.NoError(t, err)
	require.Len(t, tasks, 2)
	require.Equal(t, domain.ScheduledDMStatusPaused, tasks[0].Status)
	require.Equal(t, domain.ScheduledDMStatusPaused, tasks[1].Status)

	require.NoError(t, repo.SetTaskStatus(ctx, first.ID, domain.ScheduledDMStatusActive, now))
	tasks, err = repo.ListTasks(ctx)
	require.NoError(t, err)
	require.Equal(t, domain.ScheduledDMStatusActive, tasks[0].Status)
	require.Equal(t, domain.ScheduledDMStatusPaused, tasks[1].Status)
}

func TestScheduledDMRepositoryRoundTripsPausedTask(t *testing.T) {
	ctx, db, _, repo, now := scheduledDMTestRepository(t)
	task := scheduledDMTask(now)
	task.Status = domain.ScheduledDMStatusActive

	require.NoError(t, repo.SaveTask(ctx, task))

	tasks, err := repo.ListTasks(ctx)
	require.NoError(t, err)
	require.Equal(t, []domain.ScheduledDMTask{{
		ID: "task-1", MessageText: "hello", Status: domain.ScheduledDMStatusPaused,
		StartAt: now.Add(time.Hour), Recurrence: domain.ScheduledDMRecurrenceFiveMinutes,
		MaxRuns: 2, CompletedRuns: 0, NextRunAt: scheduledDMTimePtr(now.Add(time.Hour)),
		Recipients: []domain.ScheduledDMRecipient{
			{TaskID: "task-1", Ordinal: 0, Username: "alice", Status: domain.ScheduledDMRecipientStatusUnchecked},
			{TaskID: "task-1", Ordinal: 1, Username: "bob", Status: domain.ScheduledDMRecipientStatusResolved, LastError: "", LastCheckedAt: scheduledDMTimePtr(now)},
		},
		AccountIDs: []domain.ID{"account-1", "account-2"}, CreatedAt: now, UpdatedAt: now,
	}}, tasks)

	var recipientCount, accountCount int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduled_dm_recipients WHERE task_id='task-1'`).Scan(&recipientCount))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduled_dm_accounts WHERE task_id='task-1'`).Scan(&accountCount))
	require.Equal(t, 2, recipientCount)
	require.Equal(t, 2, accountCount)
}

func TestClaimDueDeliveryMaterializesRunAtomicallyWithStableAssignments(t *testing.T) {
	ctx, db, _, repo, now := scheduledDMTestRepository(t)
	task := scheduledDMTask(now)
	task.StartAt = now
	task.NextRunAt = scheduledDMTimePtr(now)
	require.NoError(t, repo.SaveTask(ctx, task))
	require.NoError(t, repo.SetTaskStatus(ctx, task.ID, domain.ScheduledDMStatusActive, now))

	claimed, err := repo.ClaimDueDelivery(ctx, now, time.Minute)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.Equal(t, domain.ID("task-1:run:1:delivery:0"), claimed.ID)
	require.Equal(t, domain.ID("account-1"), claimed.AccountID)
	require.NotZero(t, claimed.TelegramRandomID)

	var runs, deliveries int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduled_dm_runs WHERE task_id='task-1'`).Scan(&runs))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduled_dm_deliveries WHERE task_id='task-1'`).Scan(&deliveries))
	require.Equal(t, 1, runs)
	require.Equal(t, 2, deliveries)

	var accountID string
	var randomID int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT account_id,telegram_random_id FROM scheduled_dm_deliveries
		WHERE id='task-1:run:1:delivery:1'`).Scan(&accountID, &randomID))
	require.Equal(t, "account-2", accountID)
	require.NotZero(t, randomID)
}

func TestClaimDueDeliveryRollsBackRunWhenDeliveriesCannotMaterialize(t *testing.T) {
	ctx, db, _, repo, now := scheduledDMTestRepository(t)
	task := scheduledDMTask(now)
	task.StartAt = now
	task.NextRunAt = scheduledDMTimePtr(now)
	require.NoError(t, repo.SaveTask(ctx, task))
	_, err := db.ExecContext(ctx, `DELETE FROM scheduled_dm_recipients WHERE task_id=?`, task.ID)
	require.NoError(t, err)
	require.NoError(t, repo.SetTaskStatus(ctx, task.ID, domain.ScheduledDMStatusActive, now))

	_, err = repo.ClaimDueDelivery(ctx, now, time.Minute)
	require.Error(t, err)
	var runs, deliveries int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduled_dm_runs WHERE task_id=?`, task.ID).Scan(&runs))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduled_dm_deliveries WHERE task_id=?`, task.ID).Scan(&deliveries))
	require.Zero(t, runs)
	require.Zero(t, deliveries)
}

func TestClaimDueDeliveryRecoversExpiredLeaseWithoutChangingSenderOrRandomID(t *testing.T) {
	ctx, _, _, repo, now := scheduledDMTestRepository(t)
	task := scheduledDMTask(now)
	task.StartAt = now
	task.NextRunAt = scheduledDMTimePtr(now)
	task.Recipients = task.Recipients[:1]
	require.NoError(t, repo.SaveTask(ctx, task))
	require.NoError(t, repo.SetTaskStatus(ctx, task.ID, domain.ScheduledDMStatusActive, now))

	first, err := repo.ClaimDueDelivery(ctx, now, time.Minute)
	require.NoError(t, err)
	require.NotNil(t, first)
	second, err := repo.ClaimDueDelivery(ctx, now.Add(2*time.Minute), time.Minute)
	require.NoError(t, err)
	require.NotNil(t, second)
	require.Equal(t, first.ID, second.ID)
	require.Equal(t, first.AccountID, second.AccountID)
	require.Equal(t, first.TelegramRandomID, second.TelegramRandomID)
	require.NotEmpty(t, first.LeaseToken)
	require.NotEmpty(t, second.LeaseToken)
	require.NotEqual(t, first.LeaseToken, second.LeaseToken)
}

func TestCompleteDeliveryRejectsStaleOwnerAfterLeaseRecovery(t *testing.T) {
	ctx, db, _, repo, now := scheduledDMLeaseRecoveryRepository(t)

	stale, err := repo.ClaimDueDelivery(ctx, now, time.Minute)
	require.NoError(t, err)
	current, err := repo.ClaimDueDelivery(ctx, now.Add(2*time.Minute), time.Minute)
	require.NoError(t, err)
	require.Equal(t, "owner-1", stale.LeaseToken)
	require.Equal(t, "owner-2", current.LeaseToken)

	err = repo.CompleteDelivery(ctx, domain.ScheduledDMDeliveryResult{
		DeliveryID: stale.ID, LeaseToken: stale.LeaseToken,
		Status: domain.ScheduledDMDeliveryStatusSent, CompletedAt: now.Add(2 * time.Minute),
	})
	require.Error(t, err)
	assertScheduledDMLease(t, ctx, db, current.ID, "sending", current.LeaseToken, "")

	require.NoError(t, repo.CompleteDelivery(ctx, domain.ScheduledDMDeliveryResult{
		DeliveryID: current.ID, LeaseToken: current.LeaseToken,
		Status: domain.ScheduledDMDeliveryStatusSent, CompletedAt: now.Add(2 * time.Minute),
	}))
}

func TestCompleteDeliveryMarksInvalidUsernameRecipientInvalid(t *testing.T) {
	ctx, db, _, repo, now := scheduledDMTestRepository(t)
	task := scheduledDMTask(now)
	task.StartAt = now
	task.NextRunAt = scheduledDMTimePtr(now)
	task.Recipients = task.Recipients[:1]
	require.NoError(t, repo.SaveTask(ctx, task))
	require.NoError(t, repo.SetTaskStatus(ctx, task.ID, domain.ScheduledDMStatusActive, now))

	claimed, err := repo.ClaimDueDelivery(ctx, now, time.Minute)
	require.NoError(t, err)
	require.NoError(t, repo.CompleteDelivery(ctx, domain.ScheduledDMDeliveryResult{
		DeliveryID: claimed.ID, LeaseToken: claimed.LeaseToken,
		Status: domain.ScheduledDMDeliveryStatusFailed, CompletedAt: now, ErrorCode: "recipient_invalid",
	}))

	var status, lastError string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT peer_status,last_error FROM scheduled_dm_recipients WHERE task_id=? AND username=?`, task.ID, claimed.Recipient).Scan(&status, &lastError))
	require.Equal(t, string(domain.ScheduledDMRecipientStatusInvalid), status)
	require.Equal(t, "recipient_invalid", lastError)
}

func TestDelayDeliveryRejectsStaleOwnerAfterLeaseRecovery(t *testing.T) {
	ctx, db, _, repo, now := scheduledDMLeaseRecoveryRepository(t)

	stale, err := repo.ClaimDueDelivery(ctx, now, time.Minute)
	require.NoError(t, err)
	current, err := repo.ClaimDueDelivery(ctx, now.Add(2*time.Minute), time.Minute)
	require.NoError(t, err)
	require.Equal(t, "owner-1", stale.LeaseToken)
	require.Equal(t, "owner-2", current.LeaseToken)

	err = repo.DelayDelivery(ctx, stale.ID, stale.LeaseToken, "stale delay", now.Add(10*time.Minute))
	require.Error(t, err)
	assertScheduledDMLease(t, ctx, db, current.ID, "sending", current.LeaseToken, "")

	require.NoError(t, repo.DelayDelivery(ctx, current.ID, current.LeaseToken, "flood wait", now.Add(10*time.Minute)))
	assertScheduledDMLease(t, ctx, db, current.ID, "pending", "", "flood wait")
}

func TestCompleteDeliverySchedulesNextRunFromPriorScheduledTimeAfterRestart(t *testing.T) {
	ctx, db, path, repo, now := scheduledDMTestRepository(t)
	task := scheduledDMTask(now)
	task.StartAt = now
	task.NextRunAt = scheduledDMTimePtr(now)
	task.Recipients = task.Recipients[:1]
	require.NoError(t, repo.SaveTask(ctx, task))
	require.NoError(t, repo.SetTaskStatus(ctx, task.ID, domain.ScheduledDMStatusActive, now))

	claimed, err := repo.ClaimDueDelivery(ctx, now, time.Minute)
	require.NoError(t, err)
	require.NoError(t, repo.CompleteDelivery(ctx, domain.ScheduledDMDeliveryResult{
		DeliveryID: claimed.ID, LeaseToken: claimed.LeaseToken,
		Status: domain.ScheduledDMDeliveryStatusSent, CompletedAt: now.Add(9 * time.Minute),
	}))
	require.NoError(t, db.Close())

	db, err = openScheduledDMTestDatabase(ctx, path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	repo = NewScheduledDMRepository(db, func() time.Time { return now.Add(9 * time.Minute) })
	tasks, err := repo.ListTasks(ctx)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, 1, tasks[0].CompletedRuns)
	require.Equal(t, scheduledDMTimePtr(now.Add(5*time.Minute)), tasks[0].NextRunAt)
	require.Equal(t, domain.ScheduledDMStatusPaused, tasks[0].Status)
	require.NoError(t, repo.SetTaskStatus(ctx, task.ID, domain.ScheduledDMStatusActive, now.Add(9*time.Minute)))

	next, err := repo.ClaimDueDelivery(ctx, now.Add(9*time.Minute), time.Minute)
	require.NoError(t, err)
	require.NotNil(t, next)
	require.Equal(t, 2, next.RunNumber)
}

func scheduledDMTestRepository(t *testing.T) (context.Context, *sql.DB, string, *ScheduledDMRepository, time.Time) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "scheduled-dm.sqlite")
	db, err := openScheduledDMTestDatabase(ctx, path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, NewProductionStore(db).EnsureAccounts(ctx, []domain.Account{
		{ID: "account-1", Role: domain.AccountRoleSpammer, Status: domain.AccountActive, SessionPath: "/sessions/account-1", CreatedAt: now, UpdatedAt: now},
		{ID: "account-2", Role: domain.AccountRoleSpammer, Status: domain.AccountActive, SessionPath: "/sessions/account-2", CreatedAt: now, UpdatedAt: now},
	}))
	return ctx, db, path, NewScheduledDMRepository(db, func() time.Time { return now }), now
}

func scheduledDMLeaseRecoveryRepository(t *testing.T) (context.Context, *sql.DB, string, *ScheduledDMRepository, time.Time) {
	t.Helper()
	ctx, db, path, repo, now := scheduledDMTestRepository(t)
	tokens := []string{"owner-1", "owner-2"}
	repo.leaseToken = func() (string, error) {
		token := tokens[0]
		tokens = tokens[1:]
		return token, nil
	}
	task := scheduledDMTask(now)
	task.StartAt = now
	task.NextRunAt = scheduledDMTimePtr(now)
	task.Recipients = task.Recipients[:1]
	require.NoError(t, repo.SaveTask(ctx, task))
	require.NoError(t, repo.SetTaskStatus(ctx, task.ID, domain.ScheduledDMStatusActive, now))
	return ctx, db, path, repo, now
}

func assertScheduledDMLease(t *testing.T, ctx context.Context, db *sql.DB, deliveryID domain.ID, status, leaseToken, errorCode string) {
	t.Helper()
	var gotStatus, gotLeaseToken, gotErrorCode string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT status,lease_token,error_code FROM scheduled_dm_deliveries WHERE id=?`, deliveryID).
		Scan(&gotStatus, &gotLeaseToken, &gotErrorCode))
	require.Equal(t, []string{status, leaseToken, errorCode}, []string{gotStatus, gotLeaseToken, gotErrorCode})
}

func scheduledDMTask(now time.Time) domain.ScheduledDMTask {
	return domain.ScheduledDMTask{
		ID: "task-1", MessageText: "hello", Status: domain.ScheduledDMStatusPaused,
		StartAt: now.Add(time.Hour), Recurrence: domain.ScheduledDMRecurrenceFiveMinutes,
		MaxRuns: 2, Recipients: []domain.ScheduledDMRecipient{
			{Username: "alice", Status: domain.ScheduledDMRecipientStatusUnchecked},
			{Username: "bob", Status: domain.ScheduledDMRecipientStatusResolved, LastCheckedAt: scheduledDMTimePtr(now)},
		},
		AccountIDs: []domain.ID{"account-1", "account-2"}, CreatedAt: now, UpdatedAt: now,
	}
}

func openScheduledDMTestDatabase(ctx context.Context, path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if err := Configure(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := Migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func scheduledDMTimePtr(value time.Time) *time.Time { return &value }
