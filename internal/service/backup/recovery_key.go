package backup

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const recoveryKeyName = "backup-recovery-key-v1"

type SecretStore interface {
	GetOrCreate(ctx context.Context, name string, bytes int) ([]byte, error)
}

// ExportRecoveryKey performs the only supported plaintext recovery-key export.
// Callers must invoke it from an explicit user command and supply the selected path.
func ExportRecoveryKey(ctx context.Context, store SecretStore, selectedPath string) (retErr error) {
	if store == nil {
		return errors.New("secret store is required")
	}
	if err := validateSelectedFilePath(selectedPath); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	key, err := store.GetOrCreate(ctx, recoveryKeyName, recoveryKeyBytes)
	if err != nil {
		return fmt.Errorf("load recovery key: %w", err)
	}
	defer clear(key)
	if len(key) != recoveryKeyBytes {
		return errors.New("invalid recovery key length")
	}
	output, err := os.OpenFile(selectedPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create recovery key export: %w", err)
	}
	defer func() {
		if closeErr := output.Close(); closeErr != nil {
			retErr = errors.Join(retErr, closeErr)
		}
		if retErr != nil {
			_ = os.Remove(selectedPath)
		}
	}()
	encoded := base64.StdEncoding.EncodeToString(key) + "\n"
	if err := writeAll(output, []byte(encoded)); err != nil {
		return fmt.Errorf("write recovery key export: %w", err)
	}
	if err := output.Sync(); err != nil {
		return fmt.Errorf("sync recovery key export: %w", err)
	}
	return syncDirectory(filepath.Dir(selectedPath))
}

func validateSelectedFilePath(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return ErrUnsafePath
	}
	if err := rejectSymlinkComponents(filepath.Dir(path)); err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		return os.ErrExist
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}
