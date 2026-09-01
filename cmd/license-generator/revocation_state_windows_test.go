//go:build windows

package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"telegram-companion/internal/licenseissuer"
	"telegram-companion/internal/revocation"
)

func TestRevocationStateRepositoriesRejectStaleConcurrentWholeStateReplacement(t *testing.T) {
	root := t.TempDir()
	first, err := newRevocationStateRepository(root)
	if err != nil {
		t.Fatalf("newRevocationStateRepository(first) error = %v", err)
	}
	second, err := newRevocationStateRepository(root)
	if err != nil {
		t.Fatalf("newRevocationStateRepository(second) error = %v", err)
	}
	initial := testGeneratorRevocationState(t)
	if err := first.Save(initial); err != nil {
		t.Fatalf("Save(initial) error = %v", err)
	}
	if _, err := first.Load(); err != nil {
		t.Fatalf("Load(first) error = %v", err)
	}
	if _, err := second.Load(); err != nil {
		t.Fatalf("Load(second) error = %v", err)
	}
	firstNext := cloneGeneratorRevocationState(initial)
	firstNext.BackupConfirmed = true
	secondNext := cloneGeneratorRevocationState(initial)
	concurrentHandle, err := revocation.DeriveHandle("license-concurrent-persistence")
	if err != nil {
		t.Fatalf("DeriveHandle() error = %v", err)
	}
	secondNext.Records = append(secondNext.Records, revocationPublicationRecord{
		EventID:     "concurrent-event",
		Handle:      concurrentHandle.String(),
		RequestedAt: time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC).Format(time.RFC3339),
		State:       revocationPublicationPending,
	})
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() { <-start; results <- first.Save(firstNext) }()
	go func() { <-start; results <- second.Save(secondNext) }()
	close(start)
	errorsSeen := []error{<-results, <-results}
	successes := 0
	conflicts := 0
	for _, saveErr := range errorsSeen {
		if saveErr == nil {
			successes++
		} else if errors.Is(saveErr, licenseissuer.ErrStateStorage) {
			conflicts++
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent Save results = %v, want one success and one stale conflict", errorsSeen)
	}
	reader, _ := newRevocationStateRepository(root)
	final, err := reader.Load()
	if err != nil {
		info, statErr := os.Stat(reader.Path())
		raw, readErr := readRevocationStateFile(reader.Path())
		decoded, decodeErr := decodeRevocationState(raw, unprotectRevocationPrivateKey)
		wipeBytes(decoded.PrivateKey)
		t.Fatalf("Load(final) error = %v; stat=%v/%v read=%d/%v decode=%v", err, info, statErr, len(raw), readErr, decodeErr)
	}
	defer wipeBytes(final.PrivateKey)
	if final.BackupConfirmed == initial.BackupConfirmed && len(final.Records) == len(initial.Records) {
		t.Fatal("concurrent writes silently retained the stale baseline")
	}
}

func TestRevocationStateRepositoryRoundTripUsesDedicatedSidecar(t *testing.T) {
	repository, err := newRevocationStateRepository(t.TempDir())
	if err != nil {
		t.Fatalf("newRevocationStateRepository() error = %v", err)
	}
	state := testGeneratorRevocationState(t)
	if err := repository.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if filepath.Base(repository.Path()) != revocationStateFile {
		t.Fatalf("Path() = %q, want %q", repository.Path(), revocationStateFile)
	}
	persisted, err := os.ReadFile(repository.Path())
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	for _, secret := range []string{
		string(state.PrivateKey),
		base64.StdEncoding.EncodeToString(state.PrivateKey),
		base64.RawStdEncoding.EncodeToString(state.PrivateKey),
	} {
		if secret != "" && bytes.Contains(persisted, []byte(secret)) {
			t.Fatal("persisted revocation state exposes the private key")
		}
	}
	loaded, err := repository.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	defer wipeBytes(loaded.PrivateKey)
	if !bytes.Equal(loaded.PrivateKey, state.PrivateKey) || !bytes.Equal(loaded.PublicKey, state.PublicKey) || len(loaded.Records) != 1 {
		t.Fatalf("Load() state = %#v", loaded)
	}
}

func TestRevocationStateRepositoryAtomicallyKeepsPreviousStateOnRejectedSave(t *testing.T) {
	repository, err := newRevocationStateRepository(t.TempDir())
	if err != nil {
		t.Fatalf("newRevocationStateRepository() error = %v", err)
	}
	state := testGeneratorRevocationState(t)
	if err := repository.Save(state); err != nil {
		t.Fatalf("Save(valid) error = %v", err)
	}
	invalid := state
	invalid.PublicKey = append([]byte(nil), state.PublicKey...)
	invalid.PublicKey[0] ^= 0xff
	if err := repository.Save(invalid); !errors.Is(err, licenseissuer.ErrInvalidState) {
		t.Fatalf("Save(invalid) error = %v, want ErrInvalidState", err)
	}
	loaded, err := repository.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	defer wipeBytes(loaded.PrivateKey)
	if !bytes.Equal(loaded.PrivateKey, state.PrivateKey) || !bytes.Equal(loaded.PublicKey, state.PublicKey) {
		t.Fatal("rejected Save() replaced the prior revocation state")
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(repository.Path()), ".generator-revocation-*.tmp"))
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary state files remain after Save(): %v", matches)
	}
}

func TestRevocationStateRepositoryKeepsPreviousStateWhenReplaceFails(t *testing.T) {
	repository, err := newRevocationStateRepository(t.TempDir())
	if err != nil {
		t.Fatalf("newRevocationStateRepository() error = %v", err)
	}
	state := testGeneratorRevocationState(t)
	if err := repository.Save(state); err != nil {
		t.Fatalf("Save(initial) error = %v", err)
	}

	replaceCalled := false
	temporaryExisted := false
	repository.replaceFile = func(source, destination string) error {
		replaceCalled = true
		temporaryExisted = destination == repository.Path()
		if _, statErr := os.Stat(source); statErr != nil {
			temporaryExisted = false
		}
		return errors.New("injected replace failure")
	}
	updated := cloneGeneratorRevocationState(state)
	updated.BackupConfirmed = !state.BackupConfirmed
	if err := repository.Save(updated); !errors.Is(err, licenseissuer.ErrStateStorage) {
		t.Fatalf("Save(replace failure) error = %v, want ErrStateStorage", err)
	}
	if !replaceCalled || !temporaryExisted {
		t.Fatal("Save() did not reach replacement with a complete temporary file")
	}
	loaded, err := repository.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	defer wipeBytes(loaded.PrivateKey)
	if loaded.BackupConfirmed != state.BackupConfirmed || !bytes.Equal(loaded.PrivateKey, state.PrivateKey) {
		t.Fatal("failed replacement changed the previous revocation state")
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(repository.Path()), ".generator-revocation-*.tmp"))
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary state files remain after failed replacement: %v", matches)
	}
}

func TestRevocationStateDPAPIUsesDedicatedEntropy(t *testing.T) {
	plaintext := bytes.Repeat([]byte{0x4a}, 64)
	protected, err := protectRevocationPrivateKey(plaintext)
	if err != nil {
		t.Fatalf("protectRevocationPrivateKey() error = %v", err)
	}
	restored, err := unprotectRevocationPrivateKeyWithEntropy(protected, revocationStateEntropy)
	if err != nil {
		t.Fatalf("unprotect with revocation entropy error = %v", err)
	}
	defer wipeBytes(restored)
	if !bytes.Equal(restored, plaintext) {
		t.Fatal("DPAPI round-trip changed private bytes")
	}
	wrong, err := unprotectRevocationPrivateKeyWithEntropy(protected, seedStateEntropy)
	wipeBytes(wrong)
	if err == nil {
		t.Fatal("revocation state decrypted with the seed-state entropy")
	}
}

func TestRevocationStateDPAPIBlobIsWipedBeforeRelease(t *testing.T) {
	secret := bytes.Repeat([]byte{0x4d}, ed25519.PrivateKeySize)
	want := append([]byte(nil), secret...)
	blob := windows.DataBlob{Size: uint32(len(secret)), Data: &secret[0]}
	released := false
	wipedAtRelease := false
	copy, err := copyWipeAndReleaseRevocationBlob(&blob, func(windows.Handle) {
		released = true
		wipedAtRelease = bytes.Equal(secret, make([]byte, len(secret)))
	})
	if err != nil {
		t.Fatalf("copyWipeAndReleaseRevocationBlob() error = %v", err)
	}
	defer wipeBytes(copy)
	if !bytes.Equal(copy, want) {
		t.Fatal("copyWipeAndReleaseRevocationBlob() changed the returned secret")
	}
	if !released || !wipedAtRelease {
		t.Fatal("DPAPI output buffer was not wiped before release")
	}
	if blob.Data != nil || blob.Size != 0 {
		t.Fatal("released DPAPI blob retained its pointer or size")
	}
}
