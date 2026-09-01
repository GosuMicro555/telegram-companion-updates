package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"

	"telegram-companion/internal/repository/sqlite/migrations"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func TestMigrationV12ResetsAnalyticsAndStatsButPreservesTelegramConfiguration(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "v11.db"))
	require.NoError(t, err)
	defer db.Close()
	legacy := fstest.MapFS{}
	for _, name := range []string{
		"000001_init.sql", "000002_scout_message_tombstones.sql", "000003_delivery_queue.sql",
		"000004_upgrade_cleanup.sql", "000005_acceptance_delivery_cleanup.sql", "000006_delivery_leases.sql",
		"000007_import_provenance_cleanup.sql", "000008_account_stopped_status.sql", "000009_backup_error_sanitization.sql",
		"000010_account_status_source.sql", "000011_legacy_pause_review.sql",
	} {
		raw, readErr := fs.ReadFile(migrations.FS, name)
		require.NoError(t, readErr)
		legacy[name] = &fstest.MapFile{Data: raw}
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, legacy)
	require.NoError(t, err)
	_, err = provider.Up(ctx)
	require.NoError(t, err)

	const now = "2026-07-14T12:00:00.000000000Z"
	_, err = db.ExecContext(ctx, `INSERT INTO accounts
		(id, phone_masked, display_name, role, status, session_path, public_replies_sent, private_messages_sent, created_at, updated_at)
		VALUES ('account-1', '+7***', 'Scout', 'scout_analyst', 'paused', '/sessions/account-1', 7, 5, ?, ?)`, now, now)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO outbound_channels
		(id, telegram_chat_id, title, link, topic, sent_count, created_at, updated_at)
		VALUES ('out-1', '100', 'Outbound', 'https://t.me/out', 'kept', 9, ?, ?)`, now, now)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO scout_chats
		(id, telegram_chat_id, title, link, topic, message_count, created_at, updated_at)
		VALUES ('scout-1', '200', 'Scout chat', 'https://t.me/scout', 'kept', 4, ?, ?)`, now, now)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO account_channel_memberships
		(account_id, catalog, channel_id, is_member) VALUES ('account-1', 'scout', 'scout-1', 1)`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO scout_messages
		(telegram_chat_id, telegram_message_id, encrypted_text, nonce, message_at, received_at)
		VALUES ('200', 1, X'01', X'02', ?, ?)`, now, now)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO analysis_profiles
		(id, name, created_at, updated_at) VALUES ('profile', 'old', ?, ?)`, now, now)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO analysis_runs
		(id, source_scope, profile_id, started_at) VALUES ('run', 'all', 'profile', ?)`, now)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO keyword_candidates
		(id, run_id, normalized_value, display_value, kind, source, created_at)
		VALUES ('candidate', 'run', 'old', 'old phrase', 'phrase', 'telegram', ?)`, now)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO moderation_decisions
		(normalized_value, kind, state, decided_at) VALUES ('old', 'phrase', 'accepted', ?)`, now)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO imports
		(id, file_name, file_path, sha256, rights_confirmed, imported_at) VALUES ('import', 'old', '/old', 'hash', 1, ?)`, now)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO backup_history
		(id, archive_path, kind, created_at) VALUES ('backup', '/backup', 'daily', ?)`, now)
	require.NoError(t, err)
	settings := `{"keywords":["old trigger"],"sharedReply":"keep reply","directMessageKeywords":["old dm"]}`
	_, err = db.ExecContext(ctx, `INSERT INTO app_settings(key,value_json,updated_at) VALUES
		('keyword_settings', ?, ?), ('unrelated', '{"kept":true}', ?),
		('current_successful_run_id', '"run"', ?), ('analysis_result:run', '{}', ?)`, settings, now, now, now, now)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO outgoing_message_jobs
		(id,type,channel_id,status,next_attempt_at,created_at) VALUES ('job','public_reply','out-1','done',?,?)`, now, now)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO outgoing_message_events
		(id,job_id,account_id,channel_id,type,success,created_at) VALUES ('event','job','account-1','out-1','public_reply',1,?)`, now)
	require.NoError(t, err)

	require.NoError(t, Migrate(ctx, db))

	var role, sessionPath string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT role,session_path FROM accounts WHERE id='account-1'`).Scan(&role, &sessionPath))
	require.Equal(t, "scout_analyst", role)
	require.Equal(t, "/sessions/account-1", sessionPath)
	require.Equal(t, 1, tableCount(t, db, "outbound_channels"))
	require.Equal(t, 1, tableCount(t, db, "scout_chats"))
	require.Equal(t, 1, tableCount(t, db, "account_channel_memberships"))
	for _, table := range []string{"scout_messages", "analysis_profiles", "analysis_runs", "keyword_candidates", "moderation_decisions", "imports", "backup_history", "outgoing_message_jobs", "outgoing_message_events"} {
		require.Zero(t, tableCount(t, db, table), table)
	}
	var publicReplies, privateMessages, sentCount, messageCount int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT public_replies_sent,private_messages_sent FROM accounts WHERE id='account-1'`).Scan(&publicReplies, &privateMessages))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT sent_count FROM outbound_channels WHERE id='out-1'`).Scan(&sentCount))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT message_count FROM scout_chats WHERE id='scout-1'`).Scan(&messageCount))
	require.Zero(t, publicReplies)
	require.Zero(t, privateMessages)
	require.Zero(t, sentCount)
	require.Zero(t, messageCount)
	var raw string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT value_json FROM app_settings WHERE key='keyword_settings'`).Scan(&raw))
	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &decoded))
	require.Equal(t, "keep reply", decoded["sharedReply"])
	require.Empty(t, decoded["keywords"])
	require.Empty(t, decoded["directMessageKeywords"])
	require.Zero(t, tableCount(t, db, "canonical_keywords"))
	require.Zero(t, tableCount(t, db, "canonical_keyword_forms"))
}

func TestMigrationV14AddsCanonicalFrequencyDeltaDefault(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "v13.db"))
	require.NoError(t, err)
	defer db.Close()
	legacy := fstest.MapFS{}
	for _, name := range []string{
		"000001_init.sql", "000002_scout_message_tombstones.sql", "000003_delivery_queue.sql",
		"000004_upgrade_cleanup.sql", "000005_acceptance_delivery_cleanup.sql", "000006_delivery_leases.sql",
		"000007_import_provenance_cleanup.sql", "000008_account_stopped_status.sql", "000009_backup_error_sanitization.sql",
		"000010_account_status_source.sql", "000011_legacy_pause_review.sql", "000012_canonical_keyword_analytics.sql",
		"000013_keyword_analytics_scheduler.sql",
	} {
		raw, readErr := fs.ReadFile(migrations.FS, name)
		require.NoError(t, readErr)
		legacy[name] = &fstest.MapFile{Data: raw}
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, legacy)
	require.NoError(t, err)
	_, err = provider.Up(ctx)
	require.NoError(t, err)

	const now = "2026-07-14T12:00:00.000000000Z"
	_, err = db.ExecContext(ctx, `INSERT INTO canonical_keywords
		(id, canonical_value, language, class, decision_source, trigger_active, frequency, message_count, created_at, updated_at)
		VALUES ('money', 'money', 'en', 'neutral', 'manual', 0, 7, 3, ?, ?)`, now, now)
	require.NoError(t, err)

	require.NoError(t, Migrate(ctx, db))
	var delta int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT frequency_delta FROM canonical_keywords WHERE id='money'`).Scan(&delta))
	require.Zero(t, delta)
}

func tableCount(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM `+table).Scan(&count))
	return count
}
