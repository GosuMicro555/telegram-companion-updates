package localdb

import (
	"context"
	"path/filepath"
	"testing"

	"telegram-companion/internal/domain"

	bolt "go.etcd.io/bbolt"
)

func TestKeywordSettingsStorePreservesSharedReplyExactly(t *testing.T) {
	store, err := OpenKeywordSettingsStore(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("OpenKeywordSettingsStore returned error: %v", err)
	}
	defer store.Close()

	want := "  ответ как введён  \n"
	if err := store.Save(context.Background(), domain.KeywordSettings{SharedReply: want}); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	got, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if got.SharedReply != want {
		t.Fatalf("SharedReply = %q, want %q", got.SharedReply, want)
	}
}

func TestKeywordSettingsStoreMigratesMissingPrivateReplyWithoutOverwritingExplicitEmptyValue(t *testing.T) {
	store, err := OpenKeywordSettingsStore(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("OpenKeywordSettingsStore returned error: %v", err)
	}
	defer store.Close()

	load := func(raw string) domain.KeywordSettings {
		t.Helper()
		err := store.db.Update(func(tx *bolt.Tx) error {
			return tx.Bucket(settingsBucket).Put(keywordsKey, []byte(raw))
		})
		if err != nil {
			t.Fatalf("write settings returned error: %v", err)
		}
		settings, err := store.Load(context.Background())
		if err != nil {
			t.Fatalf("Load returned error: %v", err)
		}
		return settings
	}

	legacy := load(`{"sharedReply":"comment reply"}`)
	if legacy.PrivateReply != "comment reply" {
		t.Fatalf("legacy PrivateReply = %q, want shared reply", legacy.PrivateReply)
	}
	if legacy.PrivateReplyPresent {
		t.Fatal("legacy PrivateReplyPresent = true, want false")
	}

	explicitEmpty := load(`{"sharedReply":"comment reply","privateReply":""}`)
	if explicitEmpty.PrivateReply != "" {
		t.Fatalf("explicit empty PrivateReply = %q, want empty", explicitEmpty.PrivateReply)
	}
	if !explicitEmpty.PrivateReplyPresent {
		t.Fatal("explicit empty PrivateReplyPresent = false, want true")
	}
}
