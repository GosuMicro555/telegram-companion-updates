package backup

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExportRecoveryKeyRequiresExplicitSafeOwnerOnlyDestination(t *testing.T) {
	store := fixedSecrets{key: bytes.Repeat([]byte{0x61}, recoveryKeyBytes)}
	destination := filepath.Join(t.TempDir(), "recovery-key.txt")
	require.NoError(t, ExportRecoveryKey(context.Background(), store, destination))
	info, err := os.Stat(destination)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	data, err := os.ReadFile(destination)
	require.NoError(t, err)
	require.NotContains(t, string(data), "[")
	require.Error(t, ExportRecoveryKey(context.Background(), store, destination), "must not overwrite selected path")
	require.ErrorIs(t, ExportRecoveryKey(context.Background(), store, "relative.key"), ErrUnsafePath)
}

func TestExportRecoveryKeyRejectsSecretStoreLengthMismatch(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "recovery-key.txt")
	err := ExportRecoveryKey(context.Background(), fixedSecrets{key: []byte("short")}, destination)
	require.Error(t, err)
	require.NoFileExists(t, destination)
}
