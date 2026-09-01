package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"telegram-companion/internal/repository/sqlite"
)

func TestCurrentMigrationRemovesLegacyImportTablesAndPreservesExistingAccountData(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "local-tdata-upgrade.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, sqlite.Configure(ctx, db))
	require.NoError(t, migrateThrough(ctx, db, 30))
	_, err = db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS tdata_import_batches (id TEXT PRIMARY KEY);
		CREATE TABLE IF NOT EXISTS tdata_import_items (id TEXT PRIMARY KEY);
		CREATE TABLE IF NOT EXISTS tdata_import_batch_items (batch_id TEXT, item_id TEXT);
	`)
	require.NoError(t, err)

	const stamp = "2026-08-24T12:00:00.000000000Z"
	const sessionPath = "data/sessions/existing-account/session.json"
	_, err = db.ExecContext(ctx, `INSERT INTO accounts
		(id,phone_masked,display_name,role,status,session_path,created_at,updated_at)
		VALUES ('existing-account','','Existing','spammer','ready',?,?,?)`, sessionPath, stamp, stamp)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO telegram_account_credentials
		(account_id,nonce,ciphertext,updated_at) VALUES ('existing-account',X'010203',X'040506',?)`, stamp)
	require.NoError(t, err)
	require.NoError(t, sqlite.Migrate(ctx, db))

	var gotSessionPath, gotStatus string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT session_path,status FROM accounts WHERE id='existing-account'`).Scan(&gotSessionPath, &gotStatus))
	require.Equal(t, sessionPath, gotSessionPath)
	require.Equal(t, "ready", gotStatus)

	var gotNonce, gotCiphertext []byte
	require.NoError(t, db.QueryRowContext(ctx, `SELECT nonce,ciphertext FROM telegram_account_credentials
		WHERE account_id='existing-account'`).Scan(&gotNonce, &gotCiphertext))
	require.Equal(t, []byte{1, 2, 3}, gotNonce)
	require.Equal(t, []byte{4, 5, 6}, gotCiphertext)

	var legacyTableCount int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master
		WHERE type='table' AND name LIKE 'tdata_import_%'`).Scan(&legacyTableCount))
	require.Zero(t, legacyTableCount)
}
