package driveaccounts

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/gotd/td/crypto"
	"github.com/gotd/td/session"
	"github.com/gotd/td/session/tdesktop"
	"telegram-companion/internal/domain"
	appcrypto "telegram-companion/internal/service/crypto"
)

func TestConvertAccountUsesStableIdentityAndGotdSession(t *testing.T) {
	var key crypto.Key
	for i := range key {
		key[i] = byte(i)
	}
	source := tdesktop.Account{Authorization: tdesktop.MTPAuthorization{UserID: 12345, MainDC: 2, Keys: map[int]crypto.Key{2: key}}}
	c, err := convertAccount(source, appcrypto.AppCredentials{AppID: 123, AppHash: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if c.UserID != 12345 || c.Account.ID == "" || len(c.Session.AuthKey) != 256 {
		t.Fatal("incomplete conversion")
	}
	source.IDx = 8
	other, err := convertAccount(source, c.Credentials)
	if err != nil || other.Account.ID != c.Account.ID {
		t.Fatal("identity depends on archive position")
	}
}
func TestDiscoverFindsRootsAtAnyLevelAndRejectsUnknownData(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "outer", "tdata")
	if err := os.MkdirAll(p, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "key_datas"), []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	roots, err := discoverRoots(context.Background(), root)
	if err != nil || len(roots) != 1 || roots[0] != "outer/tdata" {
		t.Fatal("root discovery", err, roots)
	}
	if _, err = readCandidates(context.Background(), root, appcrypto.AppCredentials{AppID: 1, AppHash: "test"}); err == nil {
		t.Fatal("accepted invalid TData")
	}
}

type importerStore struct {
	accounts  []domain.Account
	committed [][]Candidate
	commitErr error
}

func (s *importerStore) ListAccounts(context.Context) ([]domain.Account, error) {
	return append([]domain.Account(nil), s.accounts...), nil
}

func (s *importerStore) CommitDriveAccounts(_ context.Context, candidates []Candidate) error {
	if s.commitErr != nil {
		return s.commitErr
	}
	copy := append([]Candidate(nil), candidates...)
	s.committed = append(s.committed, copy)
	for _, candidate := range copy {
		s.accounts = append(s.accounts, candidate.Account)
	}
	return nil
}

func syntheticTDataZIP(t *testing.T) []byte {
	t.Helper()
	var archive bytes.Buffer
	w := zip.NewWriter(&archive)
	for name, data := range syntheticTData(t, 1, 1) {
		entry, err := w.Create("tdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = entry.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return archive.Bytes()
}

func syntheticZIPDownloader(t *testing.T) *Downloader {
	t.Helper()
	archive := syntheticTDataZIP(t)
	d := NewDownloader()
	d.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "drive.google.com" || r.URL.Path != "/uc" {
			t.Fatalf("unexpected download request: %s", r.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/zip"}}, Body: io.NopCloser(bytes.NewReader(archive)), ContentLength: int64(len(archive))}, nil
	})
	return d
}

func directoryEntries(t *testing.T, root string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestImporterProcessPersistsUsableSessionAndSkipsDuplicate(t *testing.T) {
	root := t.TempDir()
	store := &importerStore{}
	importer := &Importer{
		Downloader:  syntheticZIPDownloader(t),
		Store:       store,
		SessionRoot: filepath.Join(root, "sessions"),
		ScratchRoot: filepath.Join(root, "scratch"),
		Defaults:    appcrypto.AppCredentials{AppID: 123, AppHash: "synthetic"},
		Verify: func(ctx context.Context, candidate *Candidate) error {
			loaded, err := (&session.Loader{Storage: &session.FileStorage{Path: candidate.Account.SessionPath}}).Load(ctx)
			if err != nil || !bytes.Equal(loaded.AuthKey, candidate.Session.AuthKey) {
				t.Fatalf("saved session is not usable: %v", err)
			}
			return nil
		},
	}

	first, err := importer.Process(context.Background(), Reference{ID: "synthetic"}, func(string) {})
	if err != nil || first != (Result{Added: 1}) || len(store.committed) != 1 {
		t.Fatalf("first import: result=%+v commits=%d err=%v", first, len(store.committed), err)
	}
	persisted := store.committed[0][0]
	if _, err := os.Stat(persisted.Account.SessionPath); err != nil {
		t.Fatalf("committed session missing: %v", err)
	}
	if len(directoryEntries(t, importer.ScratchRoot)) != 0 {
		t.Fatal("scratch batch was retained after successful import")
	}

	second, err := importer.Process(context.Background(), Reference{ID: "synthetic"}, func(string) {})
	if err != nil || second != (Result{Skipped: 1}) || len(store.committed) != 1 {
		t.Fatalf("duplicate import: result=%+v commits=%d err=%v", second, len(store.committed), err)
	}
	if _, err := os.Stat(persisted.Account.SessionPath); err != nil {
		t.Fatalf("duplicate import disturbed prior session: %v", err)
	}
	if len(directoryEntries(t, importer.ScratchRoot)) != 0 {
		t.Fatal("scratch batch was retained after duplicate import")
	}
}

func TestImporterProcessFailureRemovesOnlyNewSessionsAndScratch(t *testing.T) {
	for _, test := range []struct {
		name      string
		verifyErr error
		commitErr error
		wantErr   string
	}{
		{name: "verification", verifyErr: errors.New("session_invalid"), wantErr: "session_invalid"},
		{name: "commit", commitErr: errors.New("database unavailable"), wantErr: "storage_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			sessionRoot := filepath.Join(root, "sessions")
			if err := os.MkdirAll(sessionRoot, 0700); err != nil {
				t.Fatal(err)
			}
			existingPath := filepath.Join(sessionRoot, "existing.json")
			var existingKey [256]byte
			if _, err := rand.Read(existingKey[:]); err != nil {
				t.Fatal(err)
			}
			if err := (&session.Loader{Storage: &session.FileStorage{Path: existingPath}}).Save(context.Background(), &session.Data{AuthKey: existingKey[:]}); err != nil {
				t.Fatal(err)
			}
			store := &importerStore{accounts: []domain.Account{{ID: "existing", SessionPath: existingPath}}, commitErr: test.commitErr}
			importer := &Importer{
				Downloader:  syntheticZIPDownloader(t),
				Store:       store,
				SessionRoot: sessionRoot,
				ScratchRoot: filepath.Join(root, "scratch"),
				Defaults:    appcrypto.AppCredentials{AppID: 123, AppHash: "synthetic"},
				Verify: func(context.Context, *Candidate) error {
					return test.verifyErr
				},
			}

			_, err := importer.Process(context.Background(), Reference{ID: "synthetic"}, func(string) {})
			if err == nil || err.Error() != test.wantErr {
				t.Fatalf("wrong import error: %v", err)
			}
			if len(store.committed) != 0 {
				t.Fatal("failed import committed candidates")
			}
			if _, err := (&session.Loader{Storage: &session.FileStorage{Path: existingPath}}).Load(context.Background()); err != nil {
				t.Fatalf("existing session was disturbed: %v", err)
			}
			entries := directoryEntries(t, sessionRoot)
			if len(entries) != 1 || entries[0].Name() != "existing.json" {
				t.Fatalf("new session directory was retained: %v", entries)
			}
			if len(directoryEntries(t, importer.ScratchRoot)) != 0 {
				t.Fatal("scratch batch was retained after failure")
			}
		})
	}
}
