package sqlite_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"telegram-companion/internal/repository/sqlite"
	sqlitemigrations "telegram-companion/internal/repository/sqlite/migrations"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func TestMigrateWithBackupGatesExistingDatabaseBeforeUpgrade(t *testing.T) {
	ctx := context.Background()
	db := databaseAtFirstMigration(t)
	backupCalled := false
	require.NoError(t, sqlite.MigrateWithBackup(ctx, db, func(context.Context) error {
		backupCalled = true
		require.False(t, tableExists(t, db, "scout_message_tombstones"))
		return nil
	}))
	require.True(t, backupCalled)
	require.True(t, tableExists(t, db, "scout_message_tombstones"))
}

func TestMigrateWithBackupPreservesSchemaWhenBackupFails(t *testing.T) {
	db := databaseAtFirstMigration(t)
	want := errors.New("backup failed")
	err := sqlite.MigrateWithBackup(context.Background(), db, func(context.Context) error { return want })
	require.ErrorIs(t, err, want)
	require.False(t, tableExists(t, db, "scout_message_tombstones"))
}

func TestOpenWithMigrationBackupSkipsBackupForFreshDatabase(t *testing.T) {
	called := false
	db, err := sqlite.OpenWithMigrationBackup(context.Background(), filepath.Join(t.TempDir(), "fresh.sqlite"), func(context.Context) error {
		called = true
		return nil
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.False(t, called)
	require.True(t, tableExists(t, db, "scout_message_tombstones"))
}

func TestOpenUnmigratedDoesNotApplySchema(t *testing.T) {
	db, err := sqlite.OpenUnmigrated(context.Background(), filepath.Join(t.TempDir(), "unmigrated.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.False(t, tableExists(t, db, "accounts"))
}

func TestOpenUnmigratedDoesNotMutateExistingDatabaseBeforeBackupGate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.sqlite")
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE existing(value TEXT); INSERT INTO existing VALUES ('before')`)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	beforeHash := sha256.Sum256(before)

	db, err = sqlite.OpenUnmigrated(context.Background(), path)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.True(t, bytes.Equal(before, after), "database bytes changed before pre-migration backup: %x != %x", beforeHash, sha256.Sum256(after))
}

func databaseAtFirstMigration(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "existing.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	baseMigration, err := fs.ReadFile(sqlitemigrations.FS, "000001_init.sql")
	require.NoError(t, err)
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, fstest.MapFS{
		"000001_init.sql": &fstest.MapFile{Data: baseMigration},
	})
	require.NoError(t, err)
	_, err = provider.Up(context.Background())
	require.NoError(t, err)
	return db
}

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&count))
	return count == 1
}
