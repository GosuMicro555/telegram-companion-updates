package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"telegram-companion/internal/domain"
)

func TestJobRepositoryPersistsDelayRetryAndCompletion(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := Open(ctx, path)
	require.NoError(t, err)
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	repo := NewJobRepository(db, func() time.Time { return now })
	require.NoError(t, NewProductionStore(db).SaveCatalog(ctx, domain.SourceCatalogOutbound, domain.Channel{
		ID: "channel-1", TelegramID: "100", Link: "https://t.me/channel",
		Topic: "Channel", Active: true, Status: domain.ChannelReady, CreatedAt: now, UpdatedAt: now,
	}))
	job := domain.OutgoingMessageJob{
		ID: "job-1", Type: domain.JobPublicReply, ChannelID: "channel-1",
		ReplyToMessageID: "77", Status: "queued", NextAttemptAt: now, CreatedAt: now,
	}
	require.NoError(t, repo.Enqueue(ctx, job))
	require.NoError(t, db.Close())

	db, err = Open(ctx, path)
	require.NoError(t, err)
	defer db.Close()
	repo = NewJobRepository(db, func() time.Time { return now })
	require.NoError(t, NewProductionStore(db).EnsureAccounts(ctx, []domain.Account{{
		ID: "account-1", Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
		SessionPath: "/session", CreatedAt: now, UpdatedAt: now,
	}}))
	due, err := repo.NextDue(ctx)
	require.NoError(t, err)
	require.Equal(t, job.ID, due.ID)

	retryAt := now.Add(37 * time.Second)
	require.NoError(t, repo.DelayUntil(ctx, job.ID, "flood_wait", retryAt))
	due, err = repo.NextDue(ctx)
	require.NoError(t, err)
	require.Nil(t, due)
	now = retryAt
	due, err = repo.NextDue(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, due.Attempts)
	require.Equal(t, "delayed", due.Status)

	event := domain.OutgoingMessageEvent{ID: "event-1", JobID: job.ID, AccountID: "account-1", ChannelID: job.ChannelID, Type: job.Type, Success: true, CreatedAt: now}
	require.NoError(t, repo.MarkDone(ctx, job.ID, event))
	due, err = repo.NextDue(ctx)
	require.NoError(t, err)
	require.Nil(t, due)
	var status string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT status FROM outgoing_message_jobs WHERE id=?`, job.ID).Scan(&status))
	require.Equal(t, "done", status)
	var events int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outgoing_message_events WHERE job_id=?`, job.ID).Scan(&events))
	require.Equal(t, 1, events)
}

func TestJobRepositoryReleaseGroupRestDelaysPreservesOtherDelays(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "release-group-rest-delays.db"))
	require.NoError(t, err)
	defer db.Close()

	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	repo := NewJobRepository(db, func() time.Time { return now })
	for index, jobID := range []domain.ID{"rest-delayed", "flood-delayed"} {
		require.NoError(t, repo.Enqueue(ctx, domain.OutgoingMessageJob{
			ID: jobID, Type: domain.JobPublicReply, ChannelID: "channel-1",
			ReplyToMessageID: fmt.Sprintf("%d", index+1), Text: "reply",
			Status: "queued", NextAttemptAt: now, CreatedAt: now,
		}))
	}
	require.NoError(t, repo.DelayTransientUntil(ctx, "rest-delayed", "account_group_rest", now.Add(36*time.Hour)))
	require.NoError(t, repo.DelayTransientUntil(ctx, "flood-delayed", "flood_wait", now.Add(time.Hour)))

	require.NoError(t, repo.ReleaseGroupRestDelays(ctx))

	var status, lastError, nextAttemptAt string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT status,last_error,next_attempt_at
		FROM outgoing_message_jobs WHERE id='rest-delayed'`).Scan(&status, &lastError, &nextAttemptAt))
	require.Equal(t, "queued", status)
	require.Empty(t, lastError)
	require.Equal(t, formatTime(now), nextAttemptAt)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT status,last_error
		FROM outgoing_message_jobs WHERE id='flood-delayed'`).Scan(&status, &lastError))
	require.Equal(t, "delayed", status)
	require.Equal(t, "flood_wait", lastError)
}

func TestJobRepositoryDoesNotRestoreGroupRestDelayAfterFeatureIsDisabled(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "disabled-group-rest-delay.db"))
	require.NoError(t, err)
	defer db.Close()

	now := time.Date(2026, 7, 30, 11, 0, 0, 0, time.UTC)
	store := NewProductionStore(db)
	settings := domain.DefaultAppSettings()
	settings.GroupRestEnabled = false
	require.NoError(t, store.SaveAppSettings(ctx, settings))

	repo := NewJobRepository(db, func() time.Time { return now })
	require.NoError(t, repo.Enqueue(ctx, domain.OutgoingMessageJob{
		ID: "rest-race", Type: domain.JobPublicReply, ChannelID: "channel-1",
		ReplyToMessageID: "1", Text: "reply", Status: "queued",
		NextAttemptAt: now, CreatedAt: now,
	}))

	require.NoError(t, repo.DelayTransientUntil(ctx, "rest-race", "account_group_rest", now.Add(36*time.Hour)))

	var status, lastError string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT status,last_error FROM outgoing_message_jobs WHERE id='rest-race'`,
	).Scan(&status, &lastError))
	require.Equal(t, "queued", status)
	require.Empty(t, lastError)
}

func TestJobRepositoryDoesNotRestoreLeasedGroupRestDelayAfterFeatureIsDisabled(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "disabled-leased-group-rest-delay.db"))
	require.NoError(t, err)
	defer db.Close()

	now := time.Date(2026, 7, 30, 11, 30, 0, 0, time.UTC)
	store := NewProductionStore(db)
	settings := domain.DefaultAppSettings()
	settings.GroupRestEnabled = true
	require.NoError(t, store.SaveAppSettings(ctx, settings))
	require.NoError(t, store.EnsureAccounts(ctx, []domain.Account{{
		ID: "account-rest-lease", Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
		SessionPath: "/session/rest-lease", CreatedAt: now, UpdatedAt: now,
	}}))

	repo := NewJobRepository(db, func() time.Time { return now })
	job := domain.OutgoingMessageJob{
		ID: "rest-lease-race", Type: domain.JobPublicReply, ChannelID: "channel-1",
		ReplyToMessageID: "1", Text: "reply", Status: "queued",
		NextAttemptAt: now, CreatedAt: now,
	}
	require.NoError(t, repo.Enqueue(ctx, job))
	_, acquired, err := repo.Acquire(ctx, job.ID, "account-rest-lease", "rest-lease-token", now, now.Add(time.Minute))
	require.NoError(t, err)
	require.True(t, acquired)

	settings.GroupRestEnabled = false
	require.NoError(t, store.SaveAppSettings(ctx, settings))
	require.NoError(t, repo.DelayTransientLeaseUntil(ctx, job.ID, "rest-lease-token", "account_group_rest", now.Add(36*time.Hour)))

	var status, lastError, leaseToken, nextAttemptAt string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT status,last_error,lease_token,next_attempt_at FROM outgoing_message_jobs WHERE id=?`,
		job.ID,
	).Scan(&status, &lastError, &leaseToken, &nextAttemptAt))
	require.Equal(t, "queued", status)
	require.Empty(t, lastError)
	require.Empty(t, leaseToken)
	require.Equal(t, formatTime(now), nextAttemptAt)
}

func TestJobRepositoryCanDispatchRejectsRemovalRequestedChannel(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:job-dispatch-guard?mode=memory&cache=shared")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, Migrate(ctx, db))

	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	store := NewProductionStore(db)
	channel := domain.Channel{ID: "channel-1", TelegramID: "100", Link: "https://t.me/room", Topic: "Topic", Active: true, Status: domain.ChannelReady, CreatedAt: now, UpdatedAt: now}
	require.NoError(t, store.SaveCatalog(ctx, domain.SourceCatalogOutbound, channel))

	accountID := domain.ID("account-1")
	require.NoError(t, store.EnsureAccounts(ctx, []domain.Account{{
		ID: accountID, Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
		SessionPath: "/session", CreatedAt: now, UpdatedAt: now,
	}}))
	repo := NewJobRepository(db, func() time.Time { return now })
	require.NoError(t, repo.Enqueue(ctx, domain.OutgoingMessageJob{ID: "job-1", Type: domain.JobPublicReply, AccountID: &accountID, ChannelID: channel.ID, Status: "queued", NextAttemptAt: now, CreatedAt: now}))

	type dispatchGuard interface {
		CanDispatch(context.Context, domain.ID, domain.ID) (bool, error)
	}
	guard, ok := any(repo).(dispatchGuard)
	require.True(t, ok, "job repository must provide an authoritative dispatch check")

	allowed, err := guard.CanDispatch(ctx, "job-1", accountID)
	require.NoError(t, err)
	require.True(t, allowed)
	allowed, err = guard.CanDispatch(ctx, "job-1", "other-account")
	require.NoError(t, err)
	require.False(t, allowed)
	_, err = db.ExecContext(ctx, `UPDATE outbound_channels SET active=0 WHERE id=?`, channel.ID)
	require.NoError(t, err)
	allowed, err = guard.CanDispatch(ctx, "job-1", accountID)
	require.NoError(t, err)
	require.False(t, allowed)
	_, err = db.ExecContext(ctx, `UPDATE outbound_channels SET active=1 WHERE id=?`, channel.ID)
	require.NoError(t, err)
	require.NoError(t, store.RequestCatalogRemoval(ctx, domain.SourceCatalogOutbound, []domain.ID{channel.ID}))

	allowed, err = guard.CanDispatch(ctx, "job-1", accountID)
	require.NoError(t, err)
	require.False(t, allowed)
}

func TestJobRepositoryCanDispatchAllowsActiveScoutChannel(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:job-dispatch-scout-guard?mode=memory&cache=shared")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, Migrate(ctx, db))

	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	store := NewProductionStore(db)
	channel := domain.Channel{ID: "scout-channel-1", TelegramID: "200", Link: "https://t.me/scout", Topic: "Topic", Active: true, Status: domain.ChannelReady, CreatedAt: now, UpdatedAt: now}
	require.NoError(t, store.SaveCatalog(ctx, domain.SourceCatalogScout, channel))

	accountID := domain.ID("account-1")
	require.NoError(t, store.EnsureAccounts(ctx, []domain.Account{{
		ID: accountID, Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
		SessionPath: "/session", CreatedAt: now, UpdatedAt: now,
	}}))
	repo := NewJobRepository(db, func() time.Time { return now })
	require.NoError(t, repo.Enqueue(ctx, domain.OutgoingMessageJob{ID: "job-1", Type: domain.JobPublicReply, AccountID: &accountID, ChannelID: channel.ID, Status: "queued", NextAttemptAt: now, CreatedAt: now}))

	allowed, err := repo.CanDispatch(ctx, "job-1", accountID)

	require.NoError(t, err)
	require.True(t, allowed)
}

func TestJobRepositoryNextDueSkipsPausedChannelUntilReactivated(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:job-next-due-active-catalog?mode=memory&cache=shared")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, Migrate(ctx, db))

	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	store := NewProductionStore(db)
	paused := domain.Channel{
		ID: "channel-paused", TelegramID: "100", Link: "https://t.me/paused",
		Topic: "Paused", Active: false, Status: domain.ChannelPaused, CreatedAt: now, UpdatedAt: now,
	}
	active := domain.Channel{
		ID: "channel-active", TelegramID: "200", Link: "https://t.me/active",
		Topic: "Active", Active: true, Status: domain.ChannelReady, CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, store.SaveCatalog(ctx, domain.SourceCatalogOutbound, paused))
	require.NoError(t, store.SaveCatalog(ctx, domain.SourceCatalogOutbound, active))

	repo := NewJobRepository(db, func() time.Time { return now })
	require.NoError(t, repo.Enqueue(ctx, domain.OutgoingMessageJob{
		ID: "job-paused", Type: domain.JobPublicReply, ChannelID: paused.ID,
		Status: "queued", NextAttemptAt: now.Add(-time.Minute), CreatedAt: now.Add(-time.Minute),
	}))
	require.NoError(t, repo.Enqueue(ctx, domain.OutgoingMessageJob{
		ID: "job-active", Type: domain.JobPublicReply, ChannelID: active.ID,
		Status: "queued", NextAttemptAt: now, CreatedAt: now,
	}))

	due, err := repo.NextDue(ctx)
	require.NoError(t, err)
	require.NotNil(t, due)
	require.Equal(t, domain.ID("job-active"), due.ID)
	_, err = db.ExecContext(ctx, `UPDATE outgoing_message_jobs SET status='done' WHERE id=?`, due.ID)
	require.NoError(t, err)

	due, err = repo.NextDue(ctx)
	require.NoError(t, err)
	require.Nil(t, due)

	_, err = db.ExecContext(ctx, `UPDATE outbound_channels SET active=1,status='ready' WHERE id=?`, paused.ID)
	require.NoError(t, err)
	due, err = repo.NextDue(ctx)
	require.NoError(t, err)
	require.NotNil(t, due)
	require.Equal(t, domain.ID("job-paused"), due.ID)
}

func TestKeywordEnqueueRejectsIncompleteLiveAuditDraftWithoutCreatingJob(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	repo := NewJobRepository(db, time.Now)

	err = repo.EnqueueKeywordResponse(ctx, domain.OutgoingMessageJob{
		ID: "job-incomplete", Type: domain.JobKeywordResponse, ChannelID: "channel-1",
	}, domain.LiveDeliveryDraft{TriggerSnapshot: "money", TriggeredAt: time.Now().UTC()})
	require.Error(t, err)

	var jobs int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outgoing_message_jobs WHERE id='job-incomplete'`).Scan(&jobs))
	require.Zero(t, jobs)
}

func TestKeywordDeliveryModeAndFallbackPersistAcrossQueueRead(t *testing.T) {
	ctx, _, repo, now := newKeywordDeliveryFixture(t)
	accountID := domain.ID("account-1")
	job := domain.OutgoingMessageJob{
		ID:                  "job-private-mode",
		Type:                domain.JobKeywordResponse,
		AccountID:           &accountID,
		ChannelID:           "channel-1",
		ReplyToMessageID:    "77",
		Text:                "private reply",
		AllowPrivate:        true,
		KeywordDeliveryMode: domain.KeywordDeliveryModePrivate,
		FallbackToPublic:    true,
		Status:              "queued",
		NextAttemptAt:       now,
		CreatedAt:           now,
	}

	require.NoError(t, repo.EnqueueKeywordResponse(ctx, job, domain.LiveDeliveryDraft{
		SourceMessage: "money", TriggerCanonicalID: "money", TriggerSnapshot: "money", TriggeredAt: now,
	}))

	due, err := repo.NextDue(ctx)
	require.NoError(t, err)
	require.NotNil(t, due)
	require.Equal(t, domain.KeywordDeliveryModePrivate, due.KeywordDeliveryMode)
	require.True(t, due.FallbackToPublic)
}

func TestEnqueueKeywordResponseStoresNullCanonicalAfterMatchedOwnerWasDeleted(t *testing.T) {
	ctx, db, repo, now := newKeywordDeliveryFixture(t)
	_, err := db.ExecContext(ctx, `DELETE FROM canonical_keywords WHERE id='money'`)
	require.NoError(t, err)
	job := domain.OutgoingMessageJob{
		ID: "job-deleted-canonical", Type: domain.JobKeywordResponse, ChannelID: "channel-1",
		ReplyToMessageID: "77", Text: "reply", Status: "queued", NextAttemptAt: now, CreatedAt: now,
	}

	require.NoError(t, repo.EnqueueKeywordResponse(ctx, job, domain.LiveDeliveryDraft{
		SourceMessage: "where is the money", TriggerCanonicalID: "money",
		TriggerSnapshot: "money", TriggeredAt: now,
	}))

	var jobs int
	var canonicalID sql.NullString
	var sourceMessage, triggerSnapshot string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outgoing_message_jobs WHERE id=?`, job.ID).Scan(&jobs))
	require.Equal(t, 1, jobs)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT trigger_canonical_id,source_message,trigger_snapshot
		FROM live_delivery_history WHERE job_id=?`, job.ID).Scan(&canonicalID, &sourceMessage, &triggerSnapshot))
	require.False(t, canonicalID.Valid)
	require.Equal(t, "where is the money", sourceMessage)
	require.Equal(t, "money", triggerSnapshot)
}

func TestEnqueueKeywordResponseSerializesCanonicalDeleteBeforeAuditInsert(t *testing.T) {
	ctx, db, repo, now := newKeywordDeliveryFixture(t)
	deleteTx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = deleteTx.Rollback() })
	_, err = deleteTx.ExecContext(ctx, `DELETE FROM canonical_keywords WHERE id='money'`)
	require.NoError(t, err)

	started := make(chan struct{})
	completed := make(chan error, 1)
	go func() {
		close(started)
		completed <- repo.EnqueueKeywordResponse(ctx, domain.OutgoingMessageJob{
			ID: "job-concurrent-delete", Type: domain.JobKeywordResponse, ChannelID: "channel-1",
			ReplyToMessageID: "77", Text: "reply", Status: "queued", NextAttemptAt: now, CreatedAt: now,
		}, domain.LiveDeliveryDraft{
			SourceMessage: "money", TriggerCanonicalID: "money", TriggerSnapshot: "money", TriggeredAt: now,
		})
	}()
	<-started
	select {
	case enqueueErr := <-completed:
		require.Failf(t, "enqueue completed while delete held the writer lock", "error: %v", enqueueErr)
	case <-time.After(50 * time.Millisecond):
	}

	require.NoError(t, deleteTx.Commit())
	require.NoError(t, <-completed)

	var canonicalID sql.NullString
	var triggerSnapshot string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT trigger_canonical_id,trigger_snapshot
		FROM live_delivery_history WHERE job_id='job-concurrent-delete'`).Scan(&canonicalID, &triggerSnapshot))
	require.False(t, canonicalID.Valid)
	require.Equal(t, "money", triggerSnapshot)
}

func TestJobRepositoryClaimAndCompletionAreAtomicAndIdempotent(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	defer db.Close()

	now := time.Date(2026, 7, 12, 13, 0, 0, 0, time.UTC)
	store := NewProductionStore(db)
	require.NoError(t, store.EnsureAccounts(ctx, []domain.Account{{
		ID: "account-1", Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
		SessionPath: "/session/1", CreatedAt: now, UpdatedAt: now,
	}}))
	repo := NewJobRepository(db, func() time.Time { return now })
	job := domain.OutgoingMessageJob{
		ID: "job-atomic", Type: domain.JobPrivateMessage, Status: "queued",
		NextAttemptAt: now, CreatedAt: now,
	}
	require.NoError(t, repo.Enqueue(ctx, job))

	claimed, err := repo.Claim(ctx, job.ID, "account-1")
	require.NoError(t, err)
	require.Equal(t, domain.ID("account-1"), claimed)
	event := domain.OutgoingMessageEvent{
		ID: "event-atomic", JobID: job.ID, AccountID: claimed,
		Type: job.Type, Success: true, CreatedAt: now,
	}
	first, err := repo.Complete(ctx, job.ID, event)
	require.NoError(t, err)
	require.True(t, first)
	event.ID = "event-retry-must-not-persist"
	second, err := repo.Complete(ctx, job.ID, event)
	require.NoError(t, err)
	require.False(t, second)

	var events int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outgoing_message_events WHERE job_id=?`, job.ID).Scan(&events))
	require.Equal(t, 1, events)
	accounts, err := store.Accounts().List(ctx)
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.Equal(t, int64(1), accounts[0].PrivateMessagesSent)
	require.Equal(t, domain.AccountRoleSpammer, accounts[0].Role)
}

func TestJobRepositoryCompleteLeaseRequiresMatchingToken(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	now := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	store := NewProductionStore(db)
	require.NoError(t, store.EnsureAccounts(ctx, []domain.Account{{
		ID: "account-lease", Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
		SessionPath: "/session/lease", CreatedAt: now, UpdatedAt: now,
	}}))
	repo := NewJobRepository(db, func() time.Time { return now })
	job := domain.OutgoingMessageJob{
		ID: "job-lease", Type: domain.JobPrivateMessage, Status: "queued",
		NextAttemptAt: now, CreatedAt: now,
	}
	require.NoError(t, repo.Enqueue(ctx, job))
	claimed, acquired, err := repo.Acquire(ctx, job.ID, "account-lease", "lease-token", now, now.Add(time.Minute))
	require.NoError(t, err)
	require.True(t, acquired)
	require.Equal(t, domain.ID("account-lease"), claimed)
	event := domain.OutgoingMessageEvent{
		ID: "event-lease", JobID: job.ID, AccountID: claimed,
		Type: job.Type, Success: true, CreatedAt: now,
	}

	completed, err := repo.CompleteLease(ctx, job.ID, "wrong-token", event)
	require.EqualError(t, err, "outgoing job is not pending for claimed lease")
	require.False(t, completed)
	var status, leaseToken string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT status,lease_token FROM outgoing_message_jobs WHERE id=?`, job.ID).Scan(&status, &leaseToken))
	require.Equal(t, "queued", status)
	require.Equal(t, "lease-token", leaseToken)

	completed, err = repo.CompleteLease(ctx, job.ID, "lease-token", event)
	require.NoError(t, err)
	require.True(t, completed)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT status,lease_token FROM outgoing_message_jobs WHERE id=?`, job.ID).Scan(&status, &leaseToken))
	require.Equal(t, "done", status)
	require.Empty(t, leaseToken)
	var events int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outgoing_message_events WHERE job_id=?`, job.ID).Scan(&events))
	require.Equal(t, 1, events)
}

func TestJobRepositoryPersistsKeywordResponseAllowPrivate(t *testing.T) {
	ctx, db, repo, now := newKeywordDeliveryFixture(t)
	job := enqueueClaimedKeywordJob(t, ctx, repo, now, "job-load", true)

	due, err := repo.NextDue(ctx)
	require.NoError(t, err)
	require.Equal(t, job.ID, due.ID)
	require.Equal(t, domain.JobKeywordResponse, due.Type)
	require.True(t, due.AllowPrivate)

	var stored int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT allow_private FROM outgoing_message_jobs WHERE id=?`, job.ID).Scan(&stored))
	require.Equal(t, 1, stored)
}

func TestCompleteKeywordResponsePrivateSuccessAdvancesToPublic(t *testing.T) {
	ctx, db, repo, now := newKeywordDeliveryFixture(t)
	job := enqueueClaimedKeywordJob(t, ctx, repo, now, "job-private", true)
	advance := domain.DeliveryTargetPublic

	completed, err := repo.CompleteKeywordResponse(ctx, domain.KeywordDeliveryOutcome{
		JobID: job.ID, AccountID: "account-1", ChannelID: "channel-1",
		Type: domain.JobPrivateMessage, AdvanceTo: &advance, CompletedAt: now,
	})
	require.NoError(t, err)
	require.True(t, completed)
	assertKeywordDeliveryState(t, ctx, db, job.ID, "done", 0, 1, 0, domain.DeliveryTargetPublic, 1)
	assertChannelActivity(t, ctx, db, now, 1)
}

func TestKeywordDeliveryFixtureProvidesCanonicalTriggerForLiveAudit(t *testing.T) {
	ctx, _, repo, now := newKeywordDeliveryFixture(t)

	job := enqueueClaimedKeywordJobWithAudit(t, ctx, repo, now, "job-live-fixture", false)

	require.Equal(t, domain.ID("job-live-fixture"), job.ID)
}

func TestCompleteKeywordResponsePublicSuccessAlternatesToPrivate(t *testing.T) {
	ctx, db, repo, now := newKeywordDeliveryFixture(t)
	_, err := db.ExecContext(ctx, `UPDATE accounts SET next_delivery='public' WHERE id='account-1'`)
	require.NoError(t, err)
	job := enqueueClaimedKeywordJobWithAudit(t, ctx, repo, now, "job-public", false)
	advance := domain.DeliveryTargetPrivate

	completed, err := repo.CompleteKeywordResponse(ctx, domain.KeywordDeliveryOutcome{
		JobID: job.ID, AccountID: "account-1", ChannelID: "channel-1",
		Type: domain.JobPublicReply, AdvanceTo: &advance, CompletedAt: now,
	})
	require.NoError(t, err)
	require.True(t, completed)
	assertKeywordDeliveryState(t, ctx, db, job.ID, "done", 1, 0, 0, domain.DeliveryTargetPrivate, 1)
	assertLiveAuditFinalization(t, ctx, db, job.ID, "public_reply", "successful", "")
}

func TestCompleteKeywordResponsePublicOnlyLeavesCursorUnchanged(t *testing.T) {
	ctx, db, repo, now := newKeywordDeliveryFixture(t)
	job := enqueueClaimedKeywordJob(t, ctx, repo, now, "job-public-only", false)

	completed, err := repo.CompleteKeywordResponse(ctx, domain.KeywordDeliveryOutcome{
		JobID: job.ID, AccountID: "account-1", ChannelID: "channel-1",
		Type: domain.JobPublicReply, CompletedAt: now,
	})
	require.NoError(t, err)
	require.True(t, completed)
	assertKeywordDeliveryState(t, ctx, db, job.ID, "done", 1, 0, 0, domain.DeliveryTargetPrivate, 1)
}

func TestCompleteKeywordResponseFinalizesLiveAudit(t *testing.T) {
	ctx, db, repo, now := newKeywordDeliveryFixture(t)
	_, err := db.ExecContext(ctx, `UPDATE accounts SET display_name='Operator' WHERE id='account-1'`)
	require.NoError(t, err)
	job := enqueueClaimedKeywordJobWithAudit(t, ctx, repo, now, "job-live-success", true)
	advance := domain.DeliveryTargetPublic

	completed, err := repo.CompleteKeywordResponse(ctx, domain.KeywordDeliveryOutcome{
		JobID: job.ID, AccountID: "account-1", ChannelID: "channel-1", AccountTitleSnapshot: "Operator",
		Type: domain.JobPrivateMessage, AdvanceTo: &advance, CompletedAt: now,
	})
	require.NoError(t, err)
	require.True(t, completed)

	var deliveryType, accountID, title, status, errorCode, finalizedAt string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT delivery_type,account_id,account_title_snapshot,final_status,error_code,finalized_at FROM live_delivery_history WHERE job_id=?`, job.ID).
		Scan(&deliveryType, &accountID, &title, &status, &errorCode, &finalizedAt))
	require.Equal(t, "private_message", deliveryType)
	require.Equal(t, "account-1", accountID)
	require.Equal(t, "Operator", title)
	require.Equal(t, "successful", status)
	require.Empty(t, errorCode)
	require.Equal(t, formatTime(now), finalizedAt)
}

func TestCompleteKeywordResponseUsesAccountIDWhenTitleSnapshotIsEmpty(t *testing.T) {
	ctx, db, repo, now := newKeywordDeliveryFixture(t)
	job := enqueueClaimedKeywordJobWithAudit(t, ctx, repo, now, "job-live-empty-title", false)

	completed, err := repo.CompleteKeywordResponse(ctx, domain.KeywordDeliveryOutcome{
		JobID: job.ID, AccountID: "account-1", ChannelID: "channel-1",
		Type: domain.JobPublicReply, CompletedAt: now,
	})
	require.NoError(t, err)
	require.True(t, completed)

	var title string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT account_title_snapshot FROM live_delivery_history WHERE job_id=?`, job.ID).Scan(&title))
	require.Equal(t, "account-1", title)
}

func TestDelayKeywordFailureFinalizesOnlyAtEighthAttemptWithoutAccount(t *testing.T) {
	ctx, db, repo, now := newKeywordDeliveryFixture(t)
	job := enqueueKeywordJobWithAudit(t, ctx, repo, now, "job-live-terminal", false)
	failure := domain.KeywordDeliveryFailure{JobID: job.ID, ErrorCode: "no_eligible_account", FailedAt: now}

	for attempt := 1; attempt <= maxDeliveryAttempts; attempt++ {
		require.NoError(t, repo.DelayKeywordFailure(ctx, failure))
		var status sql.NullString
		var attempts int
		var accountID, deliveryType, finalStatus sql.NullString
		require.NoError(t, db.QueryRowContext(ctx, `SELECT account_id,delivery_type,final_status FROM live_delivery_history WHERE job_id=?`, job.ID).
			Scan(&accountID, &deliveryType, &finalStatus))
		require.False(t, accountID.Valid)
		require.False(t, deliveryType.Valid)
		if attempt < maxDeliveryAttempts {
			require.False(t, finalStatus.Valid)
			continue
		}
		require.True(t, finalStatus.Valid)
		require.Equal(t, "not_delivered", finalStatus.String)
		require.NoError(t, db.QueryRowContext(ctx, `SELECT status,attempts FROM outgoing_message_jobs WHERE id=?`, job.ID).Scan(&status, &attempts))
		require.Equal(t, "done", status.String)
		require.Equal(t, maxDeliveryAttempts, attempts)
	}
	assertLiveAuditFinalization(t, ctx, db, job.ID, "", "not_delivered", "no_eligible_account")
	require.NoError(t, repo.DelayKeywordFailure(ctx, failure))
	var rows int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM live_delivery_history WHERE job_id=?`, job.ID).Scan(&rows))
	require.Equal(t, 1, rows)
}

func TestDelayKeywordFailureFinalizesActualDeliveryTypeAndAccount(t *testing.T) {
	ctx, db, repo, now := newKeywordDeliveryFixture(t)
	job := enqueueClaimedKeywordJobWithAudit(t, ctx, repo, now, "job-live-send-failure", false)
	accountID := domain.ID("account-1")
	deliveryType := domain.JobPublicReply
	failure := domain.KeywordDeliveryFailure{
		JobID: job.ID, AccountID: &accountID, AccountTitleSnapshot: "Operator", Type: &deliveryType,
		ErrorCode: "send_failed", FailedAt: now,
	}

	for attempt := 0; attempt < maxDeliveryAttempts; attempt++ {
		require.NoError(t, repo.DelayKeywordFailure(ctx, failure))
	}
	assertLiveAuditFinalization(t, ctx, db, job.ID, "public_reply", "not_delivered", "send_failed")
	var account, title string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT account_id,account_title_snapshot FROM live_delivery_history WHERE job_id=?`, job.ID).Scan(&account, &title))
	require.Equal(t, "account-1", account)
	require.Equal(t, "Operator", title)
}

func TestDelayKeywordFailureRetainsRealAttemptMetadataWhenTerminalFailureHasNoAssignment(t *testing.T) {
	ctx, db, repo, now := newKeywordDeliveryFixture(t)
	job := enqueueKeywordJobWithAudit(t, ctx, repo, now, "job-live-retained-attempt", false)
	accountID := domain.ID("account-1")
	deliveryType := domain.JobPublicReply

	require.NoError(t, repo.DelayKeywordFailure(ctx, domain.KeywordDeliveryFailure{
		JobID: job.ID, AccountID: &accountID, AccountTitleSnapshot: "Operator", Type: &deliveryType,
		ErrorCode: "send_failed", FailedAt: now,
	}))

	var storedAccountID, storedDeliveryType, storedTitle sql.NullString
	var draftStatus sql.NullString
	require.NoError(t, db.QueryRowContext(ctx, `SELECT account_id,delivery_type,account_title_snapshot,final_status
		FROM live_delivery_history WHERE job_id=?`, job.ID).
		Scan(&storedAccountID, &storedDeliveryType, &storedTitle, &draftStatus))
	require.Equal(t, "account-1", storedAccountID.String)
	require.Equal(t, "public_reply", storedDeliveryType.String)
	require.Equal(t, "Operator", storedTitle.String)
	require.False(t, draftStatus.Valid)

	for attempt := 2; attempt <= maxDeliveryAttempts; attempt++ {
		require.NoError(t, repo.DelayKeywordFailure(ctx, domain.KeywordDeliveryFailure{
			JobID: job.ID, ErrorCode: "claimed_account_unavailable", FailedAt: now.Add(time.Duration(attempt) * time.Second),
		}))
	}

	var finalStatus, errorCode string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT account_id,delivery_type,account_title_snapshot,final_status,error_code
		FROM live_delivery_history WHERE job_id=?`, job.ID).
		Scan(&storedAccountID, &storedDeliveryType, &storedTitle, &finalStatus, &errorCode))
	require.Equal(t, "account-1", storedAccountID.String)
	require.Equal(t, "public_reply", storedDeliveryType.String)
	require.Equal(t, "Operator", storedTitle.String)
	require.Equal(t, "not_delivered", finalStatus)
	require.Equal(t, "claimed_account_unavailable", errorCode)
}

func TestCompleteKeywordResponseRejectsMismatchedChannelAndRollsBack(t *testing.T) {
	ctx, db, repo, now := newKeywordDeliveryFixture(t)
	job := enqueueClaimedKeywordJob(t, ctx, repo, now, "job-channel-mismatch", false)
	_, err := db.ExecContext(ctx, `INSERT INTO outbound_channels
		(id,telegram_chat_id,title,link,topic,status,active,created_at,updated_at)
		VALUES ('channel-2','102','Other','https://t.me/other','topic','ready',1,?,?)`, formatTime(now), formatTime(now))
	require.NoError(t, err)
	advance := domain.DeliveryTargetPublic

	completed, err := repo.CompleteKeywordResponse(ctx, domain.KeywordDeliveryOutcome{
		JobID: job.ID, AccountID: "account-1", ChannelID: "channel-2",
		Type: domain.JobPublicReply, AdvanceTo: &advance, CompletedAt: now,
	})
	require.Error(t, err)
	require.False(t, completed)
	assertKeywordDeliveryState(t, ctx, db, job.ID, "queued", 0, 0, 0, domain.DeliveryTargetPrivate, 0)
	assertChannelSentCount(t, ctx, db, "channel-1", 0)
	assertChannelSentCount(t, ctx, db, "channel-2", 0)
}

func TestCompleteKeywordResponseMissingStoredChannelRollsBack(t *testing.T) {
	ctx, db, repo, now := newKeywordDeliveryFixture(t)
	job := enqueueClaimedKeywordJob(t, ctx, repo, now, "job-channel-missing", false)
	_, err := db.ExecContext(ctx, `DELETE FROM outbound_channels WHERE id='channel-1'`)
	require.NoError(t, err)
	advance := domain.DeliveryTargetPublic

	completed, err := repo.CompleteKeywordResponse(ctx, domain.KeywordDeliveryOutcome{
		JobID: job.ID, AccountID: "account-1", ChannelID: "channel-1",
		Type: domain.JobPublicReply, AdvanceTo: &advance, CompletedAt: now,
	})
	require.Error(t, err)
	require.False(t, completed)
	assertKeywordDeliveryState(t, ctx, db, job.ID, "queued", 0, 0, 0, domain.DeliveryTargetPrivate, 0)
}

func TestRecordPrivateClosedAdvancesCursorAndKeepsClaimLease(t *testing.T) {
	ctx, db, repo, now := newKeywordDeliveryFixture(t)
	job := enqueueClaimedKeywordJob(t, ctx, repo, now, "job-closed", true)
	leaseUntil := now.Add(time.Minute)
	_, err := db.ExecContext(ctx, `UPDATE outgoing_message_jobs SET lease_token='lease-1',lease_until=? WHERE id=?`, formatTime(leaseUntil), job.ID)
	require.NoError(t, err)

	require.NoError(t, repo.RecordPrivateClosed(ctx, job.ID, "account-1", now))
	assertKeywordDeliveryState(t, ctx, db, job.ID, "queued", 0, 0, 1, domain.DeliveryTargetPublic, 1)
	var allowPrivate int
	var lastError, leaseToken, storedLeaseUntil string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT allow_private,last_error,lease_token,lease_until FROM outgoing_message_jobs WHERE id=?`, job.ID).
		Scan(&allowPrivate, &lastError, &leaseToken, &storedLeaseUntil))
	require.Zero(t, allowPrivate)
	require.Equal(t, "private_message_closed", lastError)
	require.Equal(t, "lease-1", leaseToken)
	require.Equal(t, formatTime(leaseUntil), storedLeaseUntil)
	assertEvent(t, ctx, db, domain.ID(string(job.ID)+":private_closed"), domain.JobPrivateMessage, false, "private_message_closed")
}

func TestRecordPrivateClosedThenPublicFallbackCompletesAtomically(t *testing.T) {
	ctx, db, repo, now := newKeywordDeliveryFixture(t)
	job := enqueueClaimedKeywordJobWithAudit(t, ctx, repo, now, "job-fallback", true)
	require.NoError(t, repo.RecordPrivateClosed(ctx, job.ID, "account-1", now))
	var attempts int
	var accountID, deliveryType, finalStatus sql.NullString
	require.NoError(t, db.QueryRowContext(ctx, `SELECT j.attempts,h.account_id,h.delivery_type,h.final_status
		FROM outgoing_message_jobs j JOIN live_delivery_history h ON h.job_id=j.id WHERE j.id=?`, job.ID).
		Scan(&attempts, &accountID, &deliveryType, &finalStatus))
	require.Zero(t, attempts)
	require.False(t, accountID.Valid)
	require.False(t, deliveryType.Valid)
	require.False(t, finalStatus.Valid)
	advance := domain.DeliveryTargetPrivate

	completed, err := repo.CompleteKeywordResponse(ctx, domain.KeywordDeliveryOutcome{
		JobID: job.ID, AccountID: "account-1", ChannelID: "channel-1",
		Type: domain.JobPublicReply, AdvanceTo: &advance, CompletedAt: now.Add(time.Second),
	})
	require.NoError(t, err)
	require.True(t, completed)
	assertKeywordDeliveryState(t, ctx, db, job.ID, "done", 1, 0, 1, domain.DeliveryTargetPrivate, 2)
	assertLiveAuditFinalization(t, ctx, db, job.ID, "public_reply", "successful", "private_message_closed")
}

func TestRecordPrivateClosedThenFallbackDelayKeepsPublicCursor(t *testing.T) {
	ctx, db, repo, now := newKeywordDeliveryFixture(t)
	job := enqueueClaimedKeywordJob(t, ctx, repo, now, "job-fallback-delay", true)
	require.NoError(t, repo.RecordPrivateClosed(ctx, job.ID, "account-1", now))

	require.NoError(t, repo.DelayUntil(ctx, job.ID, "public_fallback_failed", now.Add(time.Minute)))
	assertKeywordDeliveryState(t, ctx, db, job.ID, "delayed", 0, 0, 1, domain.DeliveryTargetPublic, 1)
	var allowPrivate, attempts int
	var lastError string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT allow_private,attempts,last_error FROM outgoing_message_jobs WHERE id=?`, job.ID).
		Scan(&allowPrivate, &attempts, &lastError))
	require.Zero(t, allowPrivate)
	require.Equal(t, 1, attempts)
	require.Equal(t, "public_fallback_failed", lastError)
	assertEvent(t, ctx, db, domain.ID(string(job.ID)+":private_closed"), domain.JobPrivateMessage, false, "private_message_closed")
}

func TestRecordPrivateClosedDuplicateDoesNotIncrementTwice(t *testing.T) {
	ctx, db, repo, now := newKeywordDeliveryFixture(t)
	job := enqueueClaimedKeywordJob(t, ctx, repo, now, "job-closed-duplicate", true)

	require.NoError(t, repo.RecordPrivateClosed(ctx, job.ID, "account-1", now))
	require.NoError(t, repo.RecordPrivateClosed(ctx, job.ID, "account-1", now.Add(time.Second)))
	assertKeywordDeliveryState(t, ctx, db, job.ID, "queued", 0, 0, 1, domain.DeliveryTargetPublic, 1)
}

func TestCompleteKeywordResponseDuplicateDoesNotAdvanceOrCountTwice(t *testing.T) {
	ctx, db, repo, now := newKeywordDeliveryFixture(t)
	job := enqueueClaimedKeywordJob(t, ctx, repo, now, "job-complete-duplicate", true)
	advance := domain.DeliveryTargetPublic
	outcome := domain.KeywordDeliveryOutcome{
		JobID: job.ID, AccountID: "account-1", ChannelID: "channel-1",
		Type: domain.JobPrivateMessage, AdvanceTo: &advance, CompletedAt: now,
	}

	first, err := repo.CompleteKeywordResponse(ctx, outcome)
	require.NoError(t, err)
	second, err := repo.CompleteKeywordResponse(ctx, outcome)
	require.NoError(t, err)
	require.True(t, first)
	require.False(t, second)
	assertKeywordDeliveryState(t, ctx, db, job.ID, "done", 0, 1, 0, domain.DeliveryTargetPublic, 1)
}

func TestCompleteKeywordResponseConcurrentDuplicateCountsOnce(t *testing.T) {
	ctx, db, repo, now := newKeywordDeliveryFixture(t)
	job := enqueueClaimedKeywordJob(t, ctx, repo, now, "job-complete-race", false)
	advance := domain.DeliveryTargetPrivate
	outcome := domain.KeywordDeliveryOutcome{
		JobID: job.ID, AccountID: "account-1", ChannelID: "channel-1",
		Type: domain.JobPublicReply, AdvanceTo: &advance, CompletedAt: now,
	}

	start := make(chan struct{})
	results := make(chan bool, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			completed, err := repo.CompleteKeywordResponse(ctx, outcome)
			results <- completed
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var completed int
	for result := range results {
		if result {
			completed++
		}
	}
	require.Equal(t, 1, completed)
	assertKeywordDeliveryState(t, ctx, db, job.ID, "done", 1, 0, 0, domain.DeliveryTargetPrivate, 1)
}

func newKeywordDeliveryFixture(t *testing.T) (context.Context, *sql.DB, *JobRepository, time.Time) {
	t.Helper()
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	now := time.Date(2026, 7, 15, 11, 0, 0, 0, time.UTC)
	store := NewProductionStore(db)
	require.NoError(t, store.EnsureAccounts(ctx, []domain.Account{{
		ID: "account-1", Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
		SessionPath: "/sessions/account-1", CreatedAt: now, UpdatedAt: now,
	}}))
	_, err = db.ExecContext(ctx, `INSERT INTO canonical_keywords
		(id,canonical_value,language,created_at,updated_at)
		VALUES ('money','money','en',?,?)`, formatTime(now), formatTime(now))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO outbound_channels
		(id,telegram_chat_id,title,link,topic,status,active,created_at,updated_at)
		VALUES ('channel-1','101','Channel','https://t.me/channel','topic','ready',1,?,?)`, formatTime(now), formatTime(now))
	require.NoError(t, err)
	return ctx, db, NewJobRepository(db, func() time.Time { return now }), now
}

func enqueueClaimedKeywordJob(t *testing.T, ctx context.Context, repo *JobRepository, now time.Time, id domain.ID, allowPrivate bool) domain.OutgoingMessageJob {
	t.Helper()
	accountID := domain.ID("account-1")
	job := domain.OutgoingMessageJob{
		ID: id, Type: domain.JobKeywordResponse, AccountID: &accountID, ChannelID: "channel-1",
		ReplyToMessageID: "77", Text: "reply", AllowPrivate: allowPrivate,
		Status: "queued", NextAttemptAt: now, CreatedAt: now,
	}
	require.NoError(t, repo.Enqueue(ctx, job))
	return job
}

func enqueueClaimedKeywordJobWithAudit(t *testing.T, ctx context.Context, repo *JobRepository, now time.Time, id domain.ID, allowPrivate bool) domain.OutgoingMessageJob {
	t.Helper()
	accountID := domain.ID("account-1")
	job := domain.OutgoingMessageJob{ID: id, Type: domain.JobKeywordResponse, AccountID: &accountID, ChannelID: "channel-1", ReplyToMessageID: "77", Text: "reply", AllowPrivate: allowPrivate, Status: "queued", NextAttemptAt: now, CreatedAt: now}
	require.NoError(t, repo.EnqueueKeywordResponse(ctx, job, domain.LiveDeliveryDraft{SourceMessage: "money", TriggerCanonicalID: "money", TriggerSnapshot: "money", TriggeredAt: now}))
	return job
}

func enqueueKeywordJobWithAudit(t *testing.T, ctx context.Context, repo *JobRepository, now time.Time, id domain.ID, allowPrivate bool) domain.OutgoingMessageJob {
	t.Helper()
	job := domain.OutgoingMessageJob{ID: id, Type: domain.JobKeywordResponse, ChannelID: "channel-1", ReplyToMessageID: "77", Text: "reply", AllowPrivate: allowPrivate, Status: "queued", NextAttemptAt: now, CreatedAt: now}
	require.NoError(t, repo.EnqueueKeywordResponse(ctx, job, domain.LiveDeliveryDraft{SourceMessage: "money", TriggerCanonicalID: "money", TriggerSnapshot: "money", TriggeredAt: now}))
	return job
}

func assertKeywordDeliveryState(t *testing.T, ctx context.Context, db *sql.DB, jobID domain.ID, status string, publicSent, privateSent, privateClosed int64, next domain.DeliveryTarget, events int) {
	t.Helper()
	var gotStatus string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT status FROM outgoing_message_jobs WHERE id=?`, jobID).Scan(&gotStatus))
	require.Equal(t, status, gotStatus)
	var gotPublic, gotPrivate, gotClosed int64
	var gotNext string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT public_replies_sent,private_messages_sent,private_messages_closed,next_delivery FROM accounts WHERE id='account-1'`).
		Scan(&gotPublic, &gotPrivate, &gotClosed, &gotNext))
	require.Equal(t, publicSent, gotPublic)
	require.Equal(t, privateSent, gotPrivate)
	require.Equal(t, privateClosed, gotClosed)
	require.Equal(t, next, domain.DeliveryTarget(gotNext))
	var gotEvents int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outgoing_message_events WHERE job_id=?`, jobID).Scan(&gotEvents))
	require.Equal(t, events, gotEvents)
}

func assertChannelActivity(t *testing.T, ctx context.Context, db *sql.DB, at time.Time, sent int64) {
	t.Helper()
	var gotSent int64
	var lastActivity string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT sent_count,last_activity_at FROM outbound_channels WHERE id='channel-1'`).Scan(&gotSent, &lastActivity))
	require.Equal(t, sent, gotSent)
	require.Equal(t, formatTime(at), lastActivity)
}

func assertChannelSentCount(t *testing.T, ctx context.Context, db *sql.DB, channelID domain.ID, sent int64) {
	t.Helper()
	var got int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT sent_count FROM outbound_channels WHERE id=?`, channelID).Scan(&got))
	require.Equal(t, sent, got)
}

func assertEvent(t *testing.T, ctx context.Context, db *sql.DB, eventID domain.ID, eventType domain.JobType, success bool, errorCode string) {
	t.Helper()
	var gotType, gotError string
	var gotSuccess int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT type,success,error_code FROM outgoing_message_events WHERE id=?`, eventID).
		Scan(&gotType, &gotSuccess, &gotError))
	require.Equal(t, eventType, domain.JobType(gotType))
	require.Equal(t, boolInt(success), gotSuccess)
	require.Equal(t, errorCode, gotError)
}

func assertLiveAuditFinalization(t *testing.T, ctx context.Context, db *sql.DB, jobID domain.ID, deliveryType, status, errorCode string) {
	t.Helper()
	var gotType, gotStatus, gotError sql.NullString
	require.NoError(t, db.QueryRowContext(ctx, `SELECT delivery_type,final_status,error_code FROM live_delivery_history WHERE job_id=?`, jobID).
		Scan(&gotType, &gotStatus, &gotError))
	if deliveryType == "" {
		require.False(t, gotType.Valid)
	} else {
		require.Equal(t, deliveryType, gotType.String)
	}
	require.Equal(t, status, gotStatus.String)
	require.Equal(t, errorCode, gotError.String)
}
