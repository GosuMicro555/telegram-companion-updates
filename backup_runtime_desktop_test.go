//go:build desktop

package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/pressly/goose/v3"
	"telegram-companion/internal/domain"
	"telegram-companion/internal/repository/localdb"
	"telegram-companion/internal/repository/sqlite"
	sqlitemigrations "telegram-companion/internal/repository/sqlite/migrations"
	telegramgotd "telegram-companion/internal/telegram/gotd"
	wailsbindings "telegram-companion/internal/transport/wails"
	"telegram-companion/internal/usecase"

	"github.com/stretchr/testify/require"
)

type desktopBackupSecrets struct{ key []byte }

func (s desktopBackupSecrets) GetOrCreate(context.Context, string, int) ([]byte, error) {
	return append([]byte(nil), s.key...), nil
}

func TestDesktopLifecycleClosesBackupBackgroundServices(t *testing.T) {
	root := t.TempDir()
	settings, err := localdb.OpenKeywordSettingsStore(filepath.Join(root, "settings.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, settings.Close()) })

	backupStarted := make(chan struct{})
	backupStopped := make(chan struct{})
	automation := usecase.NewAutomationController(usecase.AutomationRunnerFunc(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}))
	bindings := wailsbindings.NewBindings(automation, settings)
	bindings.ConfigureBackups(wailsbindings.BackupRuntime{Runner: usecase.AutomationRunnerFunc(func(ctx context.Context) error {
		close(backupStarted)
		<-ctx.Done()
		close(backupStopped)
		return ctx.Err()
	})})
	app := &DesktopApp{
		bindings: bindings,
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	require.NoError(t, app.bindings.StartAutomation())
	requireChannelClosed(t, backupStarted)

	app.closeBackgroundServices(context.Background())
	requireChannelClosed(t, backupStopped)
}

func requireChannelClosed(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("background service did not stop")
	}
}

func TestDesktopBackupCompositionCreatesVerifiedArchive(t *testing.T) {
	root := t.TempDir()
	settings, err := localdb.OpenKeywordSettingsStore(filepath.Join(root, "data", "settings.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, settings.Close()) })

	runtime, err := newDesktopBackupRuntimeAt(context.Background(), root, settings, desktopBackupSecrets{key: bytes.Repeat([]byte{0x51}, 32)})
	require.NoError(t, err)
	require.NotNil(t, runtime.Service)
	require.NotNil(t, runtime.Runner)
	record, err := runtime.Service.Create(context.Background(), domain.BackupDaily)
	require.NoError(t, err)
	require.Equal(t, domain.BackupVerified, record.Status)
	require.Equal(t, filepath.Join(root, "data", "backups"), filepath.Dir(record.ArchivePath))
}

func TestOpenProductionDatabaseCreatesVerifiedBackupBeforePendingMigration(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "data", "app.db")
	require.NoError(t, os.MkdirAll(filepath.Dir(databasePath), 0o700))
	db, err := sql.Open("sqlite", databasePath)
	require.NoError(t, err)
	baseMigration, err := fs.ReadFile(sqlitemigrations.FS, "000001_init.sql")
	require.NoError(t, err)
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, fstest.MapFS{
		"000001_init.sql": &fstest.MapFile{Data: baseMigration},
	})
	require.NoError(t, err)
	_, err = provider.Up(ctx)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	opened, err := openProductionDatabase(ctx, root, desktopBackupSecrets{key: bytes.Repeat([]byte{0x54}, 32)})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, opened.Close()) })

	archives, err := filepath.Glob(filepath.Join(root, "data", "backups", "*pre_migration*"))
	require.NoError(t, err)
	require.Len(t, archives, 1)
	var version int64
	require.NoError(t, opened.QueryRowContext(ctx, `SELECT MAX(version_id) FROM goose_db_version WHERE is_applied=1`).Scan(&version))
	require.GreaterOrEqual(t, version, int64(2))
}

func TestPreMigrationBackupRestoresRollbackDatabaseAfterUpgrade(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "data", "app.db")
	require.NoError(t, os.MkdirAll(filepath.Dir(databasePath), 0o700))
	db, err := sql.Open("sqlite", databasePath)
	require.NoError(t, err)
	baseMigration, err := fs.ReadFile(sqlitemigrations.FS, "000001_init.sql")
	require.NoError(t, err)
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, fstest.MapFS{
		"000001_init.sql": &fstest.MapFile{Data: baseMigration},
	})
	require.NoError(t, err)
	_, err = provider.Up(ctx)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO app_settings(key,value_json,revision,updated_at)
		VALUES ('rollback-marker','{"generation":"before"}',1,'2026-07-12T12:00:00.000000000Z')`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	secrets := desktopBackupSecrets{key: bytes.Repeat([]byte{0x57}, 32)}
	opened, err := openProductionDatabase(ctx, root, secrets)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, opened.Close()) })
	archives, err := filepath.Glob(filepath.Join(root, "data", "backups", "*pre_migration*"))
	require.NoError(t, err)
	require.Len(t, archives, 1)

	runtime, err := newDesktopBackupRuntimeAt(ctx, root, sqlite.NewProductionStore(opened), secrets)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtime.Close(context.Background())) })
	destination := filepath.Join(t.TempDir(), "rollback")
	require.NoError(t, runtime.Service.Restore(ctx, archives[0], destination))
	restored, err := sql.Open("sqlite", filepath.Join(destination, "database.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restored.Close()) })
	var value string
	require.NoError(t, restored.QueryRowContext(ctx, `SELECT value_json FROM app_settings WHERE key='rollback-marker'`).Scan(&value))
	require.JSONEq(t, `{"generation":"before"}`, value)
	var version int64
	require.NoError(t, restored.QueryRowContext(ctx, `SELECT MAX(version_id) FROM goose_db_version WHERE is_applied=1`).Scan(&version))
	require.Equal(t, int64(1), version)
}

func TestOpenProductionDatabaseConfiguresEveryPooledConnection(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := openProductionDatabase(ctx, root, desktopBackupSecrets{key: bytes.Repeat([]byte{0x58}, 32)})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	connections := make([]*sql.Conn, 0, 4)
	for range 4 {
		connection, err := db.Conn(ctx)
		require.NoError(t, err)
		connections = append(connections, connection)
	}
	t.Cleanup(func() {
		for _, connection := range connections {
			require.NoError(t, connection.Close())
		}
	})
	for _, connection := range connections {
		var foreignKeys, busyTimeout int
		require.NoError(t, connection.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys))
		require.NoError(t, connection.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busyTimeout))
		require.Equal(t, 1, foreignKeys)
		require.Equal(t, 5000, busyTimeout)
	}
}

func TestDesktopBackupRuntimeSharesGotdSessionBarrier(t *testing.T) {
	root := t.TempDir()
	settings, err := localdb.OpenKeywordSettingsStore(filepath.Join(root, "data", "settings.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, settings.Close()) })
	runtime, err := newDesktopBackupRuntimeAt(context.Background(), root, settings, desktopBackupSecrets{key: bytes.Repeat([]byte{0x52}, 32)})
	require.NoError(t, err)

	releaseWrite, err := telegramgotd.ProductionSessionBarrier().AcquireWrite(context.Background())
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() {
		_, createErr := runtime.Service.Create(context.Background(), domain.BackupDaily)
		done <- createErr
	}()
	select {
	case err := <-done:
		t.Fatalf("backup session read overlapped gotd session write: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	releaseWrite()
	require.NoError(t, <-done)
}

func TestDesktopBackupRestoresConsistentLiveBoltStateDuringWrites(t *testing.T) {
	root := t.TempDir()
	settings, err := localdb.OpenKeywordSettingsStore(filepath.Join(root, "data", "settings.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, settings.Close()) })
	runtime, err := newDesktopBackupRuntimeAt(context.Background(), root, settings, desktopBackupSecrets{key: bytes.Repeat([]byte{0x53}, 32)})
	require.NoError(t, err)

	started := make(chan struct{})
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		for generation := 1; ; generation++ {
			select {
			case <-stop:
				done <- nil
				return
			default:
			}
			value := fmt.Sprintf("generation-%d", generation)
			if err := settings.Save(context.Background(), domain.KeywordSettings{Keywords: []string{value}, SharedReply: value}); err != nil {
				done <- err
				return
			}
			if generation == 1 {
				close(started)
			}
		}
	}()
	<-started
	record, err := runtime.Service.Create(context.Background(), domain.BackupDaily)
	close(stop)
	require.NoError(t, <-done)
	require.NoError(t, err)

	restoredDir := filepath.Join(t.TempDir(), "restored")
	require.NoError(t, runtime.Service.Restore(context.Background(), record.ArchivePath, restoredDir))
	restored, err := localdb.OpenKeywordSettingsStore(filepath.Join(restoredDir, "application-state.bolt"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restored.Close()) })
	got, err := restored.Load(context.Background())
	require.NoError(t, err)
	require.Len(t, got.Keywords, 1)
	require.Equal(t, got.SharedReply, got.Keywords[0])
}
