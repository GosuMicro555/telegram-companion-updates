package export

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestKeywordExporterWritesAtomicUTF8FileAtExactPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "exports", "keywords.txt")
	exporter := NewKeywordExporter(path)
	got, err := exporter.Export(context.Background(), []string{"нет денег", "слишком дорого"})
	require.NoError(t, err)
	require.Equal(t, path, got)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.True(t, utf8.Valid(data))
	require.Equal(t, "нет денег\nслишком дорого\n", string(data))
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	for _, entry := range entries {
		require.False(t, strings.HasPrefix(entry.Name(), ".keywords-"))
	}
}

func TestKeywordExporterRejectsInvalidUTF8(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "exports", "keywords.txt")
	_, err := NewKeywordExporter(path).Export(context.Background(), []string{string([]byte{0xff})})
	require.ErrorContains(t, err, "UTF-8")
	require.NoFileExists(t, path)
}
