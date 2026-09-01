//go:build darwin

package macosarm64_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAppTreeDigestIgnoresCopyMtimeButDetectsMaterialChanges(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.app")
	clone := filepath.Join(root, "clone.app")
	resources := filepath.Join(source, "Contents", "Resources")
	if err := os.MkdirAll(resources, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(resources, "payload.txt")
	if err := os.WriteFile(payload, []byte("same-content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("payload.txt", filepath.Join(resources, "payload-link")); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("/usr/bin/ditto", source, clone).CombinedOutput(); err != nil {
		t.Fatalf("ditto fixture: %v: %s", err, output)
	}
	changedTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(clone, changedTime, changedTime); err != nil {
		t.Fatal(err)
	}

	sourceDigest := runAppTreeDigest(t, source)
	cloneDigest := runAppTreeDigest(t, clone)
	if sourceDigest != cloneDigest {
		t.Fatal("canonical digest changed only because copied directory metadata changed")
	}

	clonePayload := filepath.Join(clone, "Contents", "Resources", "payload.txt")
	if err := os.WriteFile(clonePayload, []byte("changed-content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if sourceDigest == runAppTreeDigest(t, clone) {
		t.Fatal("canonical digest ignored a file content change")
	}
	if err := os.WriteFile(clonePayload, []byte("same-content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(clonePayload, 0o755); err != nil {
		t.Fatal(err)
	}
	if sourceDigest == runAppTreeDigest(t, clone) {
		t.Fatal("canonical digest ignored a file mode change")
	}
	if err := os.Chmod(clonePayload, 0o644); err != nil {
		t.Fatal(err)
	}
	cloneLink := filepath.Join(clone, "Contents", "Resources", "payload-link")
	if err := os.Remove(cloneLink); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../Resources/payload.txt", cloneLink); err != nil {
		t.Fatal(err)
	}
	if sourceDigest == runAppTreeDigest(t, clone) {
		t.Fatal("canonical digest ignored a symlink target change")
	}
}

func runAppTreeDigest(t *testing.T, bundle string) string {
	t.Helper()
	command := exec.Command(
		"/bin/bash", "-c", `source "$1"; app_tree_digest "$2"`,
		"app-tree-digest-test", scriptPath("build-dual-local.sh"), bundle,
	)
	command.Env = append(os.Environ(), "PATH=/usr/bin:/bin:/usr/sbin:/sbin")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("app_tree_digest: %v: %s", err, output)
	}
	digest := strings.TrimSpace(string(output))
	if len(digest) != 64 {
		t.Fatalf("unexpected digest length %d", len(digest))
	}
	return digest
}
