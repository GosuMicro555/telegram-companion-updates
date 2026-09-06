package localdb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"telegram-companion/internal/domain"
	"telegram-companion/internal/usecase"

	bolt "go.etcd.io/bbolt"
)

func TestOpenKeywordSettingsStoreWithTimeoutDoesNotBlockOnLockedDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	store, err := OpenKeywordSettingsStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	started := time.Now()
	_, err = OpenKeywordSettingsStoreWithTimeout(path, 25*time.Millisecond)
	if err == nil {
		t.Fatal("second open unexpectedly acquired locked bbolt database")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("bounded bbolt open blocked for %s", elapsed)
	}
}

func TestKeywordSettingsStoreSnapshotIsTransactionallyConsistentDuringWrites(t *testing.T) {
	store, err := OpenKeywordSettingsStore(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	started := make(chan struct{})
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		for generation := 1; ; generation++ {
			select {
			case <-stop:
				done <- nil
				return
			default:
			}
			value := []byte(strconv.Itoa(generation))
			if err := store.db.Update(func(tx *bolt.Tx) error {
				bucket := tx.Bucket(settingsBucket)
				if err := bucket.Put([]byte("snapshot-left"), value); err != nil {
					return err
				}
				return bucket.Put([]byte("snapshot-right"), value)
			}); err != nil {
				done <- err
				return
			}
			if generation == 1 {
				close(started)
			}
		}
	}()
	<-started

	snapshotPath := filepath.Join(t.TempDir(), "state.bolt")
	if err := store.Snapshot(context.Background(), snapshotPath); err != nil {
		t.Fatalf("Snapshot returned error: %v", err)
	}
	close(stop)
	if err := <-done; err != nil {
		t.Fatalf("concurrent writer returned error: %v", err)
	}
	info, err := os.Stat(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("snapshot mode = %o, want 600", info.Mode().Perm())
	}

	snapshot, err := bolt.Open(snapshotPath, 0o600, &bolt.Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	if err := snapshot.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(settingsBucket)
		left := append([]byte(nil), bucket.Get([]byte("snapshot-left"))...)
		right := append([]byte(nil), bucket.Get([]byte("snapshot-right"))...)
		if len(left) == 0 || !bytes.Equal(left, right) {
			t.Fatalf("inconsistent snapshot values: left=%q right=%q", left, right)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSetCatalogTopicsReturnsSentinelAndLeavesRowsUnchangedForUnknownID(t *testing.T) {
	ctx := context.Background()
	store, err := OpenKeywordSettingsStore(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("OpenKeywordSettingsStore returned error: %v", err)
	}
	defer store.Close()

	for _, row := range []domain.Channel{
		{ID: "one", Link: "https://t.me/one", Topic: "Old", Active: true},
		{ID: "two", Link: "https://t.me/two", Topic: "Old", Active: true},
	} {
		if err := store.SaveCatalog(ctx, domain.SourceCatalogScout, row); err != nil {
			t.Fatalf("SaveCatalog returned error: %v", err)
		}
	}

	err = store.SetCatalogTopics(ctx, domain.SourceCatalogScout, []domain.ID{"one", "missing"}, "New")
	if !errors.Is(err, usecase.ErrChannelNotFound) {
		t.Fatalf("SetCatalogTopics error = %v, want ErrChannelNotFound", err)
	}
	rows, err := store.ListCatalog(ctx, domain.SourceCatalogScout)
	if err != nil {
		t.Fatalf("ListCatalog returned error: %v", err)
	}
	if len(rows) != 2 || rows[0].Topic != "Old" || rows[1].Topic != "Old" {
		t.Fatalf("rows changed after failed transaction: %+v", rows)
	}
}

func TestKeywordSettingsStorePersistsSettings(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "app.db")
	store, err := OpenKeywordSettingsStore(dbPath)
	if err != nil {
		t.Fatalf("OpenKeywordSettingsStore returned error: %v", err)
	}

	settings := domain.KeywordSettings{
		Keywords:      []string{"alpha", "beta", "beta", " gamma "},
		MinusKeywords: []string{" blocked phrase ", "BLOCKED PHRASE", "[fixed order]"},
		SharedReply:   " sample reply https://example.test ",
	}
	if err := store.Save(context.Background(), settings); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	reopened, err := OpenKeywordSettingsStore(dbPath)
	if err != nil {
		t.Fatalf("reopen returned error: %v", err)
	}
	defer reopened.Close()

	got, err := reopened.Load(context.Background())
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if got.SharedReply != " sample reply https://example.test " {
		t.Fatalf("SharedReply = %q", got.SharedReply)
	}
	want := []string{"alpha", "beta", "gamma"}
	if len(got.Keywords) != len(want) {
		t.Fatalf("keywords = %v, want %v", got.Keywords, want)
	}
	for i := range want {
		if got.Keywords[i] != want[i] {
			t.Fatalf("keywords = %v, want %v", got.Keywords, want)
		}
	}
	if wantMinus := []string{"blocked phrase", "[fixed order]"}; !reflect.DeepEqual(got.MinusKeywords, wantMinus) {
		t.Fatalf("minus keywords = %v, want %v", got.MinusKeywords, wantMinus)
	}
}

func TestKeywordSettingsStorePreservesDirectMessageKeywordCompatibility(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "app.db")
	store, err := OpenKeywordSettingsStore(dbPath)
	if err != nil {
		t.Fatalf("OpenKeywordSettingsStore returned error: %v", err)
	}

	var settings domain.KeywordSettings
	if err := json.Unmarshal([]byte(`{"keywords":["слил","тест1"],"sharedReply":"ответ","directMessageKeywords":["тест1"]}`), &settings); err != nil {
		t.Fatalf("Unmarshal settings returned error: %v", err)
	}
	if err := store.Save(context.Background(), settings); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	reopened, err := OpenKeywordSettingsStore(dbPath)
	if err != nil {
		t.Fatalf("reopen returned error: %v", err)
	}
	defer reopened.Close()

	got, err := reopened.Load(context.Background())
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal loaded settings returned error: %v", err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal loaded JSON returned error: %v", err)
	}
	if got := string(payload["directMessageKeywords"]); got != `["тест1"]` {
		t.Fatalf("directMessageKeywords = %s, want [\"тест1\"]", got)
	}
}

func TestKeywordSettingsStoreKeepsLegacyNilAndExplicitEmptyDirectMessageKeywordsDistinct(t *testing.T) {
	store, err := OpenKeywordSettingsStore(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("OpenKeywordSettingsStore returned error: %v", err)
	}
	defer store.Close()

	loadPayload := func(t *testing.T, raw string) string {
		t.Helper()
		if err := store.db.Update(func(tx *bolt.Tx) error {
			return tx.Bucket(settingsBucket).Put(keywordsKey, []byte(raw))
		}); err != nil {
			t.Fatalf("write settings returned error: %v", err)
		}
		settings, err := store.Load(context.Background())
		if err != nil {
			t.Fatalf("Load returned error: %v", err)
		}
		data, err := json.Marshal(settings)
		if err != nil {
			t.Fatalf("Marshal settings returned error: %v", err)
		}
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(data, &payload); err != nil {
			t.Fatalf("Unmarshal settings JSON returned error: %v", err)
		}
		return string(payload["directMessageKeywords"])
	}

	if got := loadPayload(t, `{"keywords":["слил"],"sharedReply":"ответ"}`); got != "null" {
		t.Fatalf("legacy directMessageKeywords = %s, want null", got)
	}
	if got := loadPayload(t, `{"keywords":["слил"],"sharedReply":"ответ","directMessageKeywords":[]}`); got != "[]" {
		t.Fatalf("explicit empty directMessageKeywords = %s, want []", got)
	}
}

func TestKeywordSettingsStoreReturnsProductionSafeDefaultsWhenEmpty(t *testing.T) {
	store, err := OpenKeywordSettingsStore(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("OpenKeywordSettingsStore returned error: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	got, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if got.SharedReply != "" || len(got.Keywords) != 0 || len(got.DirectMessageKeywords) != 0 {
		t.Fatalf("keyword defaults contain acceptance payload: %+v", got)
	}
	channels, err := store.LoadChannels(ctx)
	if err != nil {
		t.Fatalf("LoadChannels returned error: %v", err)
	}
	if len(channels) != 0 {
		t.Fatalf("channel defaults contain acceptance payload: %+v", channels)
	}
	accounts, err := store.ListAccounts(ctx)
	if err != nil {
		t.Fatalf("ListAccounts returned error: %v", err)
	}
	if len(accounts) != 0 {
		t.Fatalf("account defaults contain acceptance identities: %+v", accounts)
	}
}

func TestKeywordSettingsStorePersistsChannels(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "app.db")
	store, err := OpenKeywordSettingsStore(dbPath)
	if err != nil {
		t.Fatalf("OpenKeywordSettingsStore returned error: %v", err)
	}

	channels := []domain.ManagedChannel{
		{
			ID:           "boat",
			Title:        "Двое в лодке",
			Link:         " https://t.me/+unitTestInviteHash ",
			Status:       domain.ChannelStatusPaused,
			Members:      "3/3",
			Sent:         7,
			LastActivity: "20:37",
			Active:       false,
		},
	}
	if err := store.SaveChannels(context.Background(), channels); err != nil {
		t.Fatalf("SaveChannels returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	reopened, err := OpenKeywordSettingsStore(dbPath)
	if err != nil {
		t.Fatalf("reopen returned error: %v", err)
	}
	defer reopened.Close()

	got, err := reopened.LoadChannels(context.Background())
	if err != nil {
		t.Fatalf("LoadChannels returned error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("channels = %+v, want one item", got)
	}
	if got[0].Title != "Двое в лодке" || got[0].Link != "https://t.me/+unitTestInviteHash" || got[0].Active {
		t.Fatalf("channel was not normalized/persisted: %+v", got[0])
	}
}

func TestKeywordSettingsStorePersistsAppSettings(t *testing.T) {
	store, err := OpenKeywordSettingsStore(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("OpenKeywordSettingsStore returned error: %v", err)
	}
	defer store.Close()

	want := domain.AppSettings{
		RepliesPerMinute:   12,
		MinIntervalSeconds: 4,
		DirectMessages:     false,
		Proxy:              " socks5://127.0.0.1:19050 ",
	}
	if err := store.SaveAppSettings(context.Background(), want); err != nil {
		t.Fatalf("SaveAppSettings returned error: %v", err)
	}
	got, err := store.LoadAppSettings(context.Background())
	if err != nil {
		t.Fatalf("LoadAppSettings returned error: %v", err)
	}
	if got.RepliesPerMinute != 12 || got.MinIntervalSeconds != 4 || got.DirectMessages || got.Proxy != "socks5://127.0.0.1:19050" {
		t.Fatalf("settings = %+v, want normalized %+v", got, want)
	}
}
