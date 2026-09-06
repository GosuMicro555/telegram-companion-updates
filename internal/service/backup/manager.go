package backup

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"telegram-companion/internal/domain"

	moderncsqlite "modernc.org/sqlite"
)

const (
	dailyRetention              = 14
	monthlyRetention            = 3
	ApplicationStateArchivePath = "application-state.bolt"
)

type SessionReadBarrier interface {
	AcquireRead(ctx context.Context) (release func(), err error)
}

type ConsistentSnapshotSource interface {
	Snapshot(ctx context.Context, destination string) error
}

type Config struct {
	Database         *sql.DB
	DatabasePath     string
	BackupDir        string
	SessionFiles     []string
	SessionBarrier   SessionReadBarrier
	ApplicationState ConsistentSnapshotSource
	Secrets          SecretStore
	History          domain.BackupRepository
	SchemaVersion    int
	Now              func() time.Time
}

type BackupManager struct {
	database         *sql.DB
	databasePath     string
	backupDir        string
	sessionFiles     []string
	sessionBarrier   SessionReadBarrier
	applicationState ConsistentSnapshotSource
	secrets          SecretStore
	history          domain.BackupRepository
	schemaVersion    int
	now              func() time.Time
	verify           func(context.Context, string, []byte, string) (Manifest, error)
	publish          func(context.Context, string, string) error
	preparePublish   func(string) (publishTarget, error)
}

func NewManager(config Config) (*BackupManager, error) {
	if config.Database == nil || config.Secrets == nil || config.SessionBarrier == nil {
		return nil, errors.New("database, secret store, and session read barrier are required")
	}
	if config.SchemaVersion <= 0 {
		return nil, errors.New("backup schema version must be positive")
	}
	databasePath, err := filepath.Abs(config.DatabasePath)
	if err != nil || strings.TrimSpace(config.DatabasePath) == "" {
		return nil, errors.New("database path is required")
	}
	backupDir, err := filepath.Abs(config.BackupDir)
	if err != nil || strings.TrimSpace(config.BackupDir) == "" {
		return nil, errors.New("backup directory is required")
	}
	if err := rejectSymlinkComponents(filepath.Dir(backupDir)); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(backupDir); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, ErrUnsafePath
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return nil, fmt.Errorf("create backup directory: %w", err)
	}
	if err := os.Chmod(backupDir, 0o700); err != nil {
		return nil, fmt.Errorf("protect backup directory: %w", err)
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	manager := &BackupManager{
		database: config.Database, databasePath: databasePath, backupDir: backupDir,
		sessionFiles: append([]string(nil), config.SessionFiles...), sessionBarrier: config.SessionBarrier,
		applicationState: config.ApplicationState, secrets: config.Secrets, history: config.History,
		schemaVersion: config.SchemaVersion, now: now,
	}
	manager.verify = manager.verifyArchive
	manager.publish = atomicPublishNoReplace
	manager.preparePublish = prepareAtomicPublish
	return manager, nil
}

func (m *BackupManager) Create(ctx context.Context, kind domain.BackupKind) (record domain.BackupRecord, retErr error) {
	if err := ctx.Err(); err != nil {
		return record, err
	}
	if kind != domain.BackupDaily && kind != domain.BackupMonthly && kind != domain.BackupPreMigration {
		return record, errors.New("unsupported backup kind")
	}
	createdAt := m.now()
	id, err := randomID()
	if err != nil {
		return record, err
	}
	record = domain.BackupRecord{ID: domain.ID(id), Kind: kind, Status: domain.BackupRunning, CreatedAt: createdAt}
	snapshotDir, err := os.MkdirTemp(m.backupDir, ".snapshot-")
	if err != nil {
		return record, fmt.Errorf("create private snapshot: %w", err)
	}
	if err := os.Chmod(snapshotDir, 0o700); err != nil {
		_ = os.RemoveAll(snapshotDir)
		return record, err
	}
	defer os.RemoveAll(snapshotDir)

	databaseSnapshot := filepath.Join(snapshotDir, databaseArchivePath)
	if err := m.onlineDatabaseBackup(ctx, databaseSnapshot); err != nil {
		return m.failedRecord(ctx, record, err)
	}
	if m.applicationState != nil {
		stateSnapshot := filepath.Join(snapshotDir, ApplicationStateArchivePath)
		if err := m.applicationState.Snapshot(ctx, stateSnapshot); err != nil {
			return m.failedRecord(ctx, record, fmt.Errorf("snapshot live application state: %w", err))
		}
		info, err := os.Lstat(stateSnapshot)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return m.failedRecord(ctx, record, ErrUnsafePath)
		}
		if err := os.Chmod(stateSnapshot, 0o600); err != nil {
			return m.failedRecord(ctx, record, err)
		}
	}
	if err := m.snapshotSessions(ctx, snapshotDir); err != nil {
		return m.failedRecord(ctx, record, err)
	}
	schemaHash, err := databaseSchemaHash(ctx, databaseSnapshot)
	if err != nil {
		return m.failedRecord(ctx, record, err)
	}
	manifest := Manifest{ArchiveSchemaVersion: m.schemaVersion, ID: id, Kind: kind, CreatedAt: createdAt, DatabaseSchemaSHA256: schemaHash}
	manifest.Files, err = snapshotManifestFiles(ctx, snapshotDir)
	if err != nil {
		return m.failedRecord(ctx, record, err)
	}

	tarFile, err := os.CreateTemp(m.backupDir, ".plaintext-*.tar")
	if err != nil {
		return m.failedRecord(ctx, record, err)
	}
	tarPath := tarFile.Name()
	defer os.Remove(tarPath)
	if err := tarFile.Chmod(0o600); err != nil {
		_ = tarFile.Close()
		return m.failedRecord(ctx, record, err)
	}
	if err := writeSnapshotTar(ctx, snapshotDir, manifest, tarFile); err != nil {
		_ = tarFile.Close()
		return m.failedRecord(ctx, record, err)
	}
	if err := tarFile.Sync(); err != nil {
		_ = tarFile.Close()
		return m.failedRecord(ctx, record, err)
	}
	if _, err := tarFile.Seek(0, io.SeekStart); err != nil {
		_ = tarFile.Close()
		return m.failedRecord(ctx, record, err)
	}
	key, err := m.secrets.GetOrCreate(ctx, recoveryKeyName, recoveryKeyBytes)
	if err != nil {
		_ = tarFile.Close()
		return m.failedRecord(ctx, record, err)
	}
	defer clear(key)
	publishPath := filepath.Join(m.backupDir, ".publish-"+id)
	if err := encryptArchive(ctx, key, publishPath, tarFile); err != nil {
		_ = tarFile.Close()
		return m.failedRecord(ctx, record, err)
	}
	if err := tarFile.Close(); err != nil {
		os.Remove(publishPath)
		return m.failedRecord(ctx, record, err)
	}
	defer os.Remove(publishPath)
	verified, err := m.verify(ctx, publishPath, key, schemaHash)
	if err != nil {
		return m.failedRecord(ctx, record, fmt.Errorf("verify backup: %w", err))
	}
	if verified.ID != id || verified.Kind != kind || verified.ArchiveSchemaVersion != m.schemaVersion {
		return m.failedRecord(ctx, record, errors.New("verified manifest does not match backup request"))
	}
	if err := ctx.Err(); err != nil {
		return m.failedRecord(ctx, record, err)
	}
	finalPath := filepath.Join(m.backupDir, archiveName(kind, createdAt, id))
	if err := m.publish(ctx, publishPath, finalPath); err != nil {
		return m.failedRecord(ctx, record, fmt.Errorf("publish verified backup: %w", err))
	}
	if err := syncDirectory(m.backupDir); err != nil {
		_ = os.Remove(finalPath)
		_ = syncDirectory(m.backupDir)
		return m.failedRecord(ctx, record, err)
	}
	finalizeCtx := context.WithoutCancel(ctx)
	archiveInfo, err := os.Stat(finalPath)
	if err != nil {
		_ = os.Remove(finalPath)
		return m.failedRecord(ctx, record, err)
	}
	archiveHash, err := hashFile(finalizeCtx, finalPath)
	if err != nil {
		_ = os.Remove(finalPath)
		return m.failedRecord(ctx, record, err)
	}
	verifiedAt := m.now()
	record.ArchivePath = finalPath
	record.SizeBytes = archiveInfo.Size()
	record.SHA256 = archiveHash
	record.Status = domain.BackupVerified
	record.VerifiedAt = &verifiedAt
	if m.history != nil {
		if err := m.history.Save(finalizeCtx, record); err != nil {
			return record, fmt.Errorf("save verified backup history: %w", err)
		}
	}
	if err := m.rotate(finalizeCtx, kind, key); err != nil {
		return record, fmt.Errorf("rotate verified backups: %w", err)
	}
	return record, nil
}

func (m *BackupManager) Restore(ctx context.Context, archive, destination string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	archivePath, err := filepath.Abs(archive)
	if err != nil || archive == "" || archivePath != filepath.Clean(archive) {
		return ErrUnsafePath
	}
	if err := rejectSymlinkComponents(filepath.Dir(archivePath)); err != nil {
		return err
	}
	if _, err := openRegularNoSymlinkAndClose(archivePath); err != nil {
		return err
	}
	destinationPath, err := filepath.Abs(destination)
	if err != nil || destination == "" || destinationPath != filepath.Clean(destination) || destinationPath == m.databasePath {
		return ErrUnsafePath
	}
	if err := rejectSymlinkComponents(filepath.Dir(destinationPath)); err != nil {
		return err
	}
	if _, err := os.Lstat(destinationPath); err == nil {
		return os.ErrExist
	} else if !os.IsNotExist(err) {
		return err
	}
	target, err := m.preparePublish(destinationPath)
	if err != nil {
		return err
	}
	defer target.Close()
	currentSchema, err := schemaHashFromDB(ctx, m.database)
	if err != nil {
		return fmt.Errorf("read live database schema: %w", err)
	}
	key, err := m.secrets.GetOrCreate(ctx, recoveryKeyName, recoveryKeyBytes)
	if err != nil {
		return err
	}
	defer clear(key)
	temporaryName, temporary, err := target.CreateTempDir(".restore-")
	if err != nil {
		return fmt.Errorf("create restore directory: %w", err)
	}
	defer func() {
		_ = temporary.Close()
		_ = target.RemoveTemp(temporaryName)
	}()
	if _, err := m.verifyArchiveInto(ctx, archivePath, key, currentSchema, temporary); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := target.Publish(ctx, temporaryName); err != nil {
		return fmt.Errorf("publish restore: %w", err)
	}
	return nil
}

func (m *BackupManager) ExportRecoveryKey(ctx context.Context, selectedPath string) error {
	return ExportRecoveryKey(ctx, m.secrets, selectedPath)
}

func (m *BackupManager) HasVerifiedMonthly(ctx context.Context, year int, month time.Month, location *time.Location) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if location == nil {
		location = time.Local
	}
	entries, err := os.ReadDir(m.backupDir)
	if err != nil {
		return false, err
	}
	key, err := m.secrets.GetOrCreate(ctx, recoveryKeyName, recoveryKeyBytes)
	if err != nil {
		return false, err
	}
	defer clear(key)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if entry.IsDir() || kindFromArchiveName(entry.Name()) != domain.BackupMonthly {
			continue
		}
		manifest, err := m.verifyArchive(ctx, filepath.Join(m.backupDir, entry.Name()), key, "")
		if err != nil {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			continue
		}
		local := manifest.CreatedAt.In(location)
		if local.Year() == year && local.Month() == month {
			return true, nil
		}
	}
	return false, nil
}

func (m *BackupManager) onlineDatabaseBackup(ctx context.Context, destination string) error {
	connection, err := m.database.Conn(ctx)
	if err != nil {
		return fmt.Errorf("get sqlite backup connection: %w", err)
	}
	defer connection.Close()
	return connection.Raw(func(driverConnection any) error {
		backuper, ok := driverConnection.(interface {
			NewBackup(string) (*moderncsqlite.Backup, error)
		})
		if !ok {
			return errors.New("sqlite driver does not support online backup")
		}
		backup, err := backuper.NewBackup(destination)
		if err != nil {
			return fmt.Errorf("initialize sqlite online backup: %w", err)
		}
		finished := false
		defer func() {
			if !finished {
				_ = backup.Finish()
			}
		}()
		for more := true; more; {
			if err := ctx.Err(); err != nil {
				return err
			}
			more, err = backup.Step(128)
			if err != nil {
				return fmt.Errorf("copy sqlite backup pages: %w", err)
			}
		}
		finished = true
		if err := backup.Finish(); err != nil {
			return fmt.Errorf("finish sqlite online backup: %w", err)
		}
		return os.Chmod(destination, 0o600)
	})
}

func (m *BackupManager) snapshotSessions(ctx context.Context, snapshotDir string) error {
	release, err := m.sessionBarrier.AcquireRead(ctx)
	if err != nil {
		return err
	}
	defer release()
	for index, sourcePath := range m.sessionFiles {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !filepath.IsAbs(sourcePath) || filepath.Clean(sourcePath) != sourcePath {
			return ErrUnsafePath
		}
		if err := rejectSymlinkComponents(filepath.Dir(sourcePath)); err != nil {
			return err
		}
		input, err := openRegularNoSymlink(sourcePath)
		if err != nil {
			return err
		}
		info, err := input.Stat()
		if err != nil {
			input.Close()
			return err
		}
		if info.Mode().Perm()&0o077 != 0 {
			input.Close()
			return ErrUnsafePath
		}
		destinationDir := filepath.Join(snapshotDir, "sessions")
		if err := os.MkdirAll(destinationDir, 0o700); err != nil {
			input.Close()
			return err
		}
		name := fmt.Sprintf("%04d-%s", index, filepath.Base(sourcePath))
		output, err := os.OpenFile(filepath.Join(destinationDir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			input.Close()
			return err
		}
		_, copyErr := copyContext(ctx, output, input)
		syncErr := output.Sync()
		closeErr := output.Close()
		inputCloseErr := input.Close()
		if copyErr != nil || syncErr != nil || closeErr != nil || inputCloseErr != nil {
			return errors.Join(copyErr, syncErr, closeErr, inputCloseErr)
		}
		after, err := os.Lstat(sourcePath)
		if err != nil || !os.SameFile(info, after) || after.Mode()&os.ModeSymlink != 0 {
			return ErrUnsafePath
		}
		if err := rejectSymlinkComponents(filepath.Dir(sourcePath)); err != nil {
			return err
		}
	}
	return nil
}

func snapshotManifestFiles(ctx context.Context, root string) ([]ManifestFile, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return ErrUnsafePath
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return ErrUnsafePath
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	files := make([]ManifestFile, 0, len(paths))
	for _, path := range paths {
		file, err := fileManifest(ctx, root, path)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}

func (m *BackupManager) verifyArchive(ctx context.Context, archivePath string, key []byte, expectedSchema string) (Manifest, error) {
	temporary, err := os.MkdirTemp(m.backupDir, ".verify-")
	if err != nil {
		return Manifest{}, err
	}
	if err := os.Chmod(temporary, 0o700); err != nil {
		os.RemoveAll(temporary)
		return Manifest{}, err
	}
	defer os.RemoveAll(temporary)
	workspace, err := openRestoreWorkspace(temporary)
	if err != nil {
		return Manifest{}, err
	}
	defer workspace.Close()
	return m.verifyArchiveInto(ctx, archivePath, key, expectedSchema, workspace)
}

func (m *BackupManager) verifyArchiveInto(ctx context.Context, archivePath string, key []byte, expectedSchema string, destination restoreWorkspace) (Manifest, error) {
	id, err := randomID()
	if err != nil {
		return Manifest{}, err
	}
	plainName := ".decrypted-" + id + ".tar"
	plain, err := destination.CreateFile(plainName)
	if err != nil {
		return Manifest{}, err
	}
	if err := decryptArchive(ctx, key, archivePath, plain); err != nil {
		_ = plain.Close()
		_ = destination.RemoveFile(plainName)
		return Manifest{}, err
	}
	if _, err := plain.Seek(0, io.SeekStart); err != nil {
		_ = plain.Close()
		_ = destination.RemoveFile(plainName)
		return Manifest{}, err
	}
	extracted, err := extractTarWorkspace(ctx, plain, destination)
	if err != nil {
		_ = plain.Close()
		_ = destination.RemoveFile(plainName)
		return Manifest{}, err
	}
	if err := plain.Close(); err != nil {
		_ = destination.RemoveFile(plainName)
		return Manifest{}, err
	}
	if err := destination.RemoveFile(plainName); err != nil {
		return Manifest{}, err
	}
	manifestFile, err := destination.OpenFile(manifestArchivePath)
	if err != nil {
		return Manifest{}, err
	}
	decoder := json.NewDecoder(io.LimitReader(manifestFile, 1<<20))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	decodeErr := decoder.Decode(&manifest)
	var extra any
	extraErr := decoder.Decode(&extra)
	closeErr := manifestFile.Close()
	if decodeErr != nil || !errors.Is(extraErr, io.EOF) || closeErr != nil {
		return Manifest{}, errors.Join(decodeErr, extraErr, closeErr)
	}
	if manifest.ArchiveSchemaVersion != m.schemaVersion || manifest.ID == "" || manifest.CreatedAt.IsZero() {
		return Manifest{}, errors.New("incompatible backup manifest")
	}
	if err := verifyManifestWorkspace(ctx, destination, extracted, manifest); err != nil {
		return Manifest{}, err
	}
	databaseFile, err := destination.OpenFile(databaseArchivePath)
	if err != nil {
		return Manifest{}, err
	}
	if err := verifySQLite(ctx, descriptorFilePath(databaseFile.Fd()), manifest.DatabaseSchemaSHA256); err != nil {
		databaseFile.Close()
		return Manifest{}, err
	}
	if err := databaseFile.Close(); err != nil {
		return Manifest{}, err
	}
	if expectedSchema != "" && manifest.DatabaseSchemaSHA256 != expectedSchema && manifest.Kind != domain.BackupPreMigration {
		return Manifest{}, errors.New("backup database schema is incompatible")
	}
	return manifest, nil
}

func verifySQLite(ctx context.Context, path, expectedSchema string) error {
	query := url.Values{"mode": {"ro"}, "immutable": {"1"}}
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: query.Encode()}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `PRAGMA integrity_check`)
	if err != nil {
		return fmt.Errorf("run sqlite integrity check: %w", err)
	}
	defer rows.Close()
	var results []string
	for rows.Next() {
		var result string
		if err := rows.Scan(&result); err != nil {
			return err
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(results) != 1 || results[0] != "ok" {
		return fmt.Errorf("sqlite integrity check failed: %s", strings.Join(results, "; "))
	}
	schema, err := schemaHashFromDB(ctx, db)
	if err != nil {
		return err
	}
	if schema != expectedSchema {
		return errors.New("sqlite schema hash mismatch")
	}
	return nil
}

func databaseSchemaHash(ctx context.Context, path string) (string, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return "", err
	}
	defer db.Close()
	return schemaHashFromDB(ctx, db)
}

func schemaHashFromDB(ctx context.Context, db *sql.DB) (string, error) {
	rows, err := db.QueryContext(ctx, `SELECT type, name, tbl_name, COALESCE(sql, '') FROM sqlite_master ORDER BY type, name, tbl_name`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	hash := sha256.New()
	for rows.Next() {
		var objectType, name, table, statement string
		if err := rows.Scan(&objectType, &name, &table, &statement); err != nil {
			return "", err
		}
		for _, value := range []string{objectType, name, table, statement} {
			binaryWriteString(hash, value)
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	var userVersion int64
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&userVersion); err != nil {
		return "", err
	}
	binaryWriteString(hash, fmt.Sprintf("user_version:%d", userVersion))
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func binaryWriteString(writer io.Writer, value string) {
	var length [8]byte
	for i := range length {
		length[len(length)-1-i] = byte(uint64(len(value)) >> (8 * i))
	}
	_, _ = writer.Write(length[:])
	_, _ = writer.Write([]byte(value))
}

func (m *BackupManager) rotate(ctx context.Context, kind domain.BackupKind, key []byte) error {
	keep := 0
	switch kind {
	case domain.BackupDaily:
		keep = dailyRetention
	case domain.BackupMonthly:
		keep = monthlyRetention
	default:
		return nil
	}
	entries, err := os.ReadDir(m.backupDir)
	if err != nil {
		return err
	}
	type verifiedArchive struct {
		name      string
		createdAt time.Time
	}
	var verified []verifiedArchive
	for _, entry := range entries {
		if entry.IsDir() || kindFromArchiveName(entry.Name()) != kind {
			continue
		}
		manifest, err := m.verifyArchive(ctx, filepath.Join(m.backupDir, entry.Name()), key, "")
		if err != nil || manifest.Kind != kind {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		verified = append(verified, verifiedArchive{name: entry.Name(), createdAt: manifest.CreatedAt})
	}
	sort.Slice(verified, func(i, j int) bool {
		if verified[i].createdAt.Equal(verified[j].createdAt) {
			return verified[i].name > verified[j].name
		}
		return verified[i].createdAt.After(verified[j].createdAt)
	})
	for _, archive := range verified[minimum(keep, len(verified)):] {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(m.backupDir, archive.name)
		if err := ensureWithin(m.backupDir, path); err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return ErrUnsafePath
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return syncDirectory(m.backupDir)
}

func archiveName(kind domain.BackupKind, createdAt time.Time, id string) string {
	return fmt.Sprintf("%s-%s-%s.scout-backup", kind, createdAt.UTC().Format("20060102T150405.000000000Z"), id)
}

func kindFromArchiveName(name string) domain.BackupKind {
	for _, kind := range []domain.BackupKind{domain.BackupDaily, domain.BackupMonthly, domain.BackupPreMigration} {
		if strings.HasPrefix(name, string(kind)+"-") && strings.HasSuffix(name, ".scout-backup") {
			return kind
		}
	}
	return ""
}

func (m *BackupManager) failedRecord(ctx context.Context, record domain.BackupRecord, err error) (domain.BackupRecord, error) {
	record.Status = domain.BackupError
	record.Error = err.Error()
	if m.history != nil {
		if saveErr := m.history.Save(context.WithoutCancel(ctx), record); saveErr != nil {
			err = errors.Join(err, saveErr)
		}
	}
	return record, err
}

func randomID() (string, error) {
	value := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func hashFile(ctx context.Context, path string) (string, error) {
	file, err := openRegularNoSymlink(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := copyContext(ctx, hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func rejectSymlinkComponents(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	abs, err = normalizeTrustedSystemPath(abs)
	if err != nil {
		return err
	}
	current := filepath.VolumeName(abs) + string(os.PathSeparator)
	rest := strings.TrimPrefix(abs, current)
	for _, component := range strings.Split(rest, string(os.PathSeparator)) {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return ErrUnsafePath
		}
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func openRegularNoSymlinkAndClose(path string) (struct{}, error) {
	file, err := openRegularNoSymlink(path)
	if err != nil {
		return struct{}{}, err
	}
	return struct{}{}, file.Close()
}

func minimum(left, right int) int {
	if left < right {
		return left
	}
	return right
}
