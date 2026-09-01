package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"telegram-companion/internal/repository/sqlite/migrations"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

const maxOpenConnections = 4

// Open creates a private SQLite database, applies the embedded schema, and
// limits the WAL connection pool to one writer and three concurrent readers.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	db, err := openConfigured(ctx, path)
	if err != nil {
		return nil, err
	}
	if err := Migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := enforceDatabaseModes(path); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// OpenUnmigrated opens SQLite without applying schema or persistent pragma
// changes. Production uses it to establish the pre-migration backup gate before
// anything can mutate an existing database.
func OpenUnmigrated(ctx context.Context, path string) (*sql.DB, error) {
	return openUnconfigured(ctx, path)
}

// OpenWithMigrationBackup preserves Open's configuration while requiring the
// backup callback before any upgrade of an existing migrated database.
func OpenWithMigrationBackup(ctx context.Context, path string, beforeUpgrade func(context.Context) error) (*sql.DB, error) {
	db, err := openUnconfigured(ctx, path)
	if err != nil {
		return nil, err
	}
	if err := MigrateWithBackup(ctx, db, beforeUpgrade); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := db.Close(); err != nil {
		return nil, fmt.Errorf("close pre-migration sqlite database: %w", err)
	}
	return openConfigured(ctx, path)
}

func openConfigured(ctx context.Context, path string) (*sql.DB, error) {
	db, err := openDatabase(ctx, path, true)
	if err != nil {
		return nil, err
	}
	if err := Configure(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func openUnconfigured(ctx context.Context, path string) (*sql.DB, error) {
	return openDatabase(ctx, path, false)
}

func openDatabase(ctx context.Context, path string, configured bool) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("protect database directory: %w", err)
	}

	query := url.Values{"mode": {"rwc"}}
	if configured {
		query["_pragma"] = []string{
			"journal_mode(WAL)",
			"foreign_keys(ON)",
			"busy_timeout(5000)",
			"secure_delete(ON)",
			"auto_vacuum(INCREMENTAL)",
		}
	}
	dsn := (&url.URL{
		Scheme:   "file",
		Path:     path,
		RawQuery: query.Encode(),
	}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}

	closeOnError := func(err error) (*sql.DB, error) {
		_ = db.Close()
		return nil, err
	}

	db.SetMaxOpenConns(maxOpenConnections)
	db.SetMaxIdleConns(maxOpenConnections)
	if err := db.PingContext(ctx); err != nil {
		return closeOnError(fmt.Errorf("ping sqlite database: %w", err))
	}
	if err := enforceDatabaseModes(path); err != nil {
		return closeOnError(err)
	}

	return db, nil
}

// Configure applies the runtime SQLite settings after a migration backup gate
// has succeeded. Some of these operations persist changes to the database.
func Configure(ctx context.Context, db *sql.DB) error {
	for _, pragma := range []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA foreign_keys=ON`,
		`PRAGMA busy_timeout=5000`,
		`PRAGMA secure_delete=ON`,
		`PRAGMA auto_vacuum=INCREMENTAL`,
	} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			return fmt.Errorf("configure sqlite database: %w", err)
		}
	}
	return nil
}

func enforceDatabaseModes(path string) error {
	for _, target := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(target, 0o600); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("protect sqlite file: %w", err)
		}
	}
	return nil
}

// Migrate applies each embedded schema migration once.
func Migrate(ctx context.Context, db *sql.DB) error {
	provider, err := migrationProvider(db)
	if err != nil {
		return err
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("apply sqlite migrations: %w", err)
	}
	return pauseActiveScheduledDMTasks(ctx, db)
}

// MigrateWithBackup gates upgrades of databases that already have at least one
// applied migration. Fresh database initialization does not invoke the callback.
func MigrateWithBackup(ctx context.Context, db *sql.DB, beforeUpgrade func(context.Context) error) error {
	provider, err := migrationProvider(db)
	if err != nil {
		return err
	}
	pending, err := provider.HasPending(ctx)
	if err != nil {
		return fmt.Errorf("check pending sqlite migrations: %w", err)
	}
	if !pending {
		return pauseActiveScheduledDMTasks(ctx, db)
	}
	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		return fmt.Errorf("read sqlite migration version: %w", err)
	}
	if version > 0 {
		if beforeUpgrade == nil {
			return errors.New("pre-migration backup callback is required")
		}
		if err := beforeUpgrade(ctx); err != nil {
			return fmt.Errorf("create pre-migration backup: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("apply sqlite migrations: %w", err)
	}
	return pauseActiveScheduledDMTasks(ctx, db)
}

func migrationProvider(db *sql.DB) (*goose.Provider, error) {
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, migrations.FS)
	if err != nil {
		return nil, fmt.Errorf("create sqlite migration provider: %w", err)
	}
	return provider, nil
}
