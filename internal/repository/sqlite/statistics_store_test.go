package sqlite

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"telegram-companion/internal/domain"
)

func TestProductionStoreReplyStatisticsUsesSuccessfulEventsInRequestedRange(t *testing.T) {
	ctx := context.Background()
	db := openReplyStatisticsTestDB(t, ctx)
	defer db.Close()

	store := NewProductionStore(db)
	from := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(48 * time.Hour)
	require.NoError(t, store.EnsureAccounts(ctx, []domain.Account{
		{ID: "account-a", DisplayName: "Alpha", Role: domain.AccountRoleSpammer, Status: domain.AccountActive, SessionPath: "/a", CreatedAt: from, UpdatedAt: from},
		{ID: "account-b", DisplayName: "Beta", Role: domain.AccountRoleSpammer, Status: domain.AccountActive, SessionPath: "/b", CreatedAt: from, UpdatedAt: from},
		{ID: "account-c", DisplayName: "Closed only", Role: domain.AccountRoleSpammer, Status: domain.AccountActive, SessionPath: "/c", CreatedAt: from, UpdatedAt: from},
	}))
	require.NoError(t, store.Catalogs().Save(ctx, domain.SourceCatalogOutbound, domain.Channel{
		ID: "channel-a", TelegramID: "1001", Title: "First channel", Link: "https://t.me/first", Active: true, CreatedAt: from, UpdatedAt: from,
	}))
	require.NoError(t, store.Catalogs().Save(ctx, domain.SourceCatalogOutbound, domain.Channel{
		ID: "channel-b", TelegramID: "1002", Title: "Second channel", Link: "https://t.me/second", Active: true, CreatedAt: from, UpdatedAt: from,
	}))

	insertTypedReplyStatisticEvent(t, db, "event-public", "account-a", "channel-a", domain.JobPublicReply, true, "", from.Add(10*time.Hour))
	insertTypedReplyStatisticEvent(t, db, "event-private", "account-a", "channel-a", domain.JobPrivateMessage, true, "", from.Add(10*time.Hour+15*time.Minute))
	insertTypedReplyStatisticEvent(t, db, "event-closed", "account-a", "channel-b", domain.JobPrivateMessage, false, "private_message_closed", from.Add(20*time.Hour))
	insertTypedReplyStatisticEvent(t, db, "event-fallback", "account-b", "channel-b", domain.JobPublicReply, true, "", from.Add(34*time.Hour))
	insertTypedReplyStatisticEvent(t, db, "event-closed-only", "account-c", "channel-b", domain.JobPrivateMessage, false, "private_message_closed", from.Add(36*time.Hour))
	insertTypedReplyStatisticEvent(t, db, "event-failed", "account-a", "channel-b", domain.JobPrivateMessage, false, "transport_error", from.Add(37*time.Hour))
	insertReplyStatisticEvent(t, db, "event-outside", "account-b", "channel-b", true, to)

	statistics, err := store.ReplyStatistics(ctx, from, to)

	require.NoError(t, err)
	require.Equal(t, int64(3), statistics.Totals.Replies)
	require.Equal(t, int64(2), statistics.Totals.PublicReplies)
	require.Equal(t, int64(1), statistics.Totals.PrivateMessages)
	require.Equal(t, int64(2), statistics.Totals.PrivateMessagesClosed)
	require.Equal(t, 2, statistics.Totals.Channels)
	require.Equal(t, 2, statistics.Totals.Accounts)
	require.InDelta(t, 3.0/(48*60), statistics.Totals.AverageRepliesPerMinute, 0.000001)
	require.Equal(t, []domain.ReplyStatisticsBucket{
		{Date: "2026-07-01", Replies: 2},
		{Date: "2026-07-02", Replies: 1},
	}, statistics.TimeSeries)
	require.Equal(t, []domain.ReplyStatisticsAccountRow{
		{ID: "account-a", Title: "Alpha", Replies: 2, PublicReplies: 1, PrivateMessages: 1, PrivateMessagesClosed: 1, LastActivityAt: from.Add(20 * time.Hour)},
		{ID: "account-b", Title: "Beta", Replies: 1, PublicReplies: 1, LastActivityAt: from.Add(34 * time.Hour)},
		{ID: "account-c", Title: "Closed only", PrivateMessagesClosed: 1, LastActivityAt: from.Add(36 * time.Hour)},
	}, statistics.Accounts)
	require.Equal(t, []domain.ReplyStatisticsChannelRow{
		{ID: "channel-a", Title: "First channel", Replies: 2, LastActivityAt: from.Add(10*time.Hour + 15*time.Minute)},
		{ID: "channel-b", Title: "Second channel", Replies: 1, LastActivityAt: from.Add(34 * time.Hour)},
	}, statistics.Channels)
}

func TestProductionStoreReplyStatisticsReturnsEmptyValuesWhenNoSuccessfulEventsMatch(t *testing.T) {
	ctx := context.Background()
	db := openReplyStatisticsTestDB(t, ctx)
	defer db.Close()

	from := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	statistics, err := NewProductionStore(db).ReplyStatistics(ctx, from, from.Add(24*time.Hour))

	require.NoError(t, err)
	require.Equal(t, domain.ReplyStatisticsTotals{}, statistics.Totals)
	require.Empty(t, statistics.TimeSeries)
	require.Empty(t, statistics.Accounts)
	require.Empty(t, statistics.Channels)
}

func TestProductionStoreReplyStatisticsUsesOnlyExistingAccountAndChannelRows(t *testing.T) {
	ctx := context.Background()
	db := openReplyStatisticsTestDB(t, ctx)
	defer db.Close()

	store := NewProductionStore(db)
	from := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, store.EnsureAccounts(ctx, []domain.Account{{
		ID: "account-a", DisplayName: "Alpha", Role: domain.AccountRoleSpammer, Status: domain.AccountActive, SessionPath: "/a", CreatedAt: from, UpdatedAt: from,
	}}))
	require.NoError(t, store.Catalogs().Save(ctx, domain.SourceCatalogOutbound, domain.Channel{
		ID: "channel-a", TelegramID: "1001", Title: "First channel", Link: "https://t.me/first", Active: true, CreatedAt: from, UpdatedAt: from,
	}))
	insertReplyStatisticEvent(t, db, "event-1", "account-a", "channel-a", true, from.Add(time.Hour))
	insertOrphanReplyStatisticEvent(t, db, "event-orphan", "account-deleted", "channel-deleted", from.Add(2*time.Hour))

	statistics, err := store.ReplyStatistics(ctx, from, from.Add(24*time.Hour))

	require.NoError(t, err)
	require.Equal(t, int64(2), statistics.Totals.Replies)
	require.Equal(t, []domain.ReplyStatisticsBucket{{Date: "2026-07-01", Replies: 2}}, statistics.TimeSeries)
	require.Equal(t, []domain.ReplyStatisticsAccountRow{{
		ID: "account-a", Title: "Alpha", Replies: 1, PublicReplies: 1, LastActivityAt: from.Add(time.Hour),
	}}, statistics.Accounts)
	require.Equal(t, []domain.ReplyStatisticsChannelRow{{
		ID: "channel-a", Title: "First channel", Replies: 1, LastActivityAt: from.Add(time.Hour),
	}}, statistics.Channels)
}

func insertReplyStatisticEvent(t *testing.T, db *sql.DB, id, accountID, channelID string, success bool, createdAt time.Time) {
	insertTypedReplyStatisticEvent(t, db, id, accountID, channelID, domain.JobPublicReply, success, "", createdAt)
}

func insertTypedReplyStatisticEvent(t *testing.T, db *sql.DB, id, accountID, channelID string, eventType domain.JobType, success bool, errorCode string, createdAt time.Time) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `INSERT INTO outgoing_message_jobs
		(id, type, account_id, channel_id, next_attempt_at, created_at)
		VALUES (?, 'public_reply', ?, ?, ?, ?)`, "job-"+id, accountID, channelID, formatTime(createdAt), formatTime(createdAt))
	require.NoError(t, err)
	_, err = db.ExecContext(context.Background(), `INSERT INTO outgoing_message_events
		(id, job_id, account_id, channel_id, type, success, error_code, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id, "job-"+id, accountID, channelID, eventType, boolInt(success), errorCode, formatTime(createdAt))
	require.NoError(t, err)
}

func openReplyStatisticsTestDB(t *testing.T, ctx context.Context) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	require.NoError(t, Migrate(ctx, db))
	require.NoError(t, Configure(ctx, db))
	return db
}

func insertOrphanReplyStatisticEvent(t *testing.T, db *sql.DB, id, accountID, channelID string, createdAt time.Time) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `INSERT INTO outgoing_message_jobs
		(id, type, account_id, channel_id, next_attempt_at, created_at)
		VALUES (?, 'public_reply', NULL, ?, ?, ?)`, "job-"+id, channelID, formatTime(createdAt), formatTime(createdAt))
	require.NoError(t, err)
	_, err = db.ExecContext(context.Background(), `INSERT INTO outgoing_message_events
		(id, job_id, account_id, channel_id, type, success, error_code, created_at)
		VALUES (?, ?, ?, ?, 'public_reply', 1, '', ?)`, id, "job-"+id, accountID, channelID, formatTime(createdAt))
	require.NoError(t, err)
}
