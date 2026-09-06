//go:build windows

package main

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"telegram-companion/internal/licenseissuer"
)

type saveFailingSigningStateStore struct {
	delegate *licenseissuer.StateStore
}

func (s saveFailingSigningStateStore) Load() (licenseissuer.GeneratorState, error) {
	return s.delegate.Load()
}

func (s saveFailingSigningStateStore) Save(licenseissuer.GeneratorState) error {
	return licenseissuer.ErrStateStorage
}

func TestProtectedStateRepositorySaveRollsBackSeedWhenSigningStateSaveFails(t *testing.T) {
	for _, test := range []struct {
		name      string
		priorSeed bool
	}{
		{name: "existing seed is restored", priorSeed: true},
		{name: "new seed file is removed when none existed", priorSeed: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			store, err := licenseissuer.NewStateStore(root)
			if err != nil {
				t.Fatalf("NewStateStore() error = %v", err)
			}

			oldPrivate := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x11}, ed25519.SeedSize))
			oldPublic := append(ed25519.PublicKey(nil), oldPrivate.Public().(ed25519.PublicKey)...)
			oldSigning := licenseissuer.GeneratorState{
				PrivateKey:      oldPrivate,
				PublicKey:       oldPublic,
				BackupConfirmed: true,
			}
			if err := store.Save(oldSigning); err != nil {
				t.Fatalf("save old signing state: %v", err)
			}

			repository := &protectedStateRepository{
				stateStore: saveFailingSigningStateStore{delegate: store},
				seedPath:   filepath.Join(root, seedStateFile),
			}
			oldSeedID := "11111111111111111111111111111111"
			oldSeedKey := bytes.Repeat([]byte{0x31}, 32)
			if test.priorSeed {
				if err := saveProtectedSeed(repository.seedPath, oldSeedID, oldSeedKey); err != nil {
					t.Fatalf("save old protected seed: %v", err)
				}
			}

			nextPrivate := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x22}, ed25519.SeedSize))
			nextState := generatorState{
				PrivateKey:      nextPrivate,
				PublicKey:       append(ed25519.PublicKey(nil), nextPrivate.Public().(ed25519.PublicKey)...),
				BackupConfirmed: false,
				SeedID:          "22222222222222222222222222222222",
				SeedKey:         bytes.Repeat([]byte{0x32}, 32),
			}
			if err := repository.Save(nextState); !errors.Is(err, licenseissuer.ErrStateStorage) {
				t.Fatalf("Save() error = %v, want ErrStateStorage", err)
			}

			got, err := repository.Load()
			if err != nil {
				t.Fatalf("Load() after failed Save() error = %v", err)
			}
			defer wipeBytes(got.PrivateKey)
			defer wipeBytes(got.SeedKey)
			if !bytes.Equal(got.PrivateKey, oldPrivate) || !bytes.Equal(got.PublicKey, oldPublic) || !got.BackupConfirmed {
				t.Fatal("failed Save() changed persisted signing state")
			}
			if test.priorSeed {
				if got.SeedID != oldSeedID || !bytes.Equal(got.SeedKey, oldSeedKey) {
					t.Fatal("failed Save() did not restore the previous seed")
				}
				return
			}
			if got.SeedID != "" || len(got.SeedKey) != 0 {
				t.Fatal("failed first Save() left a persisted seed")
			}
			if _, err := os.Lstat(repository.seedPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("seed file after failed first Save() error = %v, want file not found", err)
			}
		})
	}
}
