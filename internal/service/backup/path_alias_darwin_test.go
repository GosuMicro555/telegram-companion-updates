//go:build darwin

package backup

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeTrustedSystemPathCanonicalizesMacOSVarAlias(t *testing.T) {
	path := filepath.Join(string(filepath.Separator), "var", "folders", "telegram-companion")

	normalized, err := normalizeTrustedSystemPath(path)

	require.NoError(t, err)
	require.Equal(t, filepath.Join(string(filepath.Separator), "private", "var", "folders", "telegram-companion"), normalized)
}
