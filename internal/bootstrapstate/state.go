package bootstrapstate

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	SchemaVersion = 1
	BundleFile    = "state.tcs"
	markerPath    = "bootstrap-state/imported.json"
)

var (
	ErrInvalidBundle   = errors.New("bootstrap state: invalid bundle")
	ErrUnsafePath      = errors.New("bootstrap state: unsafe path")
	ErrVersionMismatch = errors.New("bootstrap state: application version mismatch")
	ErrSecretNotFound  = errors.New("bootstrap state: secret not found")
)

var allowedSecrets = map[string]struct{}{
	"scout-message-key":               {},
	"outbound-target-key":             {},
	"proxy-credentials-v1":            {},
	"telegram-account-credentials-v1": {},
	"backup-recovery-key-v1":          {},
}

type SecretReader interface {
	Get(context.Context, string) ([]byte, error)
}

type SecretWriter interface {
	Set(context.Context, string, []byte) error
}

type Manifest struct {
	SchemaVersion int               `json:"schema_version"`
	BundleID      string            `json:"bundle_id"`
	AppVersion    string            `json:"app_version"`
	CreatedAt     time.Time         `json:"created_at"`
	Secrets       map[string]string `json:"secrets,omitempty"`
}

type PackConfig struct {
	SourceData string
	OutputPath string
	BundleID   string
	AppVersion string
	License    string
	Key        []byte
	Secrets    SecretReader
	Now        func() time.Time
}

type BundleFootprint struct {
	DataBytes     uint64
	RegularFiles  uint64
	Directories   uint64
	DatabaseBytes uint64
}

type ImportPreflight func(context.Context, BundleFootprint) error

type ImportConfig struct {
	BundlePath string
	TargetRoot string
	BundleID   string
	AppVersion string
	License    string
	Key        []byte
	Secrets    SecretWriter
	Preflight  ImportPreflight
}

func SecretNames() []string {
	names := make([]string, 0, len(allowedSecrets))
	for name := range allowedSecrets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func Pack(ctx context.Context, config PackConfig) error {
	if err := validatePackConfig(config); err != nil {
		return err
	}
	info, err := os.Stat(config.SourceData)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("bootstrap state: source data directory: %w", err)
	}
	if _, err := os.Stat(filepath.Join(config.SourceData, "app.db")); err != nil {
		return fmt.Errorf("bootstrap state: source database: %w", err)
	}
	secrets, err := collectSecrets(ctx, config.Secrets)
	if err != nil {
		return err
	}
	now := time.Now
	if config.Now != nil {
		now = config.Now
	}
	manifest := Manifest{
		SchemaVersion: SchemaVersion,
		BundleID:      strings.TrimSpace(config.BundleID),
		AppVersion:    strings.TrimSpace(config.AppVersion),
		CreatedAt:     now().UTC(),
		Secrets:       secrets,
	}
	if err := os.MkdirAll(filepath.Dir(config.OutputPath), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(config.OutputPath), ".bootstrap-state-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	defer func() { _ = temporary.Close() }()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	var encrypted *encryptedWriter
	if len(config.Key) > 0 {
		encrypted, err = newEncryptedWriterWithKey(temporary, config.Key)
	} else {
		encrypted, err = newEncryptedWriter(temporary, config.License)
	}
	if err != nil {
		return err
	}
	if err := writeArchive(ctx, encrypted, config.SourceData, manifest); err != nil {
		return err
	}
	if err := encrypted.Close(); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, config.OutputPath); err != nil {
		return err
	}
	return nil
}

func Import(ctx context.Context, config ImportConfig) (bool, error) {
	if strings.TrimSpace(config.BundlePath) == "" || strings.TrimSpace(config.TargetRoot) == "" ||
		strings.TrimSpace(config.AppVersion) == "" || !validCredential(config.License, config.Key) || config.Secrets == nil {
		return false, errors.New("bootstrap state: import configuration is incomplete")
	}
	if _, err := os.Stat(filepath.Join(config.TargetRoot, markerPath)); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	inspection, err := scanBundle(ctx, config, "")
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := validateImportedArchive(config, inspection); err != nil {
		return false, err
	}
	if config.Preflight != nil {
		if err := config.Preflight(ctx, inspection.footprint); err != nil {
			return false, err
		}
	}
	if err := os.MkdirAll(config.TargetRoot, 0o700); err != nil {
		return false, err
	}
	temporaryRoot, err := os.MkdirTemp(config.TargetRoot, ".bootstrap-import-*")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(temporaryRoot)
	extraction, err := scanBundle(ctx, config, temporaryRoot)
	if err != nil {
		return false, err
	}
	if extraction.digest != inspection.digest || extraction.footprint != inspection.footprint {
		return false, ErrInvalidBundle
	}
	if err := validateImportedArchive(config, extraction); err != nil {
		return false, err
	}
	if err := installSecrets(ctx, config.Secrets, extraction.manifest.Secrets); err != nil {
		return false, err
	}
	if err := replaceDataAndMark(config.TargetRoot, filepath.Join(temporaryRoot, "data"), extraction.manifest); err != nil {
		return false, err
	}
	return true, nil
}

func scanBundle(ctx context.Context, config ImportConfig, destination string) (archiveScanResult, error) {
	bundle, err := os.Open(config.BundlePath)
	if err != nil {
		return archiveScanResult{}, err
	}
	defer bundle.Close()
	var decrypted *encryptedReader
	if len(config.Key) > 0 {
		decrypted, err = newEncryptedReaderWithKey(bundle, config.Key)
	} else {
		decrypted, err = newEncryptedReader(bundle, config.License)
	}
	if err != nil {
		return archiveScanResult{}, err
	}
	return scanArchive(ctx, decrypted, destination)
}

func validateImportedArchive(config ImportConfig, archive archiveScanResult) error {
	manifest := archive.manifest
	if manifest.SchemaVersion != SchemaVersion || strings.TrimSpace(manifest.BundleID) == "" || !archive.databaseFound {
		return ErrInvalidBundle
	}
	if expected := strings.TrimSpace(config.BundleID); expected != "" && manifest.BundleID != expected {
		return ErrInvalidBundle
	}
	if manifest.AppVersion != strings.TrimSpace(config.AppVersion) {
		return ErrVersionMismatch
	}
	return nil
}

func validatePackConfig(config PackConfig) error {
	if strings.TrimSpace(config.SourceData) == "" || strings.TrimSpace(config.OutputPath) == "" ||
		strings.TrimSpace(config.BundleID) == "" || strings.TrimSpace(config.AppVersion) == "" ||
		!validCredential(config.License, config.Key) {
		return errors.New("bootstrap state: pack configuration is incomplete")
	}
	return nil
}

func validCredential(license string, key []byte) bool {
	hasLicense := strings.TrimSpace(license) != ""
	hasKey := len(key) > 0
	return hasLicense != hasKey && (!hasKey || len(key) == 32)
}

func collectSecrets(ctx context.Context, reader SecretReader) (map[string]string, error) {
	result := make(map[string]string)
	if reader == nil {
		return result, nil
	}
	for _, name := range SecretNames() {
		value, err := reader.Get(ctx, name)
		if errors.Is(err, ErrSecretNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("bootstrap state: read secret %s: %w", name, err)
		}
		if len(value) == 0 {
			return nil, ErrInvalidBundle
		}
		result[name] = base64.StdEncoding.EncodeToString(value)
	}
	return result, nil
}

func installSecrets(ctx context.Context, writer SecretWriter, encoded map[string]string) error {
	for name, value := range encoded {
		if _, ok := allowedSecrets[name]; !ok {
			return ErrInvalidBundle
		}
		decoded, err := base64.StdEncoding.DecodeString(value)
		if err != nil || len(decoded) == 0 {
			return ErrInvalidBundle
		}
		if err := writer.Set(ctx, name, decoded); err != nil {
			clear(decoded)
			return fmt.Errorf("bootstrap state: write secret %s: %w", name, err)
		}
		clear(decoded)
	}
	return nil
}

func replaceDataAndMark(targetRoot, importedData string, manifest Manifest) error {
	targetData := filepath.Join(targetRoot, "data")
	backupData := filepath.Join(targetRoot, ".bootstrap-previous-data")
	if err := os.RemoveAll(backupData); err != nil {
		return err
	}
	hadData := false
	if _, err := os.Stat(targetData); err == nil {
		if err := os.Rename(targetData, backupData); err != nil {
			return err
		}
		hadData = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	rollback := func() {
		_ = os.RemoveAll(targetData)
		if hadData {
			_ = os.Rename(backupData, targetData)
		}
	}
	if err := os.Rename(importedData, targetData); err != nil {
		rollback()
		return err
	}
	marker := filepath.Join(targetRoot, markerPath)
	markerManifest := manifest
	markerManifest.Secrets = nil
	if err := writeJSONAtomically(marker, markerManifest); err != nil {
		rollback()
		return err
	}
	if hadData {
		_ = os.RemoveAll(backupData)
	}
	return nil
}

func writeJSONAtomically(filePath string, value any) error {
	if err := os.MkdirAll(filepath.Dir(filePath), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(filePath), ".marker-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(name, filePath)
}
