package driveaccounts

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func makeZIP(t *testing.T, names []string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.zip")
	f, e := os.Create(path)
	if e != nil {
		t.Fatal(e)
	}
	w := zip.NewWriter(f)
	for _, name := range names {
		entry, e := w.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		_, _ = entry.Write([]byte("synthetic"))
	}
	if e = w.Close(); e != nil {
		t.Fatal(e)
	}
	_ = f.Close()
	return path
}
func TestExtractZIPPreservesFoldersAndRejectsUnsafeMembers(t *testing.T) {
	root := t.TempDir()
	if err := ExtractZIP(context.Background(), makeZIP(t, []string{"outer/tdata/key_datas", "outer/tdata/ABC/configs"}), root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "outer", "tdata", "key_datas")); err != nil {
		t.Fatal(err)
	}
	for _, names := range [][]string{{"../escape"}, {"/absolute"}, {"C:/escape"}, {"tdata/a", "tdata/A"}, {"tdata/a", "tdata/a/b"}, {"tdata/CON"}, {"tdata/a:stream"}, {"tdata/trailing."}, {"A/x", "a/y"}, {"é/x", "e\u0301/y"}} {
		dst := t.TempDir()
		if err := ExtractZIP(context.Background(), makeZIP(t, names), dst); err == nil {
			t.Errorf("accepted unsafe ZIP %v", names)
		}
		entries, _ := os.ReadDir(dst)
		if len(entries) > 0 {
			t.Error("unsafe ZIP caused writes before validation completed")
		}
	}
}
func TestExtractZIPCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ExtractZIP(ctx, makeZIP(t, []string{"key_datas"}), t.TempDir()); err == nil {
		t.Fatal("ignored cancellation")
	}
}
