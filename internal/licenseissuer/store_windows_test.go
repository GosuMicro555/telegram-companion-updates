//go:build windows

package licenseissuer

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStateStoreRoundTripProtectsPrivateKeyAndWhitelistsHistory(t *testing.T) {
	root := t.TempDir()
	store, err := NewStateStore(root)
	if err != nil {
		t.Fatalf("NewStateStore() error = %v", err)
	}
	publicKey, privateKey, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	entry, err := NewHistoryEntry(testPayload())
	if err != nil {
		t.Fatalf("NewHistoryEntry() error = %v", err)
	}
	state := GeneratorState{
		PrivateKey:      privateKey,
		PublicKey:       publicKey,
		BackupConfirmed: true,
		History:         []HistoryEntry{entry},
	}
	if err := store.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	persisted, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	for _, secret := range []string{
		string(privateKey),
		base64.RawURLEncoding.EncodeToString(privateKey),
		base64.StdEncoding.EncodeToString(privateKey),
		"TCPLIC1.secret-token",
	} {
		if secret != "" && bytes.Contains(persisted, []byte(secret)) {
			t.Fatal("persisted state contains private-key or token material")
		}
	}
	for _, field := range []string{`"private_key_dpapi"`, `"public_key_spki"`, `"backup_confirmed"`, `"history"`} {
		if !bytes.Contains(persisted, []byte(field)) {
			t.Fatalf("persisted state omitted %s: %s", field, persisted)
		}
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !bytes.Equal(loaded.PrivateKey, privateKey) || !bytes.Equal(loaded.PublicKey, publicKey) {
		t.Fatal("Load() did not restore the active Ed25519 key pair")
	}
	if !loaded.BackupConfirmed || len(loaded.History) != 1 || loaded.History[0] != entry {
		t.Fatalf("Load() state = %#v, want backup confirmation and history", loaded)
	}
}

func TestStateStoreAtomicallyReplacesExistingState(t *testing.T) {
	store, err := NewStateStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStateStore() error = %v", err)
	}
	first := generatedState(t, false)
	second := generatedState(t, true)
	if err := store.Save(first); err != nil {
		t.Fatalf("first Save() error = %v", err)
	}
	if err := store.Save(second); err != nil {
		t.Fatalf("second Save() error = %v", err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !bytes.Equal(loaded.PrivateKey, second.PrivateKey) || !loaded.BackupConfirmed {
		t.Fatal("Load() did not return the replacement state")
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(store.Path()), ".generator-state-*.tmp"))
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary state files remain after Save(): %v", matches)
	}
}

func TestStateStoreRejectsPublicPrivateKeyMismatchWithSafeError(t *testing.T) {
	store, err := NewStateStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStateStore() error = %v", err)
	}
	state := generatedState(t, false)
	otherPublic, _, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	state.PublicKey = otherPublic
	if err := store.Save(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Save() error = %v, want ErrInvalidState", err)
	}
}

func TestStateStoreRejectsMalformedStateWithoutEchoingIt(t *testing.T) {
	store, err := NewStateStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStateStore() error = %v", err)
	}
	const secret = "private-secret-that-must-not-leak"
	malformed, err := json.Marshal(map[string]any{
		"schema":            1,
		"private_key_dpapi": secret,
		"public_key_spki":   "invalid",
		"backup_confirmed":  false,
		"history":           map[string]any{"schema": 1, "entries": []any{}},
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(store.Path(), malformed, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	_, err = store.Load()
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Load() error = %v, want ErrInvalidState", err)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), store.Path()) {
		t.Fatal("Load() error exposes persisted state or its path")
	}
}

func generatedState(t *testing.T, backupConfirmed bool) GeneratorState {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	return GeneratorState{
		PrivateKey:      privateKey,
		PublicKey:       publicKey,
		BackupConfirmed: backupConfirmed,
		History:         []HistoryEntry{},
	}
}
