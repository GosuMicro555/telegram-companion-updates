package secrets

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEncryptedFileSecretStorePersistsWithoutSystemKeyring(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "secrets", "runtime.enc")
	first, err := NewEncryptedFileSecretStore(path, "license-token-for-test")
	require.NoError(t, err)
	require.NoError(t, first.Set(context.Background(), "telegram-session", []byte("session-secret-value")))

	second, err := NewEncryptedFileSecretStore(path, "license-token-for-test")
	require.NoError(t, err)
	got, err := second.Get(context.Background(), "telegram-session")
	require.NoError(t, err)
	require.Equal(t, []byte("session-secret-value"), got)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.False(t, bytes.Contains(raw, []byte("session-secret-value")))
	require.False(t, bytes.Contains(raw, []byte("license-token-for-test")))
}

func TestEncryptedFileSecretStoreRejectsWrongCredential(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "runtime.enc")
	store, err := NewEncryptedFileSecretStore(path, "correct-license")
	require.NoError(t, err)
	require.NoError(t, store.Set(context.Background(), "database-key", []byte("secret")))

	wrong, err := NewEncryptedFileSecretStore(path, "wrong-license")
	require.NoError(t, err)
	_, err = wrong.Get(context.Background(), "database-key")
	require.ErrorContains(t, err, "decrypt encrypted secret store")
}

func TestEncryptedFileSecretStoreRequiresPathAndCredential(t *testing.T) {
	t.Parallel()

	_, err := NewEncryptedFileSecretStore("", "license")
	require.ErrorContains(t, err, "path")

	_, err = NewEncryptedFileSecretStore(filepath.Join(t.TempDir(), "runtime.enc"), "")
	require.ErrorContains(t, err, "credential")
}
