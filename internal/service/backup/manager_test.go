package backup

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"telegram-companion/internal/domain"

	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

type fixedSecrets struct{ key []byte }

func (s fixedSecrets) GetOrCreate(context.Context, string, int) ([]byte, error) {
	return append([]byte(nil), s.key...), nil
}

type trackingBarrier struct {
	mu      sync.Mutex
	readers int
	locks   int
}

type blockingBarrier struct {
	entered chan struct{}
	release chan struct{}
}

type swappingPublishTarget struct {
	publishTarget
	before func()
}

type createSwappingPublishTarget struct {
	publishTarget
	selectedParent  string
	displacedParent string
	t               *testing.T
}

func (t *createSwappingPublishTarget) CreateTempDir(prefix string) (string, restoreWorkspace, error) {
	name, workspace, err := t.publishTarget.CreateTempDir(prefix)
	if err != nil {
		return "", nil, err
	}
	require.NoError(t.t, os.Rename(t.selectedParent, t.displacedParent))
	require.NoError(t.t, os.Mkdir(t.selectedParent, 0o700))
	require.NoError(t.t, os.Mkdir(filepath.Join(t.selectedParent, name), 0o700))
	return name, workspace, nil
}

func (t *swappingPublishTarget) Publish(ctx context.Context, sourceName string) error {
	t.before()
	return t.publishTarget.Publish(ctx, sourceName)
}

func (b *blockingBarrier) AcquireRead(ctx context.Context) (func(), error) {
	close(b.entered)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-b.release:
		return func() {}, nil
	}
}

func (b *trackingBarrier) AcquireRead(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b.mu.Lock()
	b.readers++
	b.locks++
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		b.readers--
		b.mu.Unlock()
	}, nil
}

func TestVerifiedBackupRestoresDatabaseAndSessionsAtomically(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "live", "app.sqlite")
	db := openBackupTestDB(t, dbPath)
	_, err := db.Exec(`INSERT INTO entries(value) VALUES ('before')`)
	require.NoError(t, err)

	sessionDir := filepath.Join(t.TempDir(), "sessions")
	require.NoError(t, os.MkdirAll(sessionDir, 0o700))
	session := filepath.Join(sessionDir, "session.json")
	require.NoError(t, os.WriteFile(session, []byte("session-state"), 0o600))
	barrier := &trackingBarrier{}
	manager := newBackupTestManager(t, db, dbPath, []string{session}, barrier)

	record, err := manager.Create(ctx, domain.BackupDaily)
	require.NoError(t, err)
	require.Equal(t, domain.BackupVerified, record.Status)
	require.NotEmpty(t, record.SHA256)
	info, err := os.Stat(record.ArchivePath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	require.Equal(t, 1, barrier.locks)
	require.Zero(t, barrier.readers)

	destination := filepath.Join(t.TempDir(), "restored")
	require.NoError(t, manager.Restore(ctx, record.ArchivePath, destination))
	restoredDB, err := sql.Open("sqlite", filepath.Join(destination, databaseArchivePath))
	require.NoError(t, err)
	defer restoredDB.Close()
	var value string
	require.NoError(t, restoredDB.QueryRow(`SELECT value FROM entries`).Scan(&value))
	require.Equal(t, "before", value)
	require.FileExists(t, filepath.Join(destination, "sessions", "0000-session.json"))

	require.Error(t, manager.Restore(ctx, record.ArchivePath, destination), "restore must not overwrite")
	require.NoError(t, os.WriteFile(filepath.Join(destination, "sentinel"), []byte("keep"), 0o600))
	require.FileExists(t, filepath.Join(destination, "sentinel"))
}

func TestCreateRejectsUnsafeSessionAndRestorePaths(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "app.sqlite")
	db := openBackupTestDB(t, dbPath)
	realSession := filepath.Join(t.TempDir(), "session.json")
	require.NoError(t, os.WriteFile(realSession, []byte("secret"), 0o600))
	linked := filepath.Join(t.TempDir(), "linked-session")
	require.NoError(t, os.Symlink(realSession, linked))

	manager := newBackupTestManager(t, db, dbPath, []string{linked}, &trackingBarrier{})
	_, err := manager.Create(context.Background(), domain.BackupDaily)
	require.ErrorIs(t, err, ErrUnsafePath)

	manager = newBackupTestManager(t, db, dbPath, nil, &trackingBarrier{})
	record, err := manager.Create(context.Background(), domain.BackupDaily)
	require.NoError(t, err)
	require.ErrorIs(t, manager.Restore(context.Background(), record.ArchivePath, "relative/path"), ErrUnsafePath)
	require.ErrorIs(t, manager.Restore(context.Background(), record.ArchivePath, dbPath), ErrUnsafePath)
}

func TestCreateAndRestoreRejectSymlinkedParentComponents(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "app.sqlite")
	db := openBackupTestDB(t, dbPath)
	realDir := filepath.Join(t.TempDir(), "real")
	require.NoError(t, os.MkdirAll(realDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(realDir, "session.json"), []byte("secret"), 0o600))
	linkedDir := filepath.Join(t.TempDir(), "linked")
	require.NoError(t, os.Symlink(realDir, linkedDir))

	manager := newBackupTestManager(t, db, dbPath, []string{filepath.Join(linkedDir, "session.json")}, &trackingBarrier{})
	_, err := manager.Create(context.Background(), domain.BackupDaily)
	require.ErrorIs(t, err, ErrUnsafePath)

	manager = newBackupTestManager(t, db, dbPath, nil, &trackingBarrier{})
	record, err := manager.Create(context.Background(), domain.BackupDaily)
	require.NoError(t, err)
	archiveLinkDir := filepath.Join(t.TempDir(), "archive-link")
	require.NoError(t, os.Symlink(filepath.Dir(record.ArchivePath), archiveLinkDir))
	require.ErrorIs(t,
		manager.Restore(context.Background(), filepath.Join(archiveLinkDir, filepath.Base(record.ArchivePath)), filepath.Join(t.TempDir(), "restore")),
		ErrUnsafePath,
	)
}

func TestMonthlyLookupHonorsCancellation(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "app.sqlite")
	db := openBackupTestDB(t, dbPath)
	manager := newBackupTestManager(t, db, dbPath, nil, &trackingBarrier{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := manager.HasVerifiedMonthly(ctx, 2026, time.July, time.Local)
	require.ErrorIs(t, err, context.Canceled)
}

func TestRetentionRunsOnlyAfterSuccessfulVerification(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "app.sqlite")
	db := openBackupTestDB(t, dbPath)
	now := time.Date(2026, 1, 1, 3, 0, 0, 0, time.Local)
	manager := newBackupTestManager(t, db, dbPath, nil, &trackingBarrier{})
	manager.now = func() time.Time { return now }

	for day := range 16 {
		if day > 0 {
			now = now.AddDate(0, 0, 1)
		}
		_, err := manager.Create(context.Background(), domain.BackupDaily)
		require.NoError(t, err)
	}
	require.Len(t, archiveNames(t, manager.backupDir, domain.BackupDaily), dailyRetention)

	before := archiveNames(t, manager.backupDir, domain.BackupDaily)
	manager.verify = func(context.Context, string, []byte, string) (Manifest, error) {
		return Manifest{}, errors.New("injected verification failure")
	}
	now = now.AddDate(0, 0, 1)
	_, err := manager.Create(context.Background(), domain.BackupDaily)
	require.Error(t, err)
	require.Equal(t, before, archiveNames(t, manager.backupDir, domain.BackupDaily))
}

func TestRetentionCountsOnlyCryptographicallyVerifiedArchives(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "app.sqlite")
	db := openBackupTestDB(t, dbPath)
	now := time.Date(2026, 1, 1, 3, 0, 0, 0, time.UTC)
	manager := newBackupTestManager(t, db, dbPath, nil, &trackingBarrier{})
	manager.now = func() time.Time { return now }
	for range dailyRetention {
		_, err := manager.Create(context.Background(), domain.BackupDaily)
		require.NoError(t, err)
		now = now.AddDate(0, 0, 1)
	}

	corrupt := filepath.Join(manager.backupDir, "daily-99991231T030000.000000000Z-corrupt.scout-backup")
	require.NoError(t, os.WriteFile(corrupt, []byte("not an encrypted archive"), 0o600))
	_, err := manager.Create(context.Background(), domain.BackupDaily)
	require.NoError(t, err)
	require.Len(t, verifiedArchives(t, manager, domain.BackupDaily), dailyRetention)
	require.FileExists(t, corrupt)
}

func TestSessionBarrierWaitIsCancellable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "app.sqlite")
	db := openBackupTestDB(t, dbPath)
	barrier := &blockingBarrier{entered: make(chan struct{}), release: make(chan struct{})}
	manager := newBackupTestManager(t, db, dbPath, nil, barrier)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := manager.Create(ctx, domain.BackupDaily)
		result <- err
	}()
	<-barrier.entered
	cancel()
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(250 * time.Millisecond):
		close(barrier.release)
		<-result
		t.Fatal("backup did not cancel while waiting for the session read barrier")
	}
}

func TestSessionBarrierReadWaitIsCancellable(t *testing.T) {
	barrier := NewSessionBarrier()
	releaseWrite, err := barrier.AcquireWrite(context.Background())
	require.NoError(t, err)
	defer releaseWrite()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = barrier.AcquireRead(ctx)
	require.ErrorIs(t, err, context.Canceled)
}

func TestCreateCancellationAfterVerifyDoesNotPublish(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "app.sqlite")
	db := openBackupTestDB(t, dbPath)
	manager := newBackupTestManager(t, db, dbPath, nil, &trackingBarrier{})
	ctx, cancel := context.WithCancel(context.Background())
	originalVerify := manager.verify
	manager.verify = func(ctx context.Context, path string, key []byte, schema string) (Manifest, error) {
		manifest, err := originalVerify(ctx, path, key, schema)
		cancel()
		return manifest, err
	}

	_, err := manager.Create(ctx, domain.BackupDaily)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, archiveNames(t, manager.backupDir, domain.BackupDaily))
}

func TestAtomicPublishNoReplacePreservesExistingDestination(t *testing.T) {
	parent := t.TempDir()
	source := filepath.Join(parent, "source")
	destination := filepath.Join(parent, "destination")
	require.NoError(t, os.Mkdir(source, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(source, "restored"), []byte("new"), 0o600))
	require.NoError(t, os.Mkdir(destination, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(destination, "sentinel"), []byte("old"), 0o600))

	err := atomicPublishNoReplace(context.Background(), source, destination)
	require.ErrorIs(t, err, os.ErrExist)
	require.FileExists(t, filepath.Join(source, "restored"))
	require.FileExists(t, filepath.Join(destination, "sentinel"))
}

func TestRestoreCancellationAfterVerifyDoesNotPublish(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "app.sqlite")
	db := openBackupTestDB(t, dbPath)
	manager := newBackupTestManager(t, db, dbPath, nil, &trackingBarrier{})
	record, err := manager.Create(context.Background(), domain.BackupDaily)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	originalPrepare := manager.preparePublish
	manager.preparePublish = func(path string) (publishTarget, error) {
		target, err := originalPrepare(path)
		if err != nil {
			return nil, err
		}
		return &swappingPublishTarget{publishTarget: target, before: cancel}, nil
	}
	destination := filepath.Join(t.TempDir(), "restore")
	err = manager.Restore(ctx, record.ArchivePath, destination)
	require.ErrorIs(t, err, context.Canceled)
	require.NoFileExists(t, destination)
}

func TestRestoreParentSwapCannotRedirectDescriptorRelativePublish(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "app.sqlite")
	db := openBackupTestDB(t, dbPath)
	manager := newBackupTestManager(t, db, dbPath, nil, &trackingBarrier{})
	record, err := manager.Create(context.Background(), domain.BackupDaily)
	require.NoError(t, err)

	root := t.TempDir()
	selectedParent := filepath.Join(root, "selected")
	displacedParent := filepath.Join(root, "displaced")
	require.NoError(t, os.Mkdir(selectedParent, 0o700))
	destination := filepath.Join(selectedParent, "restore")
	originalPrepare := manager.preparePublish
	manager.preparePublish = func(path string) (publishTarget, error) {
		target, err := originalPrepare(path)
		if err != nil {
			return nil, err
		}
		return &swappingPublishTarget{publishTarget: target, before: func() {
			require.NoError(t, os.Rename(selectedParent, displacedParent))
			require.NoError(t, os.Mkdir(selectedParent, 0o700))
		}}, nil
	}

	err = manager.Restore(context.Background(), record.ArchivePath, destination)
	require.ErrorIs(t, err, ErrUnsafePath)
	require.NoDirExists(t, filepath.Join(selectedParent, "restore"))
	require.NoDirExists(t, filepath.Join(displacedParent, "restore"))
	entries, err := os.ReadDir(displacedParent)
	require.NoError(t, err)
	for _, entry := range entries {
		require.NotContains(t, entry.Name(), ".restore-", "private plaintext restore temp must be removed")
	}
}

func TestRestoreParentSwapBeforeExtractionCannotRedirectPlaintext(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "app.sqlite")
	db := openBackupTestDB(t, dbPath)
	manager := newBackupTestManager(t, db, dbPath, nil, &trackingBarrier{})
	record, err := manager.Create(context.Background(), domain.BackupDaily)
	require.NoError(t, err)

	root := t.TempDir()
	selectedParent := filepath.Join(root, "selected")
	displacedParent := filepath.Join(root, "displaced")
	require.NoError(t, os.Mkdir(selectedParent, 0o700))
	destination := filepath.Join(selectedParent, "restore")
	originalPrepare := manager.preparePublish
	manager.preparePublish = func(path string) (publishTarget, error) {
		target, err := originalPrepare(path)
		if err != nil {
			return nil, err
		}
		return &createSwappingPublishTarget{
			publishTarget: target, selectedParent: selectedParent,
			displacedParent: displacedParent, t: t,
		}, nil
	}

	err = manager.Restore(context.Background(), record.ArchivePath, destination)
	require.ErrorIs(t, err, ErrUnsafePath)
	requireNoRegularFiles(t, selectedParent)
	requireNoRegularFiles(t, displacedParent)
}

func requireNoRegularFiles(t *testing.T, root string) {
	t.Helper()
	require.NoError(t, filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			t.Errorf("plaintext file retained after parent swap: %s", path)
		}
		return nil
	}))
}

func TestMonthlyRetentionKeepsThreeVerifiedArchives(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "app.sqlite")
	db := openBackupTestDB(t, dbPath)
	now := time.Date(2026, 1, 1, 3, 0, 0, 0, time.Local)
	manager := newBackupTestManager(t, db, dbPath, nil, &trackingBarrier{})
	manager.now = func() time.Time { return now }
	for range 5 {
		_, err := manager.Create(context.Background(), domain.BackupMonthly)
		require.NoError(t, err)
		now = now.AddDate(0, 1, 0)
	}
	require.Len(t, archiveNames(t, manager.backupDir, domain.BackupMonthly), monthlyRetention)
}

func openBackupTestDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(`CREATE TABLE entries(id INTEGER PRIMARY KEY, value TEXT NOT NULL)`)
	require.NoError(t, err)
	return db
}

func newBackupTestManager(t *testing.T, db *sql.DB, dbPath string, sessions []string, barrier SessionReadBarrier) *BackupManager {
	t.Helper()
	manager, err := NewManager(Config{
		Database:       db,
		DatabasePath:   dbPath,
		BackupDir:      filepath.Join(t.TempDir(), "backups"),
		SessionFiles:   sessions,
		SessionBarrier: barrier,
		Secrets:        fixedSecrets{key: bytes.Repeat([]byte{0x33}, recoveryKeyBytes)},
		SchemaVersion:  1,
	})
	require.NoError(t, err)
	return manager
}

func archiveNames(t *testing.T, dir string, kind domain.BackupKind) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && kindFromArchiveName(entry.Name()) == kind {
			names = append(names, entry.Name())
		}
	}
	return names
}

func verifiedArchives(t *testing.T, manager *BackupManager, kind domain.BackupKind) []string {
	t.Helper()
	key := bytes.Repeat([]byte{0x33}, recoveryKeyBytes)
	var names []string
	for _, name := range archiveNames(t, manager.backupDir, kind) {
		manifest, err := manager.verifyArchive(context.Background(), filepath.Join(manager.backupDir, name), key, "")
		if err == nil && manifest.Kind == kind {
			names = append(names, name)
		}
	}
	return names
}
