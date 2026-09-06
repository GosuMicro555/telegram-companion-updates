//go:build desktop

package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"telegram-companion/internal/domain"
	"telegram-companion/internal/repository/sqlite"
	backupservice "telegram-companion/internal/service/backup"
	telegramgotd "telegram-companion/internal/telegram/gotd"
	wailsbindings "telegram-companion/internal/transport/wails"
	"telegram-companion/internal/usecase"
)

func newDesktopBackupRuntimeAt(ctx context.Context, repoDir string, settings wailsbindings.SettingsStore, secrets backupservice.SecretStore) (wailsbindings.BackupRuntime, error) {
	if settings == nil || secrets == nil {
		return wailsbindings.BackupRuntime{}, errors.New("backup settings and secret store are required")
	}
	dataDir := filepath.Join(repoDir, "data")
	databasePath := filepath.Join(dataDir, "app.db")
	db, err := sqlite.OpenUnmigrated(ctx, databasePath)
	if err != nil {
		return wailsbindings.BackupRuntime{}, err
	}
	closeOnError := func(err error) (wailsbindings.BackupRuntime, error) {
		return wailsbindings.BackupRuntime{}, errors.Join(err, db.Close())
	}

	accounts, err := settings.ListAccounts(ctx)
	if err != nil {
		return closeOnError(fmt.Errorf("list backup session files: %w", err))
	}
	sessionFiles := make([]string, 0, len(accounts))
	seen := make(map[string]struct{}, len(accounts))
	for _, account := range accounts {
		path := strings.TrimSpace(account.SessionPath)
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(repoDir, path)
		}
		path = filepath.Clean(path)
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		sessionFiles = append(sessionFiles, path)
	}

	newManager := func(database *sql.DB) (*backupservice.BackupManager, error) {
		applicationState, _ := settings.(backupservice.ConsistentSnapshotSource)
		return backupservice.NewManager(backupservice.Config{
			Database:         database,
			DatabasePath:     databasePath,
			BackupDir:        filepath.Join(dataDir, "backups"),
			SessionFiles:     sessionFiles,
			SessionBarrier:   telegramgotd.ProductionSessionBarrier(),
			ApplicationState: applicationState,
			Secrets:          secrets,
			SchemaVersion:    1,
		})
	}
	manager, err := newManager(db)
	if err != nil {
		return closeOnError(err)
	}
	service := usecase.NewBackupService(manager)
	if err := sqlite.MigrateWithBackup(ctx, db, service.RequirePreMigrationBackup); err != nil {
		return closeOnError(err)
	}
	if err := db.Close(); err != nil {
		return wailsbindings.BackupRuntime{}, fmt.Errorf("close pre-migration database: %w", err)
	}
	db, err = sqlite.Open(ctx, databasePath)
	if err != nil {
		return wailsbindings.BackupRuntime{}, err
	}
	manager, err = newManager(db)
	if err != nil {
		return closeOnError(err)
	}
	service = usecase.NewBackupService(manager)
	return wailsbindings.BackupRuntime{
		Service: service,
		Runner:  usecase.NewBackupRunner(service),
		Close: func(context.Context) error {
			return db.Close()
		},
	}, nil
}

func newDesktopBackupRuntimeForDatabase(repoDir string, settings wailsbindings.SettingsStore, secrets backupservice.SecretStore, db *sql.DB) (wailsbindings.BackupRuntime, error) {
	if settings == nil || secrets == nil || db == nil {
		return wailsbindings.BackupRuntime{}, errors.New("backup root, settings, secret store, and database are required")
	}
	dataDir := filepath.Join(repoDir, "data")
	databasePath := filepath.Join(dataDir, "app.db")
	accounts, err := settings.ListAccounts(context.Background())
	if err != nil {
		return wailsbindings.BackupRuntime{}, fmt.Errorf("list backup session files: %w", err)
	}
	applicationState, _ := settings.(backupservice.ConsistentSnapshotSource)
	manager, err := backupservice.NewManager(backupservice.Config{
		Database: db, DatabasePath: databasePath, BackupDir: filepath.Join(dataDir, "backups"),
		SessionFiles: backupSessionFiles(repoDir, accounts), SessionBarrier: telegramgotd.ProductionSessionBarrier(),
		ApplicationState: applicationState, Secrets: secrets, SchemaVersion: 1,
	})
	if err != nil {
		return wailsbindings.BackupRuntime{}, err
	}
	service := usecase.NewBackupService(manager)
	return wailsbindings.BackupRuntime{Service: service, Runner: usecase.NewBackupRunner(service)}, nil
}

func backupSessionFiles(repoDir string, accounts []domain.Account) []string {
	sessionFiles := make([]string, 0, len(accounts))
	seen := make(map[string]struct{}, len(accounts))
	for _, account := range accounts {
		path := strings.TrimSpace(account.SessionPath)
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(repoDir, path)
		}
		path = filepath.Clean(path)
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		sessionFiles = append(sessionFiles, path)
	}
	return sessionFiles
}

func openProductionDatabase(ctx context.Context, repoDir string, secrets backupservice.SecretStore) (*sql.DB, error) {
	dataDir := filepath.Join(repoDir, "data")
	databasePath := filepath.Join(dataDir, "app.db")
	db, err := sqlite.OpenUnmigrated(ctx, databasePath)
	if err != nil {
		return nil, err
	}
	closeOnError := func(cause error) (*sql.DB, error) {
		return nil, errors.Join(cause, db.Close())
	}
	sessionFiles, err := filepath.Glob(filepath.Join(dataDir, "gotd-import-staging", "*", "*", "account-*", "session.json"))
	if err != nil {
		return closeOnError(err)
	}
	manager, err := backupservice.NewManager(backupservice.Config{
		Database:       db,
		DatabasePath:   databasePath,
		BackupDir:      filepath.Join(dataDir, "backups"),
		SessionFiles:   sessionFiles,
		SessionBarrier: telegramgotd.ProductionSessionBarrier(),
		Secrets:        secrets,
		SchemaVersion:  1,
	})
	if err != nil {
		return closeOnError(err)
	}
	service := usecase.NewBackupService(manager)
	if err := sqlite.MigrateWithBackup(ctx, db, service.RequirePreMigrationBackup); err != nil {
		return closeOnError(err)
	}
	if err := db.Close(); err != nil {
		return nil, fmt.Errorf("close pre-migration sqlite database: %w", err)
	}
	db, err = sqlite.Open(ctx, databasePath)
	if err != nil {
		return nil, err
	}
	return db, nil
}
