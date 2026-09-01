package sqlite

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	core "telegram-companion/internal/analytics"
	"telegram-companion/internal/domain"
	"telegram-companion/internal/repository/sqlite/migrations"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func TestAnalyticsSchedulerMigrationV13PreservesExistingCanonicalAndSettings(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "v12.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	legacy := fstest.MapFS{}
	for _, name := range []string{
		"000001_init.sql", "000002_scout_message_tombstones.sql", "000003_delivery_queue.sql",
		"000004_upgrade_cleanup.sql", "000005_acceptance_delivery_cleanup.sql", "000006_delivery_leases.sql",
		"000007_import_provenance_cleanup.sql", "000008_account_stopped_status.sql", "000009_backup_error_sanitization.sql",
		"000010_account_status_source.sql", "000011_legacy_pause_review.sql", "000012_canonical_keyword_analytics.sql",
	} {
		raw, readErr := fs.ReadFile(migrations.FS, name)
		require.NoError(t, readErr)
		legacy[name] = &fstest.MapFile{Data: raw}
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, legacy)
	require.NoError(t, err)
	_, err = provider.Up(ctx)
	require.NoError(t, err)

	const now = "2026-07-15T10:00:00.000000000Z"
	_, err = db.ExecContext(ctx, `INSERT INTO canonical_keywords
		(id, canonical_value, language, class, decision_source, trigger_active, frequency, message_count, created_at, updated_at)
		VALUES ('money', 'money', 'en', 'positive', 'manual', 1, 3, 2, ?, ?)`, now, now)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO app_settings(key, value_json, updated_at)
		VALUES ('keyword_settings', '{"sharedReply":"keep"}', ?)`, now)
	require.NoError(t, err)

	require.NoError(t, Migrate(ctx, db))
	var canonical, settings string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT canonical_value FROM canonical_keywords WHERE id='money'`).Scan(&canonical))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT value_json FROM app_settings WHERE key='keyword_settings'`).Scan(&settings))
	require.Equal(t, "money", canonical)
	require.JSONEq(t, `{"sharedReply":"keep"}`, settings)
	_, err = db.ExecContext(ctx, `UPDATE canonical_keywords SET class='service', trigger_active=0 WHERE id='money'`)
	require.NoError(t, err)
	for _, table := range []string{
		"analytics_scheduler_settings", "analytics_scout_cursors", "analytics_scheduler_runs",
		"analytics_service_words", "analytics_table_preferences",
	} {
		var found int
		require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&found))
		require.Equal(t, 1, found, table)
	}
	serviceStore := NewAnalyticsSchedulerStore(db)
	for language, expected := range map[domain.AnalyticsLanguage][]string{
		domain.AnalyticsLanguageRU: {"\u0434\u043b\u044f", "\u0447\u0442\u043e", "\u044d\u0442\u043e", "\u043e\u043d\u0438", "\u0438\u043b\u0438"},
		domain.AnalyticsLanguageEN: {"the", "and", "with", "they"},
	} {
		words, serviceErr := serviceStore.ServiceWords(ctx, language)
		require.NoError(t, serviceErr)
		require.Subset(t, words, expected)
	}
}

func TestAnalyticsSchedulerStorePersistsSettingsCursorHistoryServiceWordsAndTablePreferences(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "scheduler.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store := NewAnalyticsSchedulerStore(db)

	settings, err := store.AnalyticsSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, domain.AnalyticsSettings{Enabled: true, IntervalMinutes: 10}, settings)
	for _, interval := range []int{1, 5, 10, 30, 60, 120} {
		require.NoError(t, store.SaveAnalyticsSettings(ctx, domain.AnalyticsSettings{Enabled: true, IntervalMinutes: interval}), interval)
	}
	require.NoError(t, store.SaveAnalyticsSettings(ctx, domain.AnalyticsSettings{Enabled: false, IntervalMinutes: 30}))
	settings, err = store.AnalyticsSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, domain.AnalyticsSettings{Enabled: false, IntervalMinutes: 30}, settings)
	require.Error(t, store.SaveAnalyticsSettings(ctx, domain.AnalyticsSettings{IntervalMinutes: 15}))

	cursor := domain.AnalyticsScoutCursor{AccountID: "account-1", ChatID: "chat-7", MessageID: 101}
	require.NoError(t, store.SaveScoutCursor(ctx, cursor))
	loadedCursor, err := store.ScoutCursor(ctx, "account-1", "chat-7")
	require.NoError(t, err)
	require.Equal(t, cursor.AccountID, loadedCursor.AccountID)
	require.Equal(t, cursor.ChatID, loadedCursor.ChatID)
	require.Equal(t, cursor.MessageID, loadedCursor.MessageID)
	require.False(t, loadedCursor.UpdatedAt.IsZero())
	require.NoError(t, store.SaveScoutCursor(ctx, domain.AnalyticsScoutCursor{AccountID: "account-1", ChatID: "chat-7", MessageID: 105}))
	loadedCursor, err = store.ScoutCursor(ctx, "account-1", "chat-7")
	require.NoError(t, err)
	require.Equal(t, int64(105), loadedCursor.MessageID)

	finished := time.Date(2026, 7, 15, 10, 5, 0, 0, time.UTC)
	require.NoError(t, store.AppendAnalyticsRun(ctx, domain.AnalyticsSchedulerRun{
		ID: "run-1", StartedAt: finished.Add(-time.Minute), FinishedAt: &finished, Metrics: domain.AnalyticsRunMetrics{
			NewMessages: 17, ExtractedWords: 42, NewCanonicals: 3, ProcessedGroups: 4,
			Duration: time.Minute, Errors: []string{"one group retried"},
		},
	}))
	history, err := store.AnalyticsRunHistory(ctx, 10)
	require.NoError(t, err)
	require.Equal(t, []domain.AnalyticsSchedulerRun{{
		ID: "run-1", StartedAt: finished.Add(-time.Minute), FinishedAt: &finished, Metrics: domain.AnalyticsRunMetrics{
			NewMessages: 17, ExtractedWords: 42, NewCanonicals: 3, ProcessedGroups: 4,
			Duration: time.Minute, Errors: []string{"one group retried"},
		},
	}}, history)
	require.NoError(t, NewAnalyticsStore(db).SyncCanonical(ctx, []core.CanonicalObservation{{
		Canonical: "money", Language: core.LanguageEN, TotalFrequency: 1, MessageCount: 1,
		Forms: []core.FormObservation{{Form: "money", Frequency: 1}},
	}}))
	const storedAt = "2026-07-15T10:00:00.000000000Z"
	_, err = db.ExecContext(ctx, `INSERT INTO scout_chats
		(id, telegram_chat_id, title, link, topic, created_at, updated_at)
		VALUES ('chat-7', '700', 'Group', 'https://t.me/group', 'buyers', ?, ?)`, storedAt, storedAt)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO scout_messages
		(telegram_chat_id, telegram_message_id, encrypted_text, nonce, message_at, received_at)
		VALUES ('700', 1, X'01', X'02', ?, ?)`, storedAt, storedAt)
	require.NoError(t, err)
	metrics, err := store.AnalyticsMetrics(ctx)
	require.NoError(t, err)
	require.Equal(t, history[0].Metrics, metrics.LastRun)
	require.Equal(t, int64(1), metrics.CanonicalCount)
	require.Equal(t, int64(1), metrics.FormCount)
	require.Equal(t, int64(1), metrics.MessageCount)
	require.Equal(t, int64(1), metrics.GroupCount)
	require.Equal(t, int64(10), metrics.LogicalKeywordBytes)

	require.NoError(t, store.UpsertServiceWord(ctx, domain.AnalyticsServiceWord{Language: domain.AnalyticsLanguageRU, Value: "и"}))
	require.NoError(t, store.UpsertServiceWord(ctx, domain.AnalyticsServiceWord{Language: domain.AnalyticsLanguageEN, Value: "the"}))
	ruWords, err := store.ServiceWords(ctx, domain.AnalyticsLanguageRU)
	require.NoError(t, err)
	require.Contains(t, ruWords, "и")
	require.NoError(t, store.DeleteServiceWord(ctx, domain.AnalyticsLanguageRU, "и"))
	ruWords, err = store.ServiceWords(ctx, domain.AnalyticsLanguageRU)
	require.NoError(t, err)
	require.NotContains(t, ruWords, "и")

	pref := domain.AnalyticsTablePreference{
		Tab: "positive", SortBy: "frequency", SortDirection: domain.AnalyticsSortDescending, PageSize: 50,
		Columns: []domain.AnalyticsTableColumnPreference{
			{Key: "canonical", Width: 260, Visible: true},
			{Key: "frequency", Width: 90, Visible: true},
			{Key: "last_seen", Width: 120, Visible: false},
		},
	}
	require.NoError(t, store.SaveTablePreference(ctx, pref))
	loadedPref, err := store.TablePreference(ctx, "positive")
	require.NoError(t, err)
	require.Equal(t, pref, loadedPref)
}

func TestAnalyticsSchedulerStoreSaveScoutCursorIsMonotonic(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "scheduler.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store := NewAnalyticsSchedulerStore(db)

	require.NoError(t, store.SaveScoutCursor(ctx, domain.AnalyticsScoutCursor{
		AccountID: "account-1",
		ChatID:    "chat-7",
		MessageID: 30,
	}))
	require.NoError(t, store.SaveScoutCursor(ctx, domain.AnalyticsScoutCursor{
		AccountID: "account-1",
		ChatID:    "chat-7",
		MessageID: 25,
	}))

	cursor, err := store.ScoutCursor(ctx, "account-1", "chat-7")
	require.NoError(t, err)
	require.Equal(t, int64(30), cursor.MessageID)
}
