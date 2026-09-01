//go:build darwin

package secrets

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadMacOSKeyringUsesSeparateAccountAndPasswordFlags(t *testing.T) {
	var gotName string
	var gotArgs []string
	value, err := readMacOSKeyring(func(name string, args ...string) ([]byte, error) {
		gotName = name
		gotArgs = args
		return []byte("go-keyring-base64:WVdKag=="), nil
	}, "telegram-companion", "scout-message-key")

	require.NoError(t, err)
	require.Equal(t, "/usr/bin/security", gotName)
	require.Equal(t, []string{
		"find-generic-password", "-s", "telegram-companion", "-a", "scout-message-key", "-w",
	}, gotArgs)
	require.Equal(t, "YWJj", value)
}
