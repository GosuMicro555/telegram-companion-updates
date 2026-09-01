package export

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOSFileOpenerAllowsOnlyExactConfiguredPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "exports", "keywords.txt")
	runner := &recordingRunner{}
	opener := NewOSFileOpenerWithRunner(path, "linux", runner)
	require.Error(t, opener.Open(context.Background(), filepath.Join(filepath.Dir(path), "other.txt")))
	require.Empty(t, runner.command)
	require.NoError(t, opener.Open(context.Background(), path))
	require.Equal(t, "/usr/bin/xdg-open", runner.command)
	require.Equal(t, []string{path}, runner.args)
}

func TestOSFileOpenerUsesAllowlistedMacExecutable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keywords.txt")
	runner := &recordingRunner{}
	require.NoError(t, NewOSFileOpenerWithRunner(path, "darwin", runner).Open(context.Background(), path))
	require.Equal(t, "/usr/bin/open", runner.command)
}

func TestOSFileOpenerRejectsUnsupportedOS(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keywords.txt")
	err := NewOSFileOpenerWithRunner(path, "windows", &recordingRunner{}).Open(context.Background(), path)
	require.ErrorContains(t, err, "unsupported")
}

type recordingRunner struct {
	command string
	args    []string
}

func (r *recordingRunner) Run(_ context.Context, command string, args ...string) error {
	r.command = command
	r.args = append([]string(nil), args...)
	return nil
}
