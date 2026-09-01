package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"telegram-companion/internal/domain"
)

func TestLiveDeliveryHistoryRejectsUnknownFinalStatus(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	_, err = db.ExecContext(ctx, `INSERT INTO live_delivery_history
		(id,job_id,source_message,trigger_snapshot,triggered_at,final_status)
		VALUES ('audit-1','job-1','where to get money','money','2026-07-15T11:00:00Z','unknown')`)
	require.Error(t, err)
}

func TestLiveDeliveryHistoryDoesNotStoreAuthorID(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	rows, err := db.QueryContext(ctx, `PRAGMA table_info(live_delivery_history)`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue sql.NullString
		require.NoError(t, rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey))
		require.NotEqual(t, "author_id", name)
	}
	require.NoError(t, rows.Err())
}

func TestKeywordEnqueueCreatesAnonymizedLiveAuditAtomically(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	now := time.Date(2026, 7, 15, 11, 0, 0, 0, time.UTC)
	repo := NewJobRepository(db, func() time.Time { return now })
	_, err = db.ExecContext(ctx, `INSERT INTO canonical_keywords
		(id,canonical_value,language,created_at,updated_at)
		VALUES ('money','money','en',?,?)`, formatTime(now), formatTime(now))
	require.NoError(t, err)
	job := domain.OutgoingMessageJob{
		ID: "job-1", Type: domain.JobKeywordResponse, ChannelID: "channel-1",
	}
	draft := domain.LiveDeliveryDraft{
		SourceMessage: "where to get money", TriggerCanonicalID: "money",
		TriggerSnapshot: "money", TriggeredAt: now,
	}

	require.NoError(t, repo.EnqueueKeywordResponse(ctx, job, draft))
	var message, trigger string
	var status sql.NullString
	require.NoError(t, db.QueryRowContext(ctx, `SELECT source_message,trigger_snapshot,final_status
		FROM live_delivery_history WHERE job_id=?`, job.ID).Scan(&message, &trigger, &status))
	require.Equal(t, "where to get money", message)
	require.Equal(t, "money", trigger)
	require.False(t, status.Valid)

	var jobs int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outgoing_message_jobs WHERE id=?`, job.ID).Scan(&jobs))
	require.Equal(t, 1, jobs)
}

func TestKeywordEnqueueRejectsConflictingExistingJobWithoutLiveAudit(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	repo := NewJobRepository(db, func() time.Time { return now })

	require.NoError(t, repo.Enqueue(ctx, domain.OutgoingMessageJob{
		ID: "job-conflict", Type: domain.JobPublicReply, ChannelID: "channel-existing", RuleID: "rule-existing",
		TargetTelegramID: "target-existing", ReplyToMessageID: "reply-existing",
	}))
	err = repo.EnqueueKeywordResponse(ctx, domain.OutgoingMessageJob{
		ID: "job-conflict", Type: domain.JobKeywordResponse, ChannelID: "channel-requested", RuleID: "rule-requested",
		TargetTelegramID: "target-requested", ReplyToMessageID: "reply-requested",
	}, domain.LiveDeliveryDraft{
		SourceMessage: "where to get money", TriggerSnapshot: "money", TriggeredAt: now,
	})
	require.Error(t, err)

	var audits int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM live_delivery_history WHERE job_id='job-conflict'`).Scan(&audits))
	require.Zero(t, audits)
}

func TestKeywordEnqueueReplaysMatchingJobAndDraftIdempotently(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	repo := NewJobRepository(db, func() time.Time { return now })
	job := domain.OutgoingMessageJob{
		ID: "job-replay", Type: domain.JobKeywordResponse, ChannelID: "channel-1", RuleID: "rule-1",
		TargetTelegramID: "target-1", ReplyToMessageID: "reply-1",
	}
	draft := domain.LiveDeliveryDraft{
		SourceMessage: "where to get money", TriggerSnapshot: "money", TriggeredAt: now,
	}

	require.NoError(t, repo.EnqueueKeywordResponse(ctx, job, draft))
	require.NoError(t, repo.EnqueueKeywordResponse(ctx, job, draft))

	var jobs, audits int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outgoing_message_jobs WHERE id=?`, job.ID).Scan(&jobs))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM live_delivery_history WHERE job_id=?`, job.ID).Scan(&audits))
	require.Equal(t, 1, jobs)
	require.Equal(t, 1, audits)
}

func TestLiveDeliveryStatisticsReturnsNewestFinalizedRowsFirst(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store := NewProductionStore(db)

	_, err = db.ExecContext(ctx, `INSERT INTO live_delivery_history
		(id,job_id,source_message,trigger_snapshot,triggered_at,final_status,finalized_at)
		VALUES
		('audit-old','job-old','old message','old','2026-07-15T11:00:00Z','successful','2026-07-15T11:00:01Z'),
		('audit-new','job-new','new message','new','2026-07-15T12:00:00Z','successful','2026-07-15T12:00:01Z'),
		('audit-draft','job-draft','draft message','draft','2026-07-15T13:00:00Z',NULL,NULL)`)
	require.NoError(t, err)

	page, err := store.LiveDeliveryStatistics(ctx, domain.LiveDeliveryQuery{Limit: 50})
	require.NoError(t, err)
	require.Equal(t, 2, page.Total)
	require.Len(t, page.Rows, 2)
	require.Equal(t, domain.ID("audit-new"), page.Rows[0].ID)
	require.Equal(t, "new message", page.Rows[0].SourceMessage)
	require.Equal(t, domain.ID("audit-old"), page.Rows[1].ID)
	for _, row := range page.Rows {
		require.NotEqual(t, domain.ID("audit-draft"), row.ID)
	}
}

func TestLiveDeliveryStatisticsFallsBackToStoredAccountDetailsForLegacyAuditRows(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store := NewProductionStore(db)
	_, err = db.ExecContext(ctx, `INSERT INTO accounts
		(id,phone_masked,display_name,role,status,session_path,created_at,updated_at)
		VALUES ('account-1','+7 *** 1234','','spammer','ready','/sessions/account-1','2026-07-15T10:00:00Z','2026-07-15T10:00:00Z')`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO live_delivery_history
		(id,job_id,source_message,trigger_snapshot,triggered_at,account_id,account_title_snapshot,final_status,finalized_at)
		VALUES ('audit-legacy','job-legacy','message','money','2026-07-15T11:00:00Z','account-1','','successful','2026-07-15T11:00:01Z')`)
	require.NoError(t, err)

	page, err := store.LiveDeliveryStatistics(ctx, domain.LiveDeliveryQuery{Limit: 50})
	require.NoError(t, err)
	require.Len(t, page.Rows, 1)
	require.Equal(t, "+7 *** 1234", page.Rows[0].AccountTitleSnapshot)
}

func TestLiveDeliveryStatisticsFiltersSortsAndPagesFinalRows(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store := NewProductionStore(db)

	_, err = db.ExecContext(ctx, `INSERT INTO live_delivery_history
		(id,job_id,source_message,trigger_snapshot,triggered_at,delivery_type,account_title_snapshot,final_status,finalized_at)
		VALUES
		('audit-before','job-before','before','before','2026-07-15T09:59:59Z','public_reply','Before','successful','2026-07-15T10:00:00Z'),
		('audit-first','job-first','zeta','money','2026-07-15T10:00:00Z','private_message','Zulu','successful','2026-07-15T10:00:01Z'),
		('audit-second','job-second','alpha','credit','2026-07-15T11:00:00Z','public_reply','Alpha','not_delivered','2026-07-15T11:00:01Z'),
		('audit-at-to','job-at-to','at to','loan','2026-07-15T12:00:00Z','public_reply','To','successful','2026-07-15T12:00:01Z'),
		('audit-draft','job-draft','draft','draft','2026-07-15T11:30:00Z',NULL,'',NULL,NULL)`)
	require.NoError(t, err)

	from := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	to := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	page, err := store.LiveDeliveryStatistics(ctx, domain.LiveDeliveryQuery{From: from, To: to, Limit: 50})
	require.NoError(t, err)
	require.Equal(t, 2, page.Total)
	require.Equal(t, []domain.ID{"audit-second", "audit-first"}, liveDeliveryRowIDs(page.Rows))

	for _, test := range []struct {
		name      string
		sortBy    domain.LiveDeliverySortColumn
		direction domain.LiveDeliverySortDirection
		want      []domain.ID
	}{
		{name: "message ascending", sortBy: domain.LiveDeliverySortSourceMessage, direction: domain.LiveDeliverySortAscending, want: []domain.ID{"audit-second", "audit-first"}},
		{name: "trigger descending", sortBy: domain.LiveDeliverySortTriggerSnapshot, direction: domain.LiveDeliverySortDescending, want: []domain.ID{"audit-first", "audit-second"}},
		{name: "delivery type ascending", sortBy: domain.LiveDeliverySortDeliveryType, direction: domain.LiveDeliverySortAscending, want: []domain.ID{"audit-first", "audit-second"}},
		{name: "account ascending", sortBy: domain.LiveDeliverySortAccountTitle, direction: domain.LiveDeliverySortAscending, want: []domain.ID{"audit-second", "audit-first"}},
		{name: "status ascending", sortBy: domain.LiveDeliverySortFinalStatus, direction: domain.LiveDeliverySortAscending, want: []domain.ID{"audit-second", "audit-first"}},
		{name: "finalized ascending", sortBy: domain.LiveDeliverySortFinalizedAt, direction: domain.LiveDeliverySortAscending, want: []domain.ID{"audit-first", "audit-second"}},
		{name: "triggered ascending", sortBy: domain.LiveDeliverySortTriggeredAt, direction: domain.LiveDeliverySortAscending, want: []domain.ID{"audit-first", "audit-second"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			page, err := store.LiveDeliveryStatistics(ctx, domain.LiveDeliveryQuery{
				From: from, To: to, Limit: 50, SortBy: test.sortBy, SortDirection: test.direction,
			})
			require.NoError(t, err)
			require.Equal(t, test.want, liveDeliveryRowIDs(page.Rows))
		})
	}
}

func TestLiveDeliveryStatisticsRejectsSortValuesOutsideAllowlists(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store := NewProductionStore(db)

	tests := []struct {
		name  string
		query domain.LiveDeliveryQuery
		want  string
	}{
		{
			name:  "column",
			query: domain.LiveDeliveryQuery{Limit: 50, SortBy: domain.LiveDeliverySortColumn("triggered_at; DROP TABLE live_delivery_history")},
			want:  "live delivery statistics sort column is unsupported",
		},
		{
			name:  "direction",
			query: domain.LiveDeliveryQuery{Limit: 50, SortDirection: domain.LiveDeliverySortDirection("DESC; DROP TABLE live_delivery_history")},
			want:  "live delivery statistics sort direction is unsupported",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := store.LiveDeliveryStatistics(ctx, test.query)
			require.EqualError(t, err, test.want)
		})
	}
}

func TestLiveDeliveryStatisticsAcceptsOnlySupportedPageSizes(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store := NewProductionStore(db)

	for i := 0; i < 1001; i++ {
		_, err := db.ExecContext(ctx, `INSERT INTO live_delivery_history
			(id,job_id,source_message,trigger_snapshot,triggered_at,final_status)
			VALUES (?,?,?,?,?,?)`, fmt.Sprintf("audit-%04d", i), fmt.Sprintf("job-%04d", i), "message", "money",
			fmt.Sprintf("2026-07-15T10:%02d:%02dZ", (i/60)%60, i%60), "successful")
		require.NoError(t, err)
	}

	for _, size := range []int{50, 100, 200, 500, 1000} {
		t.Run(fmt.Sprintf("page size %d", size), func(t *testing.T) {
			page, err := store.LiveDeliveryStatistics(ctx, domain.LiveDeliveryQuery{Limit: size, Offset: 1})
			require.NoError(t, err)
			require.Equal(t, 1001, page.Total)
			require.Len(t, page.Rows, size)
		})
	}
	_, err = store.LiveDeliveryStatistics(ctx, domain.LiveDeliveryQuery{Limit: 51})
	require.EqualError(t, err, "live delivery statistics page size is unsupported")
}

func TestLiveDeliveryStatisticsReportsHistoryPayloadBytes(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := Open(ctx, path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store := NewProductionStore(db)

	_, err = db.ExecContext(ctx, `INSERT INTO live_delivery_history
		(id,job_id,source_message,trigger_snapshot,triggered_at,final_status)
		VALUES ('audit-1','job-1','message','money','2026-07-15T10:00:00Z','successful')`)
	require.NoError(t, err)
	page, err := store.LiveDeliveryStatistics(ctx, domain.LiveDeliveryQuery{Limit: 50})
	require.NoError(t, err)

	want := int64(len("audit-1") + len("job-1") + len("message") + len("money") + len("2026-07-15T10:00:00Z") + len("successful"))
	require.Equal(t, want, page.DatabaseBytes)
}

func liveDeliveryRowIDs(rows []domain.LiveDeliveryRow) []domain.ID {
	ids := make([]domain.ID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids
}
