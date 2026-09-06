package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"telegram-companion/internal/licenseissuer"
	"telegram-companion/internal/revocation"
)

func TestWindowsGeneratorWiresFailClosedRevocationProvisioningCommand(t *testing.T) {
	source, err := os.ReadFile("main_windows.go")
	if err != nil {
		t.Fatalf("ReadFile(main_windows.go) error = %v", err)
	}
	for _, required := range []string{
		"parseRevocationProvisioningCommand",
		"provisionRevocation",
		"openRevocationProvisioningService",
		"newProductionGitHubRevocationAbsenceProbe",
		"runRevocationProvisioning",
		"NewSecretStore",
		"os.Unsetenv",
	} {
		if !bytes.Contains(source, []byte(required)) {
			t.Fatalf("main_windows.go is missing provisioning seam %q", required)
		}
	}
}

func TestParseRevocationProvisioningCommandRequiresExplicitOutputsAndSecretEnvironments(t *testing.T) {
	command, handled, err := parseRevocationProvisioningCommand([]string{
		"--provision-revocation",
		"--backup-output", `C:\operator\revocation.tcompkeybackup`,
		"--public-output", `C:\operator\revocation-public.json`,
	})
	if err != nil || !handled {
		t.Fatalf("parseRevocationProvisioningCommand() = (%#v, %t, %v)", command, handled, err)
	}
	if command.backupOutputPath == command.publicOutputPath || command.passwordEnv != defaultRevocationBackupPasswordEnv || command.credentialEnv != defaultRevocationCredentialEnv {
		t.Fatalf("command = %#v", command)
	}

	for _, arguments := range [][]string{
		{"--provision-revocation"},
		{"--provision-revocation", "--backup-output", "same", "--public-output", "same"},
		{"--provision-revocation", "--backup-output", "backup", "--public-output", "public", "--password", "secret"},
		{"--provision-revocation", "--backup-output", "backup", "--public-output", "public", "--credential", "secret"},
	} {
		if _, handled, err := parseRevocationProvisioningCommand(arguments); !handled || err == nil {
			t.Fatalf("parseRevocationProvisioningCommand(%q) = handled %t, error %v", arguments, handled, err)
		}
	}
}

func TestParseRevocationPreparationCommandRequiresThreeDistinctOutputsAndNoCredential(t *testing.T) {
	command, handled, err := parseRevocationPreparationCommand([]string{
		"--prepare-revocation",
		"--backup-output", `C:\operator\revocation.tcompkeybackup`,
		"--public-output", `C:\operator\revocation-public.json`,
		"--manifest-output", `C:\operator\revocations-seq0.tcrev`,
	})
	if err != nil || !handled {
		t.Fatalf("parseRevocationPreparationCommand() = (%#v, %t, %v)", command, handled, err)
	}
	if command.passwordEnv != defaultRevocationBackupPasswordEnv || command.backupOutputPath == command.publicOutputPath || command.backupOutputPath == command.manifestOutputPath || command.publicOutputPath == command.manifestOutputPath {
		t.Fatalf("command = %#v", command)
	}

	for _, arguments := range [][]string{
		{"--prepare-revocation"},
		{"--prepare-revocation", "--backup-output", "backup", "--public-output", "public", "--manifest-output", "public"},
		{"--prepare-revocation", "--backup-output", "backup", "--public-output", "public", "--manifest-output", "manifest", "--password", "secret"},
		{"--prepare-revocation", "--backup-output", "backup", "--public-output", "public", "--manifest-output", "manifest", "--credential-env", "TOKEN"},
		{"--prepare-revocation=true", "--backup-output", "backup", "--public-output", "public", "--manifest-output", "manifest"},
		{"--prepare-revocation", "--provision-revocation", "--backup-output", "backup", "--public-output", "public", "--manifest-output", "manifest"},
	} {
		if _, handled, err := parseRevocationPreparationCommand(arguments); !handled || err == nil {
			t.Fatalf("parseRevocationPreparationCommand(%q) = handled %t, error %v", arguments, handled, err)
		}
	}
}

func TestRevocationPreparationRejectsWindowsPathAliasesBeforeServiceUse(t *testing.T) {
	directory, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	absolute := filepath.Join(directory, "same-output")
	relative, err := filepath.Rel(directory, absolute)
	if err != nil {
		t.Fatalf("Rel() error = %v", err)
	}
	aliases := [][2]string{
		{absolute, strings.ToUpper(absolute)},
		{absolute, relative},
		{absolute, strings.ReplaceAll(absolute, `\`, `/`)},
	}
	for _, pair := range aliases {
		arguments := []string{"--prepare-revocation", "--backup-output", pair[0], "--public-output", pair[1], "--manifest-output", filepath.Join(directory, "manifest")}
		if _, handled, err := parseRevocationPreparationCommand(arguments); !handled || err == nil {
			t.Fatalf("parseRevocationPreparationCommand(alias %q, %q) = handled %t, error %v", pair[0], pair[1], handled, err)
		}

		service := &recordingRevocationPreparationService{}
		artifacts := &recordingRevocationPreparationArtifacts{events: &service.events}
		command := revocationPreparationCommand{backupOutputPath: pair[0], publicOutputPath: pair[1], manifestOutputPath: filepath.Join(directory, "manifest")}
		if err := runRevocationPreparation(context.Background(), command, "password", service, artifacts); !errors.Is(err, ErrGeneratorRevocationUnavailable) || len(service.events) != 0 {
			t.Fatalf("runRevocationPreparation(alias) = %v, events=%q", err, service.events)
		}
	}
}

func TestOpenRevocationProvisioningServiceGeneratesOnlyAfterExplicitAbsenceProof(t *testing.T) {
	for name, test := range map[string]struct {
		absent   bool
		probeErr error
		wantErr  bool
	}{
		"explicit absence": {absent: true},
		"remote present":   {wantErr: true},
		"ambiguous probe":  {probeErr: errors.New("network detail"), wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			repository := repositoryWithGeneratedState(t)
			revocationRepository := &memoryRevocationStateStore{loadErr: licenseissuer.ErrStateNotFound}
			probe := &recordingRevocationAbsenceProbe{absent: test.absent, err: test.probeErr}
			service, err := openRevocationProvisioningService(context.Background(), repository, revocationRepository, probe, fixedNow, deterministicRandom())
			if service != nil {
				defer service.Close()
			}
			if probe.calls != 1 || (err != nil) != test.wantErr {
				t.Fatalf("open = (%v, %v), proof calls = %d", service, err, probe.calls)
			}
			if test.wantErr {
				if revocationRepository.saveCalls != 0 || !errors.Is(err, ErrGeneratorRevocationUnavailable) || strings.Contains(err.Error(), "network detail") {
					t.Fatalf("failed open mutated state or leaked detail: saves=%d err=%q", revocationRepository.saveCalls, err)
				}
				return
			}
			if revocationRepository.saveCalls != 1 || !validGeneratorRevocationState(revocationRepository.state) {
				t.Fatalf("generated state = %#v, saves=%d", revocationRepository.state, revocationRepository.saveCalls)
			}
		})
	}
}

func TestOpenRevocationProvisioningServiceReusesExistingKeyWithoutAbsenceProbe(t *testing.T) {
	repository := repositoryWithGeneratedState(t)
	revocationRepository := &memoryRevocationStateStore{state: testGeneratorRevocationState(t)}
	probe := &recordingRevocationAbsenceProbe{}
	service, err := openRevocationProvisioningService(context.Background(), repository, revocationRepository, probe, fixedNow, deterministicRandom())
	if err != nil {
		t.Fatalf("openRevocationProvisioningService() error = %v", err)
	}
	defer service.Close()
	if probe.calls != 0 || revocationRepository.saveCalls != 0 {
		t.Fatalf("existing state triggered proof=%d saves=%d", probe.calls, revocationRepository.saveCalls)
	}
}

func TestOpenRevocationPreparationServiceRequiresExactAbsenceEvenForExistingKey(t *testing.T) {
	emptyExisting := testGeneratorRevocationState(t)
	emptyExisting.Records = []revocationPublicationRecord{}
	for name, test := range map[string]struct {
		state    generatorRevocationState
		loadErr  error
		absent   bool
		probeErr error
		wantErr  bool
	}{
		"new state after exact absence":            {loadErr: licenseissuer.ErrStateNotFound, absent: true},
		"existing empty state after exact absence": {state: emptyExisting, absent: true},
		"existing state with a record":             {state: testGeneratorRevocationState(t), absent: true, wantErr: true},
		"existing state with remote present":       {state: emptyExisting, wantErr: true},
		"existing state with ambiguous probe":      {state: emptyExisting, probeErr: errors.New("network detail"), wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			repository := repositoryWithGeneratedState(t)
			revocationRepository := &memoryRevocationStateStore{state: test.state, loadErr: test.loadErr}
			probe := &recordingRevocationAbsenceProbe{absent: test.absent, err: test.probeErr}
			service, err := openRevocationPreparationService(context.Background(), repository, revocationRepository, probe, fixedNow, deterministicRandom())
			if service != nil {
				defer service.Close()
			}
			if probe.calls != 1 || (err != nil) != test.wantErr {
				t.Fatalf("open = (%v, %v), proof calls = %d", service, err, probe.calls)
			}
			if test.wantErr {
				if revocationRepository.saveCalls != 0 || !errors.Is(err, ErrGeneratorRevocationUnavailable) || strings.Contains(err.Error(), "network detail") {
					t.Fatalf("failed preparation open mutated state or leaked detail: saves=%d err=%q", revocationRepository.saveCalls, err)
				}
				return
			}
			if test.loadErr != nil && revocationRepository.saveCalls != 1 {
				t.Fatalf("new preparation state saves = %d, want 1", revocationRepository.saveCalls)
			}
			if test.loadErr == nil && revocationRepository.saveCalls != 0 {
				t.Fatalf("existing preparation state saves = %d, want 0", revocationRepository.saveCalls)
			}
		})
	}
}

func TestRunRevocationProvisioningConfirmsValidatedV3BackupAndPublicExportBeforePublish(t *testing.T) {
	service := &recordingRevocationProvisioningService{
		status: GeneratorStatus{
			RevocationPublicKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, ed25519.PublicKeySize)),
		},
	}
	artifacts := &recordingRevocationProvisioningArtifacts{events: &service.events}
	command := revocationProvisioningCommand{backupOutputPath: "backup.tcompkeybackup", publicOutputPath: "public.json"}
	if err := runRevocationProvisioning(context.Background(), command, "strong backup password", service, artifacts); err != nil {
		t.Fatalf("runRevocationProvisioning() error = %v", err)
	}
	wantEvents := []string{"create-backup", "validate-backup", "store-backup", "confirm-backup", "store-public", "publish-sequence-zero"}
	if strings.Join(service.events, ",") != strings.Join(wantEvents, ",") {
		t.Fatalf("events = %q, want %q", service.events, wantEvents)
	}
	var exported map[string]string
	if err := json.Unmarshal(artifacts.publicDocument, &exported); err != nil || len(exported) != 2 || exported["key_id"] != generatorRevocationKeyID || exported["public_key"] != service.status.RevocationPublicKey {
		t.Fatalf("public export = %q, decoded=%#v, err=%v", artifacts.publicDocument, exported, err)
	}
	if bytes.Contains(artifacts.publicDocument, []byte(generatorBackupV3Format)) || bytes.Contains(artifacts.publicDocument, []byte("strong backup password")) {
		t.Fatal("public export contains secret backup material")
	}
}

func TestRunRevocationProvisioningNeverPublishesWhenBackupConfirmationIsNotDurable(t *testing.T) {
	service := &recordingRevocationProvisioningService{
		status:           GeneratorStatus{RevocationPublicKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x24}, ed25519.PublicKeySize))},
		leaveUnconfirmed: true,
	}
	artifacts := &recordingRevocationProvisioningArtifacts{events: &service.events}
	err := runRevocationProvisioning(context.Background(), revocationProvisioningCommand{backupOutputPath: "backup", publicOutputPath: "public"}, "password", service, artifacts)
	if !errors.Is(err, ErrGeneratorRevocationUnavailable) || strings.Contains(strings.Join(service.events, ","), "publish") || len(artifacts.publicDocument) != 0 {
		t.Fatalf("run = %v, events=%q, public=%q", err, service.events, artifacts.publicDocument)
	}
}

func TestRunRevocationPreparationConfirmsBackupBeforePublicAndSignedSequenceZero(t *testing.T) {
	service := &recordingRevocationPreparationService{
		recordingRevocationProvisioningService: recordingRevocationProvisioningService{
			status: GeneratorStatus{RevocationPublicKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x52}, ed25519.PublicKeySize))},
		},
		manifest: []byte("TCREV1.prepared-sequence-zero"),
	}
	artifacts := &recordingRevocationPreparationArtifacts{events: &service.events}
	command := revocationPreparationCommand{backupOutputPath: "backup.tcompkeybackup", publicOutputPath: "public.json", manifestOutputPath: "revocations-seq0.tcrev"}
	if err := runRevocationPreparation(context.Background(), command, "strong backup password", service, artifacts); err != nil {
		t.Fatalf("runRevocationPreparation() error = %v", err)
	}
	wantEvents := []string{"check-sequence-zero", "create-backup", "validate-backup", "store-backup", "confirm-backup", "store-public", "prepare-sequence-zero", "validate-sequence-zero", "store-sequence-zero"}
	if strings.Join(service.events, ",") != strings.Join(wantEvents, ",") {
		t.Fatalf("events = %q, want %q", service.events, wantEvents)
	}
	if !bytes.Equal(artifacts.manifestDocument, service.manifest) || bytes.Contains(artifacts.publicDocument, []byte("strong backup password")) || bytes.Contains(artifacts.manifestDocument, []byte("strong backup password")) {
		t.Fatal("preparation artifacts are missing or contain the backup password")
	}
}

func TestRunRevocationPreparationStopsBeforeBackupAndArtifactsWhenInitialManifestIsUnsafe(t *testing.T) {
	service := &recordingRevocationPreparationService{
		recordingRevocationProvisioningService: recordingRevocationProvisioningService{
			status: GeneratorStatus{RevocationPublicKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x53}, ed25519.PublicKeySize))},
		},
		preflightErr: ErrGeneratorRevocationUnavailable,
	}
	artifacts := &recordingRevocationPreparationArtifacts{events: &service.events}
	command := revocationPreparationCommand{backupOutputPath: "backup", publicOutputPath: "public", manifestOutputPath: "manifest"}
	err := runRevocationPreparation(context.Background(), command, "password", service, artifacts)
	if !errors.Is(err, ErrGeneratorRevocationUnavailable) || strings.Join(service.events, ",") != "check-sequence-zero" || len(artifacts.publicDocument) != 0 || len(artifacts.manifestDocument) != 0 {
		t.Fatalf("runRevocationPreparation(unsafe state) = %v, events=%q", err, service.events)
	}
}

func TestGeneratorServicePreparesAndValidatesCanonicalSignedSequenceZeroOnlyAfterBothBackups(t *testing.T) {
	service, repository := serviceWithRevocationState(t)
	defer service.Close()
	service.state.BackupConfirmed = true
	repository.state.BackupConfirmed = false
	if _, err := service.PrepareInitialRevocationManifest(); !errors.Is(err, ErrGeneratorRevocationUnavailable) {
		t.Fatalf("PrepareInitialRevocationManifest(unconfirmed) = %v", err)
	}

	repository.state.BackupConfirmed = true
	manifest, err := service.PrepareInitialRevocationManifest()
	if err != nil {
		t.Fatalf("PrepareInitialRevocationManifest() error = %v", err)
	}
	verified, err := revocation.Verify(string(manifest), generatorRevocationKeyID, service.revocationState.PublicKey)
	if err != nil {
		t.Fatalf("Verify(prepared manifest) error = %v", err)
	}
	payload := verified.PayloadCopy()
	if payload.Schema != revocation.ManifestSchema || payload.KeyID != generatorRevocationKeyID || payload.Sequence != 0 || len(payload.Entries) != 0 || payload.GeneratedAt != fixedNow().Format(time.RFC3339) {
		t.Fatalf("prepared payload = %#v", payload)
	}
	if err := service.ValidateInitialRevocationManifest(manifest); err != nil {
		t.Fatalf("ValidateInitialRevocationManifest(valid) error = %v", err)
	}
	tampered := append([]byte(nil), manifest...)
	tampered[len(tampered)-1] ^= 1
	if err := service.ValidateInitialRevocationManifest(tampered); !errors.Is(err, ErrGeneratorRevocationUnavailable) {
		t.Fatalf("ValidateInitialRevocationManifest(tampered) = %v", err)
	}
	if bytes.Contains(manifest, service.revocationState.PrivateKey) || bytes.Contains(manifest, []byte(generatorBackupV3Format)) {
		t.Fatal("prepared manifest contains private or backup material")
	}

	repository.state.Records = append([]revocationPublicationRecord(nil), testGeneratorRevocationState(t).Records...)
	service.revocationState.Records = []revocationPublicationRecord{}
	if err := service.CanPrepareInitialRevocationManifest(); !errors.Is(err, ErrGeneratorRevocationUnavailable) {
		t.Fatalf("CanPrepareInitialRevocationManifest(recorded) = %v", err)
	}
	if _, err := service.PrepareInitialRevocationManifest(); !errors.Is(err, ErrGeneratorRevocationUnavailable) {
		t.Fatalf("PrepareInitialRevocationManifest(recorded) = %v", err)
	}
	if err := service.ValidateInitialRevocationManifest(manifest); !errors.Is(err, ErrGeneratorRevocationUnavailable) {
		t.Fatalf("ValidateInitialRevocationManifest(recorded) = %v", err)
	}
}

type recordingRevocationAbsenceProbe struct {
	absent bool
	err    error
	calls  int
}

func (probe *recordingRevocationAbsenceProbe) ProveRemoteAbsent(context.Context) (bool, error) {
	probe.calls++
	return probe.absent, probe.err
}

type recordingRevocationProvisioningService struct {
	status           GeneratorStatus
	events           []string
	leaveUnconfirmed bool
}

func (service *recordingRevocationProvisioningService) Status() GeneratorStatus {
	return service.status
}

func (service *recordingRevocationProvisioningService) CreateBackup(string) (string, error) {
	service.events = append(service.events, "create-backup")
	return generatorBackupV3Format + ".encrypted", nil
}

func (service *recordingRevocationProvisioningService) ValidateBackup(string, string) error {
	service.events = append(service.events, "validate-backup")
	return nil
}

func (service *recordingRevocationProvisioningService) ConfirmBackup() error {
	service.events = append(service.events, "confirm-backup")
	if !service.leaveUnconfirmed {
		service.status.BackupConfirmed = true
		service.status.RevocationBackupConfirmed = true
	}
	return nil
}

func (service *recordingRevocationProvisioningService) InitializeRevocationManifest(context.Context) error {
	service.events = append(service.events, "publish-sequence-zero")
	return nil
}

type recordingRevocationPreparationService struct {
	recordingRevocationProvisioningService
	manifest     []byte
	preflightErr error
}

func (service *recordingRevocationPreparationService) CanPrepareInitialRevocationManifest() error {
	service.events = append(service.events, "check-sequence-zero")
	return service.preflightErr
}

func (service *recordingRevocationPreparationService) PrepareInitialRevocationManifest() ([]byte, error) {
	service.events = append(service.events, "prepare-sequence-zero")
	return append([]byte(nil), service.manifest...), nil
}

func (service *recordingRevocationPreparationService) ValidateInitialRevocationManifest(candidate []byte) error {
	service.events = append(service.events, "validate-sequence-zero")
	if !bytes.Equal(candidate, service.manifest) {
		return ErrGeneratorRevocationUnavailable
	}
	return nil
}

type recordingRevocationProvisioningArtifacts struct {
	events         *[]string
	publicDocument []byte
}

func (artifacts *recordingRevocationProvisioningArtifacts) EnsureBackup(_ string, candidate []byte, validate func([]byte) error) error {
	if err := validate(candidate); err != nil {
		return err
	}
	*artifacts.events = append(*artifacts.events, "store-backup")
	return nil
}

func (artifacts *recordingRevocationProvisioningArtifacts) EnsurePublic(_ string, document []byte) error {
	*artifacts.events = append(*artifacts.events, "store-public")
	artifacts.publicDocument = append([]byte(nil), document...)
	return nil
}

type recordingRevocationPreparationArtifacts struct {
	events           *[]string
	publicDocument   []byte
	manifestDocument []byte
}

func (artifacts *recordingRevocationPreparationArtifacts) EnsureBackup(_ string, candidate []byte, validate func([]byte) error) error {
	if err := validate(candidate); err != nil {
		return err
	}
	*artifacts.events = append(*artifacts.events, "store-backup")
	return nil
}

func (artifacts *recordingRevocationPreparationArtifacts) EnsurePublic(_ string, document []byte) error {
	*artifacts.events = append(*artifacts.events, "store-public")
	artifacts.publicDocument = append([]byte(nil), document...)
	return nil
}

func (artifacts *recordingRevocationPreparationArtifacts) EnsureManifest(_ string, create func() ([]byte, error), validate func([]byte) error) error {
	candidate, err := create()
	if err != nil {
		return err
	}
	if err := validate(candidate); err != nil {
		return err
	}
	*artifacts.events = append(*artifacts.events, "store-sequence-zero")
	artifacts.manifestDocument = append([]byte(nil), candidate...)
	return nil
}
