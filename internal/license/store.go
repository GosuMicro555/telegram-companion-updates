package license

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	licenseFileName       = "license.tcomplicense"
	maxStoredLicenseBytes = 64 * 1024
)

// FileStore persists a license token in the application's data directory.
type FileStore struct {
	dataRoot string
}

// NewFileStore creates a store rooted under dataRoot.
func NewFileStore(dataRoot string) FileStore {
	return FileStore{dataRoot: dataRoot}
}

// Directory returns the directory containing the persisted license.
func (s FileStore) Directory() string {
	return filepath.Join(s.dataRoot, "license")
}

// Path returns the full path of the persisted license token.
func (s FileStore) Path() string {
	return filepath.Join(s.Directory(), licenseFileName)
}

// Save atomically replaces the stored token using a private, synced temp file.
func (s FileStore) Save(token string) error {
	return s.save(token, syncDirectory)
}

func (s FileStore) save(token string, syncDir func(string) error) error {
	if err := validateTokenEnvelope(token); err != nil {
		return err
	}
	if err := os.MkdirAll(s.Directory(), 0o700); err != nil {
		return fmt.Errorf("license: create store directory: %w", err)
	}
	if err := syncDir(filepath.Dir(s.Directory())); err != nil {
		return fmt.Errorf("license: sync data directory: %w", err)
	}

	temporary, err := os.CreateTemp(s.Directory(), ".license-*")
	if err != nil {
		return fmt.Errorf("license: create temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("license: secure temporary file: %w", err)
	}
	if _, err := temporary.WriteString(token); err != nil {
		temporary.Close()
		return fmt.Errorf("license: write temporary file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("license: sync temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("license: close temporary file: %w", err)
	}
	if err := os.Rename(temporaryPath, s.Path()); err != nil {
		return fmt.Errorf("license: replace stored license: %w", err)
	}
	if err := syncDir(s.Directory()); err != nil {
		return fmt.Errorf("license: sync store directory: %w", err)
	}
	return nil
}

// Load returns the stored token after validating its non-secret envelope.
func (s FileStore) Load() (string, error) {
	file, err := os.Open(s.Path())
	if os.IsNotExist(err) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("license: read stored license: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("license: inspect stored license: %w", err)
	}
	if info.Size() > maxStoredLicenseBytes {
		return "", ErrMalformed
	}
	data, err := io.ReadAll(io.LimitReader(file, maxStoredLicenseBytes+1))
	if err != nil {
		return "", fmt.Errorf("license: read stored license: %w", err)
	}
	if len(data) > maxStoredLicenseBytes {
		return "", ErrMalformed
	}
	token := string(data)
	if strings.HasSuffix(token, "\r\n") {
		token = strings.TrimSuffix(token, "\r\n")
	} else {
		token = strings.TrimSuffix(token, "\n")
	}
	if err := validateTokenEnvelope(token); err != nil {
		return "", err
	}
	return token, nil
}
