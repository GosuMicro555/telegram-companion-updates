package sqlite_test

import (
	"context"
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"telegram-companion/internal/domain"
	"telegram-companion/internal/repository/sqlite"
	sqlitemigrations "telegram-companion/internal/repository/sqlite/migrations"
	"telegram-companion/internal/usecase"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
)

func migrateThrough(ctx context.Context, db *sql.DB, maxVersion int) error {
	migrations, err := migrationFSThrough(maxVersion)
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, migrations)
	if err != nil {
		return err
	}
	_, err = provider.Up(ctx)
	return err
}

func migrationFSThrough(maxVersion int) (fstest.MapFS, error) {
	entries, err := fs.ReadDir(sqlitemigrations.FS, ".")
	if err != nil {
		return nil, err
	}
	migrations := fstest.MapFS{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, err := strconv.Atoi(strings.SplitN(entry.Name(), "_", 2)[0])
		if err != nil || version > maxVersion {
			continue
		}
		data, err := fs.ReadFile(sqlitemigrations.FS, entry.Name())
		if err != nil {
			return nil, err
		}
		migrations[entry.Name()] = &fstest.MapFile{Data: data}
	}
	return migrations, nil
}

func TestOpenMigratesCompleteSchema(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "app.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	for _, table := range []string{
		"accounts", "outbound_channels", "scout_chats",
		"account_channel_memberships", "scout_messages", "imports",
		"scout_message_tombstones",
		"analysis_profiles", "analysis_runs", "keyword_candidates",
		"moderation_decisions", "app_settings", "backup_history",
		"outgoing_message_jobs", "outgoing_message_events",
		"scheduled_dm_tasks", "scheduled_dm_recipients", "scheduled_dm_accounts",
		"scheduled_dm_runs", "scheduled_dm_deliveries",
	} {
		var count int
		err := db.QueryRowContext(ctx,
			`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table,
		).Scan(&count)
		require.NoError(t, err)
		require.Equal(t, 1, count, table)
	}

	require.NoError(t, sqlite.Migrate(ctx, db))

	rows, err := db.QueryContext(ctx, `PRAGMA table_info(scout_message_tombstones)`)
	require.NoError(t, err)
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		require.NoError(t, rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey))
		columns = append(columns, name)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []string{"telegram_chat_id", "telegram_message_id", "deleted_at"}, columns)
}

func TestChannelRemovalMigrationAddsNullableMarkers(t *testing.T) {
	contents, err := fs.ReadFile(sqlitemigrations.FS, "000026_channel_removal_requested.sql")
	require.NoError(t, err)
	require.Contains(t, string(contents), "ALTER TABLE outbound_channels ADD COLUMN removal_requested_at TEXT;")
	require.Contains(t, string(contents), "ALTER TABLE scout_chats ADD COLUMN removal_requested_at TEXT;")
}

func TestChannelRemovalMigrationDownPreservesScoutMessages(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "channel-removal-down.sqlite"))
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, sqlite.Configure(ctx, db))
	require.NoError(t, migrateThrough(ctx, db, 26))

	const storedAt = "2026-07-27T12:00:00.000000000Z"
	_, err = db.ExecContext(ctx, `INSERT INTO scout_chats
		(id,telegram_chat_id,title,link,topic,status,active,created_at,updated_at)
		VALUES ('scout','200','Scout','https://t.me/scout','Research','ready',1,?,?)`, storedAt, storedAt)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO scout_messages
		(telegram_chat_id,telegram_message_id,encrypted_text,nonce,message_at,received_at)
		VALUES ('200',7,X'01',X'02',?,?)`, storedAt, storedAt)
	require.NoError(t, err)

	migrations, err := migrationFSThrough(26)
	require.NoError(t, err)
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, migrations)
	require.NoError(t, err)
	_, err = provider.Down(ctx)
	require.NoError(t, err)

	var messages int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scout_messages WHERE telegram_chat_id='200'`).Scan(&messages))
	require.Equal(t, 1, messages)
	var removalColumn int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('scout_chats')
		WHERE name='removal_requested_at'`).Scan(&removalColumn))
	require.Zero(t, removalColumn)
}

func TestScoutArchiveMigrationDownPreservesScoutMessages(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "scout-archive-down.sqlite"))
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, sqlite.Configure(ctx, db))
	require.NoError(t, migrateThrough(ctx, db, 26))

	const storedAt = "2026-07-27T12:00:00.000000000Z"
	_, err = db.ExecContext(ctx, `INSERT INTO scout_chats
		(id,telegram_chat_id,title,link,topic,status,active,created_at,updated_at)
		VALUES ('scout','200','Scout','https://t.me/scout','Research','ready',1,?,?)`, storedAt, storedAt)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO scout_messages
		(telegram_chat_id,telegram_message_id,encrypted_text,nonce,message_at,received_at)
		VALUES ('200',7,X'01',X'02',?,?)`, storedAt, storedAt)
	require.NoError(t, err)

	migrations, err := migrationFSThrough(27)
	require.NoError(t, err)
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, migrations)
	require.NoError(t, err)
	_, err = provider.Up(ctx)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE scout_chats SET archived_at=? WHERE id='scout'`, storedAt)
	require.NoError(t, err)
	var messages int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scout_messages WHERE telegram_chat_id='200'`).Scan(&messages))
	require.Equal(t, 1, messages)

	_, err = provider.Down(ctx)
	require.NoError(t, err)

	messages = 0
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scout_messages WHERE telegram_chat_id='200'`).Scan(&messages))
	require.Equal(t, 1, messages)
	var archivedColumn int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('scout_chats')
		WHERE name='archived_at'`).Scan(&archivedColumn))
	require.Zero(t, archivedColumn)
}

func TestOpenForcesOwnerOnlyDatabaseDirectoryAndSidecarModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not support POSIX file permissions")
	}
	ctx := context.Background()
	root := t.TempDir()
	directory := filepath.Join(root, "permissive")
	require.NoError(t, os.Mkdir(directory, 0o777))
	require.NoError(t, os.Chmod(directory, 0o777))
	path := filepath.Join(directory, "app.sqlite")
	db, err := sqlite.Open(ctx, path)
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, db.PingContext(ctx))

	for target, mode := range map[string]os.FileMode{
		directory:     0o700,
		path:          0o600,
		path + "-wal": 0o600,
		path + "-shm": 0o600,
	} {
		info, err := os.Stat(target)
		require.NoError(t, err, target)
		require.Equal(t, mode, info.Mode().Perm(), target)
	}
}

func TestConfigureDoesNotRunUnconditionalFullVacuum(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "freelist.sqlite")
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer db.Close()
	_, err = db.ExecContext(ctx, `PRAGMA auto_vacuum=INCREMENTAL; VACUUM;
		CREATE TABLE payloads(value BLOB);`)
	require.NoError(t, err)
	for range 200 {
		_, err = db.ExecContext(ctx, `INSERT INTO payloads(value) VALUES (zeroblob(8192))`)
		require.NoError(t, err)
	}
	_, err = db.ExecContext(ctx, `DELETE FROM payloads`)
	require.NoError(t, err)
	var before int
	require.NoError(t, db.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&before))
	require.Greater(t, before, 0)

	require.NoError(t, sqlite.Configure(ctx, db))
	var after int
	require.NoError(t, db.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&after))
	require.Greater(t, after, 0, "normal open must not rewrite the full database")
}

func TestOpenConfiguresSQLite(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "private", "app.sqlite")
	db, err := sqlite.Open(ctx, path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	info, err := os.Stat(filepath.Dir(path))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	require.Equal(t, 4, db.Stats().MaxOpenConnections)

	for _, pragma := range []struct {
		name string
		want any
	}{
		{"journal_mode", "wal"},
		{"foreign_keys", int64(1)},
		{"busy_timeout", int64(5000)},
		{"secure_delete", int64(1)},
		{"auto_vacuum", int64(2)},
	} {
		var got any
		require.NoError(t, db.QueryRowContext(ctx, "PRAGMA "+pragma.name).Scan(&got))
		require.Equal(t, pragma.want, got, pragma.name)
	}
}

func TestMigrateNormalizesExistingScoutMessageTimestamps(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "existing.sqlite")
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	baseMigration, err := fs.ReadFile(sqlitemigrations.FS, "000001_init.sql")
	require.NoError(t, err)
	baseProvider, err := goose.NewProvider(goose.DialectSQLite3, db, fstest.MapFS{
		"000001_init.sql": &fstest.MapFile{Data: baseMigration},
	})
	require.NoError(t, err)
	_, err = baseProvider.Up(ctx)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `INSERT INTO scout_chats
		(id, telegram_chat_id, link, topic, created_at, updated_at)
		VALUES ('chat-1', '1001', 'https://t.me/scout1', 'topic',
			'2026-07-11T08:00:00Z', '2026-07-11T08:00:00Z')`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO scout_messages
		(telegram_chat_id, telegram_message_id, encrypted_text, nonce, message_at, received_at, edited_at)
		VALUES ('1001', 77, X'01', X'02',
			'2026-07-11T08:30:00Z',
			'2026-07-11T08:30:00.123Z',
			'2026-07-11T08:30:00.000000001Z')`)
	require.NoError(t, err)

	require.NoError(t, migrateThrough(ctx, db, 2))
	var messageAt, receivedAt, editedAt string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT message_at, received_at, edited_at
		FROM scout_messages WHERE telegram_message_id = 77`).Scan(&messageAt, &receivedAt, &editedAt))
	require.Equal(t, "2026-07-11T08:30:00.000000000Z", messageAt)
	require.Equal(t, "2026-07-11T08:30:00.123000000Z", receivedAt)
	require.Equal(t, "2026-07-11T08:30:00.000000001Z", editedAt)
}

func TestMigrateRequiresExplicitResumeForLegacyFloodWaitAccount(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "legacy.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	legacyFS := fstest.MapFS{}
	for _, name := range []string{"000001_init.sql", "000002_scout_message_tombstones.sql"} {
		migration, err := fs.ReadFile(sqlitemigrations.FS, name)
		require.NoError(t, err)
		legacyFS[name] = &fstest.MapFile{Data: migration}
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, legacyFS)
	require.NoError(t, err)
	_, err = provider.Up(ctx)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `INSERT INTO accounts
		(id, phone_masked, status, session_path, last_error, created_at, updated_at)
		VALUES ('opaque-account', '', 'flood_wait', '/sessions/opaque.session', 'legacy wait',
			'2026-07-12T12:00:00.000000000Z', '2026-07-12T12:00:00.000000000Z')`)
	require.NoError(t, err)

	require.NoError(t, migrateThrough(ctx, db, 11))
	var status, lastError string
	var reviewRequired int
	var floodWaitUntil sql.NullString
	require.NoError(t, db.QueryRowContext(ctx, `SELECT status, last_error, flood_wait_until, legacy_pause_review_required FROM accounts WHERE id='opaque-account'`).Scan(&status, &lastError, &floodWaitUntil, &reviewRequired))
	require.Equal(t, "paused", status)
	require.Empty(t, lastError)
	require.False(t, floodWaitUntil.Valid)
	require.Equal(t, 1, reviewRequired)
	resumed, err := usecase.NewAccountService(sqlite.NewProductionStore(db)).ResumeLegacyPaused(ctx, "opaque-account")
	require.NoError(t, err)
	require.Equal(t, domain.AccountStopped, resumed.Status)
}

func TestMigrationV5CleanupRemainsEffectiveWhenUpgradingThroughCurrent(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "acceptance-upgrade.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	legacyFS := fstest.MapFS{}
	for _, name := range []string{"000001_init.sql", "000002_scout_message_tombstones.sql", "000003_delivery_queue.sql"} {
		migration, err := fs.ReadFile(sqlitemigrations.FS, name)
		require.NoError(t, err)
		legacyFS[name] = &fstest.MapFile{Data: migration}
	}
	legacyFS["000004_upgrade_cleanup.sql"] = &fstest.MapFile{Data: []byte(`-- +goose Up
DELETE FROM account_channel_memberships
WHERE (catalog = 'outbound' AND channel_id = 'boat-chat-outbound')
   OR (catalog = 'scout' AND channel_id = 'boat-chat-scout');
DELETE FROM outbound_channels WHERE id = 'boat-chat-outbound';
DELETE FROM scout_chats WHERE id = 'boat-chat-scout';

-- +goose Down
-- Removed acceptance-only rows must never be recreated by rollback.
`)}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, legacyFS)
	require.NoError(t, err)
	_, err = provider.Up(ctx)
	require.NoError(t, err)
	version, err := goose.GetDBVersion(db)
	require.NoError(t, err)
	require.Equal(t, int64(4), version)

	const timestamp = "2026-07-12T12:00:00.000000000Z"
	_, err = db.ExecContext(ctx, `INSERT INTO outgoing_message_jobs
		(id,type,channel_id,target_telegram_id,status,next_attempt_at,created_at) VALUES
		('acceptance-public','public_reply','boat-chat-outbound','','queued',?,?),
		('acceptance-private','private_message','boat-chat-scout','encrypted-v1','delayed',?,?),
		('ordinary-public','public_reply','ordinary-channel','','queued',?,?)`,
		timestamp, timestamp, timestamp, timestamp, timestamp, timestamp)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO outgoing_message_events
		(id,job_id,account_id,channel_id,type,success,created_at) VALUES
		('acceptance-public-event','acceptance-public','account-a','boat-chat-outbound','public_reply',1,?),
		('acceptance-private-event','acceptance-private','account-a','boat-chat-scout','private_message',1,?),
		('ordinary-event','ordinary-public','account-a','ordinary-channel','public_reply',1,?)`,
		timestamp, timestamp, timestamp)
	require.NoError(t, err)

	require.NoError(t, migrateThrough(ctx, db, 11))
	var acceptanceJobs, acceptanceEvents, ordinaryJobs, ordinaryEvents int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outgoing_message_jobs
		WHERE channel_id IN ('boat-chat-outbound','boat-chat-scout')`).Scan(&acceptanceJobs))
	require.Zero(t, acceptanceJobs)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outgoing_message_events
		WHERE id IN ('acceptance-public-event','acceptance-private-event')`).Scan(&acceptanceEvents))
	require.Zero(t, acceptanceEvents)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outgoing_message_jobs WHERE id='ordinary-public'`).Scan(&ordinaryJobs))
	require.Equal(t, 1, ordinaryJobs)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outgoing_message_events WHERE id='ordinary-event'`).Scan(&ordinaryEvents))
	require.Equal(t, 1, ordinaryEvents)
	version, err = goose.GetDBVersion(db)
	require.NoError(t, err)
	require.Equal(t, int64(11), version)
	var leaseColumns int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('outgoing_message_jobs')
		WHERE name IN ('lease_token','lease_until')`).Scan(&leaseColumns))
	require.Equal(t, 2, leaseColumns)
}

func TestMigrationV7RemovesPersistedAbsoluteImportPaths(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "import-upgrade.sqlite"))
	require.NoError(t, err)
	defer db.Close()
	legacyFS := fstest.MapFS{}
	for _, name := range []string{
		"000001_init.sql", "000002_scout_message_tombstones.sql", "000003_delivery_queue.sql",
		"000004_upgrade_cleanup.sql", "000005_acceptance_delivery_cleanup.sql", "000006_delivery_leases.sql",
	} {
		migration, err := fs.ReadFile(sqlitemigrations.FS, name)
		require.NoError(t, err)
		legacyFS[name] = &fstest.MapFile{Data: migration}
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, legacyFS)
	require.NoError(t, err)
	_, err = provider.Up(ctx)
	require.NoError(t, err)
	const stamp = "2026-07-12T12:00:00.000000000Z"
	_, err = db.ExecContext(ctx, `INSERT INTO imports
		(id,file_name,file_path,sha256,rights_confirmed,imported_at,status,model_name,model_sha256,tokenizer_sha256,last_error)
		VALUES ('import-1','data.csv','/Users/private/data.csv',?,1,?,'complete','model',?,?,'')`,
		strings.Repeat("a", 64), stamp, strings.Repeat("b", 64), strings.Repeat("c", 64))
	require.NoError(t, err)

	require.NoError(t, migrateThrough(ctx, db, 7))
	var path string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT file_path FROM imports WHERE id='import-1'`).Scan(&path))
	require.Empty(t, path)
	version, err := goose.GetDBVersion(db)
	require.NoError(t, err)
	require.Equal(t, int64(7), version)
}

func TestMigrationV8PreservesAccountsAndForeignKeysWhileAddingStoppedStatus(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "stopped-upgrade.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, sqlite.Configure(ctx, db))

	legacyFS := fstest.MapFS{}
	for _, name := range []string{
		"000001_init.sql", "000002_scout_message_tombstones.sql", "000003_delivery_queue.sql",
		"000004_upgrade_cleanup.sql", "000005_acceptance_delivery_cleanup.sql", "000006_delivery_leases.sql",
		"000007_import_provenance_cleanup.sql",
	} {
		migration, readErr := fs.ReadFile(sqlitemigrations.FS, name)
		require.NoError(t, readErr)
		legacyFS[name] = &fstest.MapFile{Data: migration}
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, legacyFS)
	require.NoError(t, err)
	_, err = provider.Up(ctx)
	require.NoError(t, err)

	const stamp = "2026-07-12T12:00:00.000000000Z"
	_, err = db.ExecContext(ctx, `INSERT INTO accounts
		(id,phone_masked,display_name,role,status,session_path,created_at,updated_at)
		VALUES ('opaque-account','','Account','spammer','ready','account-0/session.json',?,?)`, stamp, stamp)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO account_channel_memberships
		(account_id,catalog,channel_id,is_member,status)
		VALUES ('opaque-account','outbound','channel-1',1,'member')`)
	require.NoError(t, err)

	require.NoError(t, migrateThrough(ctx, db, 8))
	_, err = db.ExecContext(ctx, `UPDATE accounts SET status='stopped' WHERE id='opaque-account'`)
	require.NoError(t, err)
	var accountCount, membershipCount, foreignKeyErrors int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts WHERE id='opaque-account' AND status='stopped'`).Scan(&accountCount))
	require.Equal(t, 1, accountCount)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM account_channel_memberships WHERE account_id='opaque-account'`).Scan(&membershipCount))
	require.Equal(t, 1, membershipCount)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&foreignKeyErrors))
	require.Zero(t, foreignKeyErrors)
	version, err := goose.GetDBVersion(db)
	require.NoError(t, err)
	require.Equal(t, int64(8), version)
}

func TestMigrationV9SanitizesLegacyBackupFailureText(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "backup-error-upgrade.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	legacyFS := fstest.MapFS{}
	for _, name := range []string{
		"000001_init.sql", "000002_scout_message_tombstones.sql", "000003_delivery_queue.sql",
		"000004_upgrade_cleanup.sql", "000005_acceptance_delivery_cleanup.sql", "000006_delivery_leases.sql",
		"000007_import_provenance_cleanup.sql", "000008_account_stopped_status.sql",
	} {
		migration, readErr := fs.ReadFile(sqlitemigrations.FS, name)
		require.NoError(t, readErr)
		legacyFS[name] = &fstest.MapFile{Data: migration}
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, legacyFS)
	require.NoError(t, err)
	_, err = provider.Up(ctx)
	require.NoError(t, err)
	const stamp = "2026-07-12T12:00:00.000000000Z"
	_, err = db.ExecContext(ctx, `INSERT INTO backup_history
		(id,archive_path,kind,status,created_at,error)
		VALUES ('failed','record:failed','daily','error',?,
		'open /Users/private/account-7/session.json: no such file')`, stamp)
	require.NoError(t, err)

	require.NoError(t, migrateThrough(ctx, db, 9))
	var failure string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT error FROM backup_history WHERE id='failed'`).Scan(&failure))
	require.Equal(t, "backup_failed", failure)
	version, err := goose.GetDBVersion(db)
	require.NoError(t, err)
	require.Equal(t, int64(9), version)
}

func TestMigrationV11PreservesAmbiguousLegacyPausesUntilSelectedAccountResume(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "runtime-stopped-upgrade.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	preV10FS := fstest.MapFS{}
	for _, name := range []string{
		"000001_init.sql", "000002_scout_message_tombstones.sql", "000003_delivery_queue.sql",
		"000004_upgrade_cleanup.sql", "000005_acceptance_delivery_cleanup.sql", "000006_delivery_leases.sql",
		"000007_import_provenance_cleanup.sql", "000008_account_stopped_status.sql",
		"000009_backup_error_sanitization.sql",
	} {
		migration, readErr := fs.ReadFile(sqlitemigrations.FS, name)
		require.NoError(t, readErr)
		preV10FS[name] = &fstest.MapFile{Data: migration}
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, preV10FS)
	require.NoError(t, err)
	_, err = provider.Up(ctx)
	require.NoError(t, err)

	const created = "2026-07-12T10:00:00.000000000Z"
	_, err = db.ExecContext(ctx, `INSERT INTO accounts
		(id,phone_masked,role,status,session_path,last_activity_at,last_error,created_at,updated_at) VALUES
		('runtime-paused','','spammer','paused','account-0/session.json',?,'cancelled',?,?),
		('user-paused','','spammer','paused','account-1/session.json',?,'',?,?),
		('fresh-ready','','spammer','ready','account-2/session.json',NULL,'',?,?)`,
		created, created, created, created, created, created, created, created)
	require.NoError(t, err)

	throughV10 := fstest.MapFS{}
	for _, name := range []string{
		"000001_init.sql", "000002_scout_message_tombstones.sql", "000003_delivery_queue.sql",
		"000004_upgrade_cleanup.sql", "000005_acceptance_delivery_cleanup.sql", "000006_delivery_leases.sql",
		"000007_import_provenance_cleanup.sql", "000008_account_stopped_status.sql",
		"000009_backup_error_sanitization.sql", "000010_account_status_source.sql",
	} {
		migration, readErr := fs.ReadFile(sqlitemigrations.FS, name)
		require.NoError(t, readErr)
		throughV10[name] = &fstest.MapFile{Data: migration}
	}
	v10Provider, err := goose.NewProvider(goose.DialectSQLite3, db, throughV10)
	require.NoError(t, err)
	_, err = v10Provider.Up(ctx)
	require.NoError(t, err)
	var v10Stopped int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts WHERE status='stopped'`).Scan(&v10Stopped))
	require.Equal(t, 2, v10Stopped, "fixture must reproduce v10's ambiguous reactivation")
	_, err = db.ExecContext(ctx, `UPDATE accounts SET status='ready',last_error='' WHERE id='user-paused'`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE accounts SET status='flood_wait',flood_wait_until='2099-07-12T14:00:00.000000000Z' WHERE id='runtime-paused'`)
	require.NoError(t, err)

	require.NoError(t, migrateThrough(ctx, db, 11))
	rows, err := db.QueryContext(ctx, `SELECT id,status,legacy_pause_review_required FROM accounts ORDER BY id`)
	require.NoError(t, err)
	defer rows.Close()
	type state struct {
		status string
		review int
	}
	got := map[string]state{}
	for rows.Next() {
		var id, status string
		var review int
		require.NoError(t, rows.Scan(&id, &status, &review))
		got[id] = state{status: status, review: review}
	}
	require.NoError(t, rows.Err())
	require.Equal(t, state{status: "paused", review: 1}, got["runtime-paused"])
	require.Equal(t, state{status: "paused", review: 1}, got["user-paused"])
	require.Equal(t, state{status: "paused", review: 1}, got["fresh-ready"])
	active, err := sqlite.NewProductionStore(db).Accounts().ListActive(ctx)
	require.NoError(t, err)
	require.Empty(t, active, "ordinary migration and START discovery must not activate ambiguous pauses")

	resumed, err := usecase.NewAccountService(sqlite.NewProductionStore(db)).ResumeLegacyPaused(ctx, "runtime-paused")
	require.NoError(t, err)
	require.Equal(t, domain.AccountFloodWait, resumed.Status)
	active, err = sqlite.NewProductionStore(db).Accounts().ListActive(ctx)
	require.NoError(t, err)
	require.Empty(t, active, "explicit resume must honor Telegram's future flood-wait deadline")
	_, err = db.ExecContext(ctx, `UPDATE accounts SET flood_wait_until='2020-01-01T00:00:00.000000000Z' WHERE id='runtime-paused'`)
	require.NoError(t, err)
	active, err = sqlite.NewProductionStore(db).Accounts().ListActive(ctx)
	require.NoError(t, err)
	require.Len(t, active, 1)
	require.Equal(t, domain.ID("runtime-paused"), active[0].ID)
	var userStatus string
	var userReview int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT status,legacy_pause_review_required FROM accounts WHERE id='user-paused'`).Scan(&userStatus, &userReview))
	require.Equal(t, "paused", userStatus)
	require.Equal(t, 1, userReview)
	version, err := goose.GetDBVersion(db)
	require.NoError(t, err)
	require.Equal(t, int64(11), version)
	currentProvider, err := goose.NewProvider(goose.DialectSQLite3, db, sqlitemigrations.FS)
	require.NoError(t, err)
	_, err = currentProvider.Down(ctx)
	require.NoError(t, err)
	var reviewColumns int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('accounts') WHERE name='legacy_pause_review_required'`).Scan(&reviewColumns))
	require.Zero(t, reviewColumns)
}

func TestOpenConfiguresEveryConnection(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "app.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	connections := make([]*sql.Conn, 0, 2)
	for range 2 {
		conn, err := db.Conn(ctx)
		require.NoError(t, err)
		connections = append(connections, conn)
	}
	t.Cleanup(func() {
		for _, conn := range connections {
			require.NoError(t, conn.Close())
		}
	})

	for _, conn := range connections {
		var enabled int
		require.NoError(t, conn.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&enabled))
		require.Equal(t, 1, enabled)
	}
}

func TestMembershipStatusMigrationPreservesRowsAndAcceptsModerationStates(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "memberships.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, migrateThrough(ctx, db, 18))

	now := "2026-07-17T12:00:00Z"
	_, err = db.ExecContext(ctx, `INSERT INTO accounts
		(id,phone_masked,display_name,role,status,session_path,created_at,updated_at)
		VALUES ('account-1','+7***','','spammer','ready','/session',?,?)`, now, now)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO outbound_channels
		(id,telegram_chat_id,title,link,topic,status,active,created_at,updated_at)
		VALUES ('channel-1','pending','Room','https://t.me/room','Test','joining',1,?,?)`, now, now)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO account_channel_memberships
		(account_id,catalog,channel_id,is_member,status,last_error)
		VALUES ('account-1','outbound','channel-1',0,'joining','')`)
	require.NoError(t, err)

	require.NoError(t, migrateThrough(ctx, db, 19))
	var status string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT status FROM account_channel_memberships
		WHERE account_id='account-1' AND catalog='outbound' AND channel_id='channel-1'`).Scan(&status))
	require.Equal(t, "joining", status)

	for _, next := range []string{"pending_approval", "flood_wait"} {
		_, err = db.ExecContext(ctx, `UPDATE account_channel_memberships SET status=?
			WHERE account_id='account-1' AND catalog='outbound' AND channel_id='channel-1'`, next)
		require.NoError(t, err, next)
	}
}

func TestMembershipModerationTimestampsRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "memberships.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, sqlite.Migrate(ctx, db))

	store := sqlite.NewProductionStore(db)
	now := time.Date(2026, 7, 18, 7, 0, 0, 0, time.UTC)
	require.NoError(t, store.Accounts().Save(ctx, domain.Account{
		ID: "account-1", Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
		SessionPath: "/sessions/account-1", CreatedAt: now, UpdatedAt: now,
	}))
	membership := domain.ChannelMembership{
		AccountID: "account-1", ChannelID: "channel-1",
		Status: "member", IsMember: true,
		RequestSubmittedAt: ptrTime(time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)),
		JoinedAt:           ptrTime(time.Date(2026, 7, 18, 8, 17, 0, 0, time.UTC)),
	}

	require.NoError(t, store.SaveMembership(ctx, domain.SourceCatalogOutbound, membership))
	memberships, err := store.ListMemberships(ctx, domain.SourceCatalogOutbound, "channel-1")
	require.NoError(t, err)
	require.Len(t, memberships, 1)
	require.Equal(t, membership.RequestSubmittedAt, memberships[0].RequestSubmittedAt)
	require.Equal(t, membership.JoinedAt, memberships[0].JoinedAt)

	catalogMembership := membership
	catalogMembership.ChannelID = "channel-2"
	require.NoError(t, store.Catalogs().SaveMembership(ctx, domain.SourceCatalogOutbound, catalogMembership))
	loaded, found, err := store.Catalogs().LoadMembership(ctx, "account-1", domain.SourceCatalogOutbound, "channel-2")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, catalogMembership.RequestSubmittedAt, loaded.RequestSubmittedAt)
	require.Equal(t, catalogMembership.JoinedAt, loaded.JoinedAt)
}

func TestSaveMembershipPreservesModerationTimestampsOnPartialUpdate(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "memberships.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, sqlite.Migrate(ctx, db))

	store := sqlite.NewProductionStore(db)
	now := time.Date(2026, 7, 18, 7, 0, 0, 0, time.UTC)
	require.NoError(t, store.Accounts().Save(ctx, domain.Account{
		ID: "account-1", Role: domain.AccountRoleSpammer, Status: domain.AccountActive,
		SessionPath: "/sessions/account-1", CreatedAt: now, UpdatedAt: now,
	}))
	membership := domain.ChannelMembership{
		AccountID: "account-1", ChannelID: "channel-1",
		Status: "member", IsMember: true,
		RequestSubmittedAt: ptrTime(time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)),
		JoinedAt:           ptrTime(time.Date(2026, 7, 18, 8, 17, 0, 0, time.UTC)),
	}
	require.NoError(t, store.SaveMembership(ctx, domain.SourceCatalogOutbound, membership))

	partialUpdate := domain.ChannelMembership{
		AccountID: "account-1", ChannelID: "channel-1",
		Status: "error", IsMember: false, LastError: "temporary failure",
	}
	require.NoError(t, store.SaveMembership(ctx, domain.SourceCatalogOutbound, partialUpdate))

	memberships, err := store.ListMemberships(ctx, domain.SourceCatalogOutbound, "channel-1")
	require.NoError(t, err)
	require.Len(t, memberships, 1)
	require.Equal(t, membership.RequestSubmittedAt, memberships[0].RequestSubmittedAt)
	require.Equal(t, membership.JoinedAt, memberships[0].JoinedAt)

	conflictingUpdate := partialUpdate
	conflictingUpdate.RequestSubmittedAt = ptrTime(time.Date(2026, 7, 18, 9, 0, 0, 0, time.UTC))
	conflictingUpdate.JoinedAt = ptrTime(time.Date(2026, 7, 18, 9, 17, 0, 0, time.UTC))
	require.NoError(t, store.SaveMembership(ctx, domain.SourceCatalogOutbound, conflictingUpdate))

	memberships, err = store.ListMemberships(ctx, domain.SourceCatalogOutbound, "channel-1")
	require.NoError(t, err)
	require.Len(t, memberships, 1)
	require.Equal(t, membership.RequestSubmittedAt, memberships[0].RequestSubmittedAt)
	require.Equal(t, membership.JoinedAt, memberships[0].JoinedAt)
}

func TestMigration20BackfillsPendingApprovalTimestamp(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "memberships.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, migrateThrough(ctx, db, 19))

	const lastCheckAt = "2026-07-18T08:00:00.000000000Z"
	_, err = db.ExecContext(ctx, `INSERT INTO accounts
		(id,phone_masked,display_name,role,status,session_path,created_at,updated_at)
		VALUES ('account-1','','','spammer','ready','/sessions/account-1',?,?)`, lastCheckAt, lastCheckAt)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO account_channel_memberships
		(account_id,catalog,channel_id,is_member,status,last_check_at,last_error) VALUES
		('account-1','outbound','pending-approval',0,'pending_approval',?,'pending'),
		('account-1','outbound','member',1,'member',?,'')`, lastCheckAt, lastCheckAt)
	require.NoError(t, err)

	require.NoError(t, migrateThrough(ctx, db, 20))
	var submittedAt sql.NullString
	require.NoError(t, db.QueryRowContext(ctx, `SELECT request_submitted_at FROM account_channel_memberships
		WHERE channel_id='pending-approval'`).Scan(&submittedAt))
	require.Equal(t, sql.NullString{String: lastCheckAt, Valid: true}, submittedAt)

	var memberSubmittedAt, memberJoinedAt sql.NullString
	require.NoError(t, db.QueryRowContext(ctx, `SELECT request_submitted_at,joined_at FROM account_channel_memberships
		WHERE channel_id='member'`).Scan(&memberSubmittedAt, &memberJoinedAt))
	require.False(t, memberSubmittedAt.Valid)
	require.False(t, memberJoinedAt.Valid)

	provider, err := goose.NewProvider(goose.DialectSQLite3, db, sqlitemigrations.FS)
	require.NoError(t, err)
	_, err = provider.Down(ctx)
	require.NoError(t, err)
	version, err := provider.GetDBVersion(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(19), version)

	var timestampColumns int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('account_channel_memberships')
		WHERE name IN ('request_submitted_at','joined_at')`).Scan(&timestampColumns))
	require.Zero(t, timestampColumns)
	var status, preservedLastCheckAt, lastError string
	var isMember int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT is_member,status,last_check_at,last_error FROM account_channel_memberships
		WHERE channel_id='pending-approval'`).Scan(&isMember, &status, &preservedLastCheckAt, &lastError))
	require.Equal(t, []any{0, "pending_approval", lastCheckAt, "pending"}, []any{isMember, status, preservedLastCheckAt, lastError})
}

func TestJoinScheduleMigrationPreservesNullableCompatibility(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "memberships.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, migrateThrough(ctx, db, 20))

	const timestamp = "2026-07-18T08:00:00.000000000Z"
	_, err = db.ExecContext(ctx, `INSERT INTO accounts
		(id,phone_masked,display_name,role,status,session_path,created_at,updated_at)
		VALUES ('account-1','','','spammer','ready','/sessions/account-1',?,?)`, timestamp, timestamp)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO account_channel_memberships
		(account_id,catalog,channel_id,is_member,status,last_check_at,request_submitted_at,joined_at,last_error)
		VALUES ('account-1','outbound','channel-1',1,'member',?,?,?,'')`, timestamp, timestamp, timestamp)
	require.NoError(t, err)

	require.NoError(t, migrateThrough(ctx, db, 21))
	var joinNotBefore sql.NullString
	require.NoError(t, db.QueryRowContext(ctx, `SELECT join_not_before FROM account_channel_memberships
		WHERE account_id='account-1' AND catalog='outbound' AND channel_id='channel-1'`).Scan(&joinNotBefore))
	require.False(t, joinNotBefore.Valid)

	provider, err := goose.NewProvider(goose.DialectSQLite3, db, sqlitemigrations.FS)
	require.NoError(t, err)
	_, err = provider.Down(ctx)
	require.NoError(t, err)
	version, err := provider.GetDBVersion(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(20), version)

	var requestSubmittedAt, joinedAt string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT request_submitted_at,joined_at FROM account_channel_memberships
		WHERE account_id='account-1' AND catalog='outbound' AND channel_id='channel-1'`).Scan(&requestSubmittedAt, &joinedAt))
	require.Equal(t, timestamp, requestSubmittedAt)
	require.Equal(t, timestamp, joinedAt)
}

func TestAccountGroupRestMigrationUpDown(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "account-group-rest.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, migrateThrough(ctx, db, 24))

	var sqliteVersion string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT sqlite_version()`).Scan(&sqliteVersion))
	t.Logf("bundled sqlite_version()=%s", sqliteVersion)

	const timestamp = "2026-07-20T08:00:00.000000000Z"
	_, err = db.ExecContext(ctx, `INSERT INTO accounts
		(id,phone_masked,display_name,role,status,session_path,created_at,updated_at)
		VALUES ('account-1','','','spammer','ready','/sessions/account-1',?,?)`, timestamp, timestamp)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO account_channel_memberships
		(account_id,catalog,channel_id,is_member,status,last_check_at,request_submitted_at,joined_at,join_not_before,last_error)
		VALUES ('account-1','outbound','channel-1',1,'member',?,?,?,?,'')`,
		timestamp, timestamp, timestamp, timestamp)
	require.NoError(t, err)

	require.NoError(t, migrateThrough(ctx, db, 25))
	var restColumns int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('account_channel_memberships')
		WHERE name IN ('rest_started_at','rest_until','rest_duration_hours')`).Scan(&restColumns))
	require.Equal(t, 3, restColumns)
	var restStartedAt, restUntil sql.NullString
	var durationHours int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT rest_started_at,rest_until,rest_duration_hours
		FROM account_channel_memberships WHERE account_id='account-1'`).Scan(&restStartedAt, &restUntil, &durationHours))
	require.False(t, restStartedAt.Valid)
	require.False(t, restUntil.Valid)
	require.Zero(t, durationHours)

	_, err = db.ExecContext(ctx, `UPDATE account_channel_memberships
		SET rest_started_at=?,rest_until=?,rest_duration_hours=36 WHERE account_id='account-1'`, timestamp, timestamp)
	require.NoError(t, err)
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, sqlitemigrations.FS)
	require.NoError(t, err)
	_, err = provider.Down(ctx)
	require.NoError(t, err)
	version, err := provider.GetDBVersion(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(24), version)

	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('account_channel_memberships')
		WHERE name IN ('rest_started_at','rest_until','rest_duration_hours')`).Scan(&restColumns))
	require.Zero(t, restColumns)
	var joinedAt, joinNotBefore string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT joined_at,join_not_before FROM account_channel_memberships
		WHERE account_id='account-1'`).Scan(&joinedAt, &joinNotBefore))
	require.Equal(t, timestamp, joinedAt)
	require.Equal(t, timestamp, joinNotBefore)
}

func TestMembershipRestBackfillMigrationOnlyUpdatesLegacyJoiningAndPendingApproval(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "membership-rest-backfill.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, migrateThrough(ctx, db, 27))

	const timestamp = "2026-07-27T08:00:00.000000000Z"
	_, err = db.ExecContext(ctx, `INSERT INTO accounts
		(id,phone_masked,display_name,role,status,session_path,created_at,updated_at)
		VALUES ('account-1','','','spammer','ready','/sessions/account-1',?,?)`, timestamp, timestamp)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO account_channel_memberships
		(account_id,catalog,channel_id,is_member,status,rest_duration_hours,last_error)
		VALUES
			('account-1','outbound','joining-zero',0,'joining',0,''),
			('account-1','outbound','approval-zero',0,'pending_approval',0,''),
			('account-1','outbound','joining-configured',0,'joining',72,''),
			('account-1','outbound','approval-configured',0,'pending_approval',48,''),
			('account-1','outbound','member-zero',1,'member',0,''),
			('account-1','outbound','error-zero',0,'error',0,'')`)
	require.NoError(t, err)

	require.NoError(t, migrateThrough(ctx, db, 28))

	expected := map[string]int{
		"joining-zero":        36,
		"approval-zero":       36,
		"joining-configured":  72,
		"approval-configured": 48,
		"member-zero":         0,
		"error-zero":          0,
	}
	for channelID, expectedHours := range expected {
		var actualHours int
		require.NoError(t, db.QueryRowContext(ctx, `SELECT rest_duration_hours
			FROM account_channel_memberships WHERE account_id='account-1' AND channel_id=?`, channelID).Scan(&actualHours))
		require.Equal(t, expectedHours, actualHours, channelID)
	}
}

func TestScheduledDMMigrationDownRemovesOnlyScheduledTables(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "scheduled-dm-migration.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, sqlite.Configure(ctx, db))
	require.NoError(t, migrateThrough(ctx, db, 21))
	require.NoError(t, migrateThrough(ctx, db, 22))

	for _, table := range []string{
		"scheduled_dm_tasks", "scheduled_dm_recipients", "scheduled_dm_accounts",
		"scheduled_dm_runs", "scheduled_dm_deliveries",
	} {
		var count int
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count))
		require.Equal(t, 1, count, table)
	}

	provider, err := goose.NewProvider(goose.DialectSQLite3, db, sqlitemigrations.FS)
	require.NoError(t, err)
	_, err = provider.Down(ctx)
	require.NoError(t, err)
	for _, table := range []string{
		"scheduled_dm_tasks", "scheduled_dm_recipients", "scheduled_dm_accounts",
		"scheduled_dm_runs", "scheduled_dm_deliveries",
	} {
		var count int
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count))
		require.Zero(t, count, table)
	}
	var accounts int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='accounts'`).Scan(&accounts))
	require.Equal(t, 1, accounts)
}

func ptrTime(value time.Time) *time.Time {
	return &value
}
