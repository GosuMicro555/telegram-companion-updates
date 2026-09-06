package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"telegram-companion/internal/licenseissuer"
	"telegram-companion/internal/revocation"
)

func TestGeneratorServiceRequiresRestoreWhenMissingRevocationStateWasNotProvenRemoteAbsent(t *testing.T) {
	repository := repositoryWithGeneratedState(t)
	revocationRepository := &memoryRevocationStateStore{loadErr: licenseissuer.ErrStateNotFound}
	service, err := newGeneratorServiceWithRevocation(repository, revocationRepository, revocationBootstrapRequireRestore, fixedNow, deterministicRandom())
	if err != nil {
		t.Fatalf("newGeneratorServiceWithRevocation() error = %v", err)
	}
	defer service.Close()
	status := service.Status()
	if revocationRepository.saveCalls != 0 || status.RevocationPublicKey != "" || !status.RevocationRestoreRequired || status.BackupConfirmed {
		t.Fatalf("Status() = %#v, revocation saves = %d", status, revocationRepository.saveCalls)
	}
}

func TestGeneratorServiceGeneratesIndependentRevocationKeyOnlyAfterRemoteAbsentDecision(t *testing.T) {
	repository := repositoryWithGeneratedState(t)
	revocationRepository := &memoryRevocationStateStore{loadErr: licenseissuer.ErrStateNotFound}
	service, err := newGeneratorServiceWithRevocation(repository, revocationRepository, revocationBootstrapRemoteAbsent, fixedNow, deterministicRandom())
	if err != nil {
		t.Fatalf("newGeneratorServiceWithRevocation() error = %v", err)
	}
	defer service.Close()
	if revocationRepository.saveCalls != 1 || !validGeneratorRevocationState(revocationRepository.state) || revocationRepository.state.BackupConfirmed {
		t.Fatalf("generated revocation state = %#v, saves = %d", revocationRepository.state, revocationRepository.saveCalls)
	}
	if bytes.Equal(revocationRepository.state.PrivateKey, repository.state.PrivateKey) || bytes.Equal(revocationRepository.state.PublicKey, repository.state.PublicKey) {
		t.Fatal("revocation key is not independent from the signing key")
	}
	status := service.Status()
	wantPublicKey := base64.StdEncoding.EncodeToString(revocationRepository.state.PublicKey)
	if status.RevocationPublicKey != wantPublicKey || status.RevocationRestoreRequired || status.BackupConfirmed {
		t.Fatalf("Status() = %#v", status)
	}
}

func TestGeneratorServiceLoadsExistingRevocationStateWithoutStatusRewrite(t *testing.T) {
	repository := repositoryWithGeneratedState(t)
	repository.state.BackupConfirmed = true
	revocationRepository := &memoryRevocationStateStore{state: testGeneratorRevocationState(t)}
	service, err := newGeneratorServiceWithRevocation(repository, revocationRepository, revocationBootstrapRemoteAbsent, fixedNow, deterministicRandom())
	if err != nil {
		t.Fatalf("newGeneratorServiceWithRevocation() error = %v", err)
	}
	defer service.Close()
	status := service.Status()
	if !status.BackupConfirmed || !status.RevocationBackupConfirmed || status.RevocationRestoreRequired {
		t.Fatalf("Status() = %#v", status)
	}
	if repository.saveCalls != 0 || revocationRepository.saveCalls != 0 {
		t.Fatalf("Status() rewrote state: signing saves = %d, revocation saves = %d", repository.saveCalls, revocationRepository.saveCalls)
	}
}

func TestGeneratorServiceCreatesV3AndConfirmsBothRepositories(t *testing.T) {
	repository := repositoryWithGeneratedState(t)
	revocationRepository := &memoryRevocationStateStore{state: testGeneratorRevocationState(t)}
	revocationRepository.state.BackupConfirmed = false
	service, err := newGeneratorServiceWithRevocation(repository, revocationRepository, revocationBootstrapRequireRestore, fixedNow, deterministicRandom())
	if err != nil {
		t.Fatalf("newGeneratorServiceWithRevocation() error = %v", err)
	}
	defer service.Close()
	backup, err := service.CreateBackup("a portable v3 service password")
	if err != nil {
		t.Fatalf("CreateBackup() error = %v", err)
	}
	if !strings.HasPrefix(backup, generatorBackupV3Format+".") || service.Status().BackupConfirmed || repository.saveCalls != 0 || revocationRepository.saveCalls != 0 {
		t.Fatalf("backup/status before confirmation = %q / %#v", backup, service.Status())
	}
	if err := service.ConfirmBackup(); err != nil {
		t.Fatalf("ConfirmBackup() error = %v", err)
	}
	if !repository.state.BackupConfirmed || !revocationRepository.state.BackupConfirmed || !service.Status().BackupConfirmed || repository.saveCalls != 1 || revocationRepository.saveCalls != 1 {
		t.Fatalf("confirmation states = signing %#v, revocation %#v, status %#v", repository.state, revocationRepository.state, service.Status())
	}
}

func TestGeneratorServiceConfirmBackupRollsBackSigningStateWhenRevocationSaveFails(t *testing.T) {
	repository := repositoryWithGeneratedState(t)
	revocationRepository := &memoryRevocationStateStore{state: testGeneratorRevocationState(t)}
	revocationRepository.state.BackupConfirmed = false
	service, err := newGeneratorServiceWithRevocation(repository, revocationRepository, revocationBootstrapRequireRestore, fixedNow, deterministicRandom())
	if err != nil {
		t.Fatalf("newGeneratorServiceWithRevocation() error = %v", err)
	}
	defer service.Close()
	revocationRepository.saveErr = licenseissuer.ErrStateStorage
	if err := service.ConfirmBackup(); !errors.Is(err, ErrGeneratorStorage) {
		t.Fatalf("ConfirmBackup() error = %v, want ErrGeneratorStorage", err)
	}
	if repository.state.BackupConfirmed || revocationRepository.state.BackupConfirmed || service.Status().BackupConfirmed {
		t.Fatal("failed confirmation left a repository or memory state confirmed")
	}
	if repository.saveCalls != 2 || revocationRepository.saveCalls != 1 {
		t.Fatalf("rollback saves = signing %d, revocation %d", repository.saveCalls, revocationRepository.saveCalls)
	}
}

func TestGeneratorServiceConfirmBackupDoesNotTouchRevocationAfterSigningSaveFails(t *testing.T) {
	repository := repositoryWithGeneratedState(t)
	revocationRepository := &memoryRevocationStateStore{state: testGeneratorRevocationState(t)}
	revocationRepository.state.BackupConfirmed = false
	service, err := newGeneratorServiceWithRevocation(repository, revocationRepository, revocationBootstrapRequireRestore, fixedNow, deterministicRandom())
	if err != nil {
		t.Fatalf("newGeneratorServiceWithRevocation() error = %v", err)
	}
	defer service.Close()
	repository.saveErr = licenseissuer.ErrStateStorage
	if err := service.ConfirmBackup(); !errors.Is(err, ErrGeneratorStorage) {
		t.Fatalf("ConfirmBackup() error = %v, want ErrGeneratorStorage", err)
	}
	if repository.saveCalls != 1 || revocationRepository.saveCalls != 0 || service.Status().BackupConfirmed {
		t.Fatalf("saves after signing failure = signing %d, revocation %d", repository.saveCalls, revocationRepository.saveCalls)
	}
}

func TestGeneratorServiceRestoresV3TransactionallyIntoRestoreRequiredState(t *testing.T) {
	material := testGeneratorBackupV3Material(0x71)
	backup, err := createGeneratorBackupV3(material, "v3 restore service password")
	if err != nil {
		t.Fatalf("createGeneratorBackupV3() error = %v", err)
	}
	repository := repositoryWithGeneratedState(t)
	revocationRepository := &memoryRevocationStateStore{loadErr: licenseissuer.ErrStateNotFound}
	service, err := newGeneratorServiceWithRevocation(repository, revocationRepository, revocationBootstrapRequireRestore, fixedNow, deterministicRandom())
	if err != nil {
		t.Fatalf("newGeneratorServiceWithRevocation() error = %v", err)
	}
	defer service.Close()
	handle, _ := revocation.DeriveHandle("license-restored-remote")
	service.revocationFactory = &recordingGeneratorPublisherFactory{restoreEvidence: revocationRestoreEvidence{
		Present:        true,
		Sequence:       7,
		ManifestDigest: [32]byte{0x71},
		Entries:        []revocation.Entry{{Kind: revocation.EntryKindLicenseIDSHA256, Value: handle.String(), RevokedAt: "2026-08-25T12:00:00Z"}},
	}}
	oldSigningKey := service.state.PrivateKey
	oldSeedKey := service.state.SeedKey
	if err := service.RestoreBackup(backup, "v3 restore service password"); err != nil {
		t.Fatalf("RestoreBackup(v3) error = %v", err)
	}
	if !bytes.Equal(repository.state.PrivateKey, material.LicensePrivateKey) || !bytes.Equal(repository.state.SeedKey, material.SeedKey) || !bytes.Equal(revocationRepository.state.PrivateKey, material.RevocationPrivateKey) || len(revocationRepository.state.Records) != 1 || revocationRepository.state.Records[0].Handle != handle.String() || revocationRepository.state.Records[0].PublishedSequence != 7 {
		t.Fatal("RestoreBackup(v3) did not adopt all three secrets")
	}
	if !repository.state.BackupConfirmed || !revocationRepository.state.BackupConfirmed || !service.Status().BackupConfirmed || service.Status().RevocationRestoreRequired {
		t.Fatalf("Status() after v3 restore = %#v", service.Status())
	}
	if !bytes.Equal(oldSigningKey, make([]byte, len(oldSigningKey))) || !bytes.Equal(oldSeedKey, make([]byte, len(oldSeedKey))) {
		t.Fatal("RestoreBackup(v3) retained superseded signing or seed bytes")
	}
}

func TestGeneratorServiceRejectsV3RestoreWhenRemoteInspectionIsAmbiguousWithoutMutation(t *testing.T) {
	material := testGeneratorBackupV3Material(0x72)
	backup, _ := createGeneratorBackupV3(material, "v3 remote inspection password")
	repository := repositoryWithGeneratedState(t)
	beforeSigning := cloneGeneratorState(repository.state)
	revocationRepository := &memoryRevocationStateStore{loadErr: licenseissuer.ErrStateNotFound}
	service, err := newGeneratorServiceWithRevocation(repository, revocationRepository, revocationBootstrapRequireRestore, fixedNow, deterministicRandom())
	if err != nil {
		t.Fatalf("newGeneratorServiceWithRevocation() error = %v", err)
	}
	defer service.Close()
	service.revocationFactory = &recordingGeneratorPublisherFactory{restoreErr: errors.New("remote body and credentials must not escape")}
	if err := service.RestoreBackup(backup, "v3 remote inspection password"); !errors.Is(err, ErrGeneratorRevocationUnavailable) || strings.Contains(err.Error(), "remote body") {
		t.Fatalf("RestoreBackup(ambiguous remote) error = %q", err)
	}
	if repository.saveCalls != 0 || revocationRepository.saveCalls != 0 || !bytes.Equal(repository.state.PrivateKey, beforeSigning.PrivateKey) {
		t.Fatal("ambiguous remote inspection mutated generator state")
	}
}

func TestGeneratorServiceRejectsV3RevocationKeyMismatchWithoutMutation(t *testing.T) {
	material := testGeneratorBackupV3Material(0x31)
	backup, err := createGeneratorBackupV3(material, "v3 mismatch password")
	if err != nil {
		t.Fatalf("createGeneratorBackupV3() error = %v", err)
	}
	repository := repositoryWithGeneratedState(t)
	revocationRepository := &memoryRevocationStateStore{state: testGeneratorRevocationStateWithByte(t, 0x55)}
	service, err := newGeneratorServiceWithRevocation(repository, revocationRepository, revocationBootstrapRequireRestore, fixedNow, deterministicRandom())
	if err != nil {
		t.Fatalf("newGeneratorServiceWithRevocation() error = %v", err)
	}
	defer service.Close()
	if err := service.RestoreBackup(backup, "v3 mismatch password"); !errors.Is(err, ErrGeneratorKeyMismatch) {
		t.Fatalf("RestoreBackup(v3 mismatch) error = %v, want ErrGeneratorKeyMismatch", err)
	}
	if repository.saveCalls != 0 || revocationRepository.saveCalls != 0 {
		t.Fatal("rejected v3 mismatch mutated a repository")
	}
}

func TestGeneratorServiceLegacyRestorePreservesRevocationKeyButClearsItsConfirmation(t *testing.T) {
	material := testGeneratorBackupV3Material(0x43)
	legacy, err := createGeneratorBackup(material.LicensePrivateKey, material.SeedID, material.SeedKey, "legacy service password")
	if err != nil {
		t.Fatalf("createGeneratorBackup(v2) error = %v", err)
	}
	repository := repositoryWithGeneratedState(t)
	revocationRepository := &memoryRevocationStateStore{state: testGeneratorRevocationState(t)}
	originalRevocationKey := append(ed25519.PrivateKey(nil), revocationRepository.state.PrivateKey...)
	service, err := newGeneratorServiceWithRevocation(repository, revocationRepository, revocationBootstrapRequireRestore, fixedNow, deterministicRandom())
	if err != nil {
		t.Fatalf("newGeneratorServiceWithRevocation() error = %v", err)
	}
	defer service.Close()
	if err := service.RestoreBackup(legacy, "legacy service password"); err != nil {
		t.Fatalf("RestoreBackup(v2) error = %v", err)
	}
	if !bytes.Equal(revocationRepository.state.PrivateKey, originalRevocationKey) || revocationRepository.state.BackupConfirmed || service.Status().BackupConfirmed {
		t.Fatalf("legacy restore revocation state = %#v, status = %#v", revocationRepository.state, service.Status())
	}
}

func TestGeneratorServiceCloseWipesActiveRevocationPrivateKey(t *testing.T) {
	service, _ := serviceWithRevocationState(t)
	activeRevocationKey := service.revocationState.PrivateKey
	service.Close()
	if !bytes.Equal(activeRevocationKey, make([]byte, len(activeRevocationKey))) || service.revocationState.PrivateKey != nil {
		t.Fatal("Close() retained the active revocation private key")
	}
}

func TestGeneratorServiceRejectsRevocationSecretsAndV3MarkerBeforeIssuance(t *testing.T) {
	repository := repositoryWithGeneratedState(t)
	revocationRepository := &memoryRevocationStateStore{state: testGeneratorRevocationState(t)}
	service, err := newGeneratorServiceWithRevocation(repository, revocationRepository, revocationBootstrapRequireRestore, fixedNow, deterministicRandom())
	if err != nil {
		t.Fatalf("newGeneratorServiceWithRevocation() error = %v", err)
	}
	defer service.Close()
	revocationSeed := service.revocationState.PrivateKey.Seed()
	defer wipeBytes(revocationSeed)
	comments := []string{
		generatorBackupV3Format + ".license.seed.revocation",
		base64.StdEncoding.EncodeToString(service.revocationState.PrivateKey),
		base64.RawURLEncoding.EncodeToString(service.revocationState.PrivateKey),
		hex.EncodeToString(service.revocationState.PrivateKey),
		base64.StdEncoding.EncodeToString(revocationSeed),
		base64.RawURLEncoding.EncodeToString(revocationSeed),
		hex.EncodeToString(revocationSeed),
		strings.ToUpper(hex.EncodeToString(revocationSeed)),
	}
	for index, comment := range comments {
		if _, err := service.Issue(IssueRequest{MachineID: strings.Repeat("AB", 32), Owner: "Denis", Comment: comment}); !errors.Is(err, ErrInvalidIssueRequest) {
			t.Errorf("Issue(revocation secret %d) error = %v, want ErrInvalidIssueRequest", index, err)
		}
	}
	if repository.saveCalls != 0 || len(service.Status().History) != 0 {
		t.Fatal("revocation secret reached issuance history")
	}
}
