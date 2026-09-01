package license

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFileStoreFirstSaveSyncsCreatedDirectoryAndRename(t *testing.T) {
	dataRoot := t.TempDir()
	store := NewFileStore(dataRoot)
	var synced []string

	err := store.save(goldenToken, func(path string) error {
		synced = append(synced, filepath.Clean(path))
		return nil
	})
	if err != nil {
		t.Fatalf("save() error = %v", err)
	}

	want := []string{filepath.Clean(dataRoot), filepath.Clean(store.Directory())}
	if len(synced) != len(want) {
		t.Fatalf("synced directories = %q, want %q", synced, want)
	}
	for i := range want {
		if synced[i] != want[i] {
			t.Fatalf("synced directories = %q, want %q", synced, want)
		}
	}
}

func TestFileStoreSaveReloadAndReplace(t *testing.T) {
	store := NewFileStore(t.TempDir())
	if err := store.Save(goldenToken); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.Save(replacementToken); err != nil {
		t.Fatalf("replacement Save() error = %v", err)
	}

	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got != replacementToken {
		t.Fatalf("Load() = %q", got)
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(store.Path())
		if err != nil {
			t.Fatalf("Stat() error = %v", err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("license mode = %o, want 0600", got)
		}
	}
}

func TestFileStoreLoadAcceptsSingleTerminalLineEnding(t *testing.T) {
	for _, lineEnding := range []string{"\n", "\r\n"} {
		t.Run(base64.RawURLEncoding.EncodeToString([]byte(lineEnding)), func(t *testing.T) {
			store := NewFileStore(t.TempDir())
			if err := os.MkdirAll(store.Directory(), 0o700); err != nil {
				t.Fatalf("MkdirAll() error = %v", err)
			}
			if err := os.WriteFile(store.Path(), []byte(goldenToken+lineEnding), 0o600); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}

			got, err := store.Load()
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if got != goldenToken {
				t.Fatalf("Load() returned a non-canonical token")
			}
		})
	}
}

func TestFileStoreLoadRejectsCorruptBase64Envelope(t *testing.T) {
	store := NewFileStore(t.TempDir())
	if err := os.MkdirAll(store.Directory(), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(store.Path(), []byte("TCPLIC1.invalid%payload.invalid-signature"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := store.Load()
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("Load() error = %v, want ErrMalformed", err)
	}
}

func TestFileStoreLoadRejectsCorruptFile(t *testing.T) {
	store := NewFileStore(t.TempDir())
	if err := os.MkdirAll(store.Directory(), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(store.Path(), []byte("corrupt"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := store.Load()
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("Load() error = %v, want ErrMalformed", err)
	}
}

func TestFileStoreLoadRejectsOversizedLicenseBeforeReadingIt(t *testing.T) {
	store := NewFileStore(t.TempDir())
	if err := os.MkdirAll(store.Directory(), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(make([]byte, 50*1024))
	signature := base64.RawURLEncoding.EncodeToString(make([]byte, 64))
	oversized := "TCPLIC1." + payload + "." + signature
	if len(oversized) <= 64*1024 {
		t.Fatalf("test token length = %d, want more than 64 KiB", len(oversized))
	}
	if err := os.WriteFile(store.Path(), []byte(oversized), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := store.Load()
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("Load() error = %v, want ErrMalformed", err)
	}
}

func TestFileStoreLoadReportsMissingLicense(t *testing.T) {
	_, err := NewFileStore(t.TempDir()).Load()
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load() error = %v, want ErrNotFound", err)
	}
}
