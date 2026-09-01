package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"telegram-companion/internal/domain"
	"telegram-companion/internal/repository/sqlite"
	"telegram-companion/internal/usecase"

	"github.com/stretchr/testify/require"
)

type passthroughMessageDecrypter struct{}

func (passthroughMessageDecrypter) Decrypt(_ context.Context, ciphertext, _ []byte, _ string, _ int64, _ time.Time) ([]byte, error) {
	return append([]byte(nil), ciphertext...), nil
}

func TestSettingsStoreMigratesMissingPrivateReplyWithoutOverwritingExplicitEmptyValue(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "app.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	store := sqlite.NewSettingsStore(db)
	load := func(raw string) domain.KeywordSettings {
		t.Helper()
		_, err := db.ExecContext(ctx, `INSERT INTO app_settings (key, value_json, revision, updated_at) VALUES (?, ?, 1, ?)
			ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json`, "keyword_settings", raw, time.Now().UTC().Format(time.RFC3339Nano))
		require.NoError(t, err)
		settings, err := store.Load(ctx)
		require.NoError(t, err)
		return settings
	}

	legacy := load(`{"sharedReply":"comment reply"}`)
	require.Equal(t, "comment reply", legacy.PrivateReply)
	require.False(t, legacy.PrivateReplyPresent)

	explicitEmpty := load(`{"sharedReply":"comment reply","privateReply":""}`)
	require.Empty(t, explicitEmpty.PrivateReply)
	require.True(t, explicitEmpty.PrivateReplyPresent)
}

func TestCatalogStoreSeparatesOutboundAndScoutCatalogs(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "app.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	store := sqlite.NewCatalogStore(db)
	now := time.Date(2026, time.July, 11, 12, 0, 0, 0, time.UTC)

	require.NoError(t, store.Save(ctx, domain.SourceCatalogOutbound, domain.Channel{
		ID: "outbound-1", TelegramID: "1001", Title: "Outbound", Link: "https://t.me/outbound",
		Topic: "Без тематики", Status: domain.ChannelReady, Active: true, CreatedAt: now, UpdatedAt: now,
	}))
	require.NoError(t, store.Save(ctx, domain.SourceCatalogScout, domain.Channel{
		ID: "scout-1", TelegramID: "2001", Title: "Scout", Link: "https://t.me/scout",
		Topic: "Research", Status: domain.ChannelPaused, Active: false, CreatedAt: now, UpdatedAt: now,
	}))

	outbound, err := store.List(ctx, domain.SourceCatalogOutbound)
	require.NoError(t, err)
	require.Len(t, outbound, 1)
	require.Equal(t, "outbound-1", string(outbound[0].ID))
	require.Equal(t, "Без тематики", outbound[0].Topic)

	scout, err := store.List(ctx, domain.SourceCatalogScout)
	require.NoError(t, err)
	require.Len(t, scout, 1)
	require.Equal(t, "scout-1", string(scout[0].ID))
	require.Equal(t, "Research", scout[0].Topic)
}

func TestRequestCatalogRemovalMarksRowsAndCancelsOnlyPendingJobs(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:channel-removal?mode=memory&cache=shared")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, sqlite.Migrate(ctx, db))

	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	store := sqlite.NewProductionStore(db)
	channel := domain.Channel{ID: "remove", TelegramID: "100", Link: "https://t.me/remove", Topic: "Topic", Active: true, Status: domain.ChannelReady, CreatedAt: now, UpdatedAt: now}
	require.NoError(t, store.SaveCatalog(ctx, domain.SourceCatalogOutbound, channel))
	jobs := sqlite.NewJobRepository(db, func() time.Time { return now })
	for _, job := range []domain.OutgoingMessageJob{
		{ID: "queued", Type: domain.JobPublicReply, ChannelID: channel.ID, Status: "queued", NextAttemptAt: now, CreatedAt: now},
		{ID: "delayed", Type: domain.JobPublicReply, ChannelID: channel.ID, Status: "delayed", NextAttemptAt: now.Add(time.Hour), CreatedAt: now},
		{ID: "done", Type: domain.JobPublicReply, ChannelID: channel.ID, Status: "done", NextAttemptAt: now, CreatedAt: now},
	} {
		require.NoError(t, jobs.Enqueue(ctx, job))
	}
	require.NoError(t, store.RequestCatalogRemoval(ctx, domain.SourceCatalogOutbound, []domain.ID{channel.ID}))
	require.NoError(t, store.RequestCatalogRemoval(ctx, domain.SourceCatalogOutbound, []domain.ID{channel.ID}))

	rows, err := store.ListCatalog(ctx, domain.SourceCatalogOutbound)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.False(t, rows[0].Active)
	require.Equal(t, domain.ChannelPaused, rows[0].Status)
	require.NotNil(t, rows[0].RemovalRequestedAt)

	jobStates := map[string]string{}
	for _, id := range []string{"queued", "delayed", "done"} {
		var status, lastError string
		require.NoError(t, db.QueryRowContext(ctx, `SELECT status,last_error FROM outgoing_message_jobs WHERE id=?`, id).Scan(&status, &lastError))
		jobStates[id] = status + ":" + lastError
	}
	require.Equal(t, "done:channel_removal_requested", jobStates["queued"])
	require.Equal(t, "done:channel_removal_requested", jobStates["delayed"])
	require.Equal(t, "done:", jobStates["done"])
}

func TestCompleteCatalogRemovalWaitsForEveryMembership(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:channel-removal-finalize?mode=memory&cache=shared")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, sqlite.Migrate(ctx, db))

	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	store := sqlite.NewProductionStore(db)
	require.NoError(t, store.EnsureAccounts(ctx, []domain.Account{{
		ID: "account", Role: domain.AccountRoleSpammer, Status: domain.AccountActive, SessionPath: "session", CreatedAt: now, UpdatedAt: now,
	}}))
	channel := domain.Channel{ID: "remove", TelegramID: "100", Link: "https://t.me/remove", Topic: "Topic", Active: true, Status: domain.ChannelReady, CreatedAt: now, UpdatedAt: now}
	require.NoError(t, store.SaveCatalog(ctx, domain.SourceCatalogOutbound, channel))
	require.NoError(t, store.SaveMembership(ctx, domain.SourceCatalogOutbound, domain.ChannelMembership{
		AccountID: "account", ChannelID: channel.ID, IsMember: true, Status: "member",
	}))
	require.NoError(t, store.RequestCatalogRemoval(ctx, domain.SourceCatalogOutbound, []domain.ID{channel.ID}))
	require.NoError(t, store.CompleteCatalogRemoval(ctx, domain.SourceCatalogOutbound, channel.ID))

	rows, err := store.ListCatalog(ctx, domain.SourceCatalogOutbound)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	memberships, err := store.ListMemberships(ctx, domain.SourceCatalogOutbound, channel.ID)
	require.NoError(t, err)
	require.Len(t, memberships, 1)

	require.NoError(t, store.DeleteMembership(ctx, domain.SourceCatalogOutbound, "account", channel.ID))
	require.NoError(t, store.CompleteCatalogRemoval(ctx, domain.SourceCatalogOutbound, channel.ID))
	rows, err = store.ListCatalog(ctx, domain.SourceCatalogOutbound)
	require.NoError(t, err)
	require.Empty(t, rows)
}

func TestCompleteScoutCatalogRemovalArchivesChannelAndPreservesAnalytics(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:scout-removal-archive?mode=memory&cache=shared")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, sqlite.Configure(ctx, db))
	require.NoError(t, sqlite.Migrate(ctx, db))

	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	store := sqlite.NewProductionStore(db)
	channel := domain.Channel{
		ID: "scout-original", TelegramID: "200", Title: "Scout", Link: "https://t.me/scout",
		Topic: "Research", Active: true, Status: domain.ChannelReady, MessageCount: 1, CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, store.SaveCatalog(ctx, domain.SourceCatalogScout, channel))
	_, err = db.ExecContext(ctx, `INSERT INTO scout_messages
		(telegram_chat_id,telegram_message_id,encrypted_text,nonce,message_at,received_at)
		VALUES ('200',7,?,X'01',?,?)`, []byte("preserved analytics"), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	require.NoError(t, err)

	require.NoError(t, store.RequestCatalogRemoval(ctx, domain.SourceCatalogScout, []domain.ID{channel.ID}))
	require.NoError(t, store.CompleteCatalogRemoval(ctx, domain.SourceCatalogScout, channel.ID))

	visible, err := store.ListCatalog(ctx, domain.SourceCatalogScout)
	require.NoError(t, err)
	require.Empty(t, visible)
	var archivedAt, removalRequestedAt sql.NullString
	var active int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT active,archived_at,removal_requested_at
		FROM scout_chats WHERE id=?`, channel.ID).Scan(&active, &archivedAt, &removalRequestedAt))
	require.Equal(t, 1, active)
	require.True(t, archivedAt.Valid)
	require.True(t, removalRequestedAt.Valid)

	source := sqlite.NewEncryptedAnalyticsRowSource(db, passthroughMessageDecrypter{})
	analyticsRows, err := source.Rows(ctx, "all", nil)
	require.NoError(t, err)
	require.Len(t, analyticsRows, 1)
	require.Equal(t, "preserved analytics", analyticsRows[0].Text)

	visible, err = usecase.NewCatalogService(store).AddLinks(
		ctx, domain.SourceCatalogScout, []string{channel.Link}, "Restored research",
	)
	require.NoError(t, err)
	require.Len(t, visible, 1)
	require.NotEqual(t, channel.ID, visible[0].ID)
	require.Equal(t, channel.TelegramID, visible[0].TelegramID)
	require.Equal(t, "Restored research", visible[0].Topic)
	require.False(t, visible[0].Active)
	require.EqualValues(t, 1, visible[0].MessageCount)
	require.Nil(t, visible[0].RemovalRequestedAt)

	var preservedMessages int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scout_messages
		WHERE telegram_chat_id=?`, channel.TelegramID).Scan(&preservedMessages))
	require.Equal(t, 1, preservedMessages)
}
