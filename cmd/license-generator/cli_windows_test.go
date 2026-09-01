//go:build windows

package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"telegram-companion/internal/licenseissuer"
)

func TestWindowsGeneratorSupportsNonInteractiveBootstrapSeedExport(t *testing.T) {
	source, err := os.ReadFile("main_windows.go")
	if err != nil {
		t.Fatalf("ReadFile(main_windows.go) error = %v", err)
	}

	for _, required := range []string{
		"--export-bootstrap-seed",
		"\"output\"",
		"\"password-env\"",
		"TC_LICENSE_GENERATOR_BOOTSTRAP_PASSWORD",
	} {
		if !strings.Contains(string(source), required) {
			t.Fatalf("Windows generator CLI is missing %q", required)
		}
	}
	for _, forbidden := range []string{"\"--password\"", "\"-password\""} {
		if strings.Contains(string(source), forbidden) {
			t.Fatal("Windows generator CLI must not accept the bootstrap password as an argument")
		}
	}
}

func TestWindowsGeneratorKeepsSigningGUIUsableWhenRevocationAttachNeedsRestore(t *testing.T) {
	source, err := os.ReadFile("main_windows.go")
	if err != nil {
		t.Fatalf("ReadFile(main_windows.go) error = %v", err)
	}
	text := string(source)
	if !strings.Contains(text, "_ = service.AttachRevocationPublication") {
		t.Fatal("GUI startup does not tolerate bounded revocation restore/publication degradation")
	}
	if strings.Contains(text, "service.AttachRevocationPublication(context.Background(), credentials, &githubRevocationPublisherFactory{}) != nil") {
		t.Fatal("GUI startup still exits when only revocation publication attachment is unavailable")
	}
}

func TestWindowsGeneratorWiresOfflineRevocationPreparationWithoutCredentialOrPublisher(t *testing.T) {
	source, err := os.ReadFile("main_windows.go")
	if err != nil {
		t.Fatalf("ReadFile(main_windows.go) error = %v", err)
	}
	text := string(source)
	for _, required := range []string{
		"parseRevocationPreparationCommand",
		"prepareRevocation",
		"openRevocationPreparationService",
		"runRevocationPreparation",
		"os.Unsetenv",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("main_windows.go is missing offline preparation seam %q", required)
		}
	}
	start := strings.Index(text, "func prepareRevocation(")
	if start < 0 {
		t.Fatal("prepareRevocation function is unavailable")
	}
	remainder := text[start:]
	end := strings.Index(remainder, "\nfunc ")
	if end < 0 {
		t.Fatal("prepareRevocation function boundary is unavailable")
	}
	body := remainder[:end]
	for _, forbidden := range []string{"credentialEnv", "newRevocationCredentials", "AttachRevocationPublication", "InitializeRevocationManifest", "github_pat_"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("offline prepareRevocation contains publication dependency %q", forbidden)
		}
	}
}

func TestBootstrapSeedExportCommandRequiresExplicitNonInteractiveInputs(t *testing.T) {
	command, handled, err := parseBootstrapSeedExportCommand([]string{
		"--export-bootstrap-seed",
		"--output", `C:\build\public.tcompbuildseed`,
	})
	if err != nil || !handled {
		t.Fatalf("parseBootstrapSeedExportCommand() = (%#v, %v, %v)", command, handled, err)
	}
	if command.outputPath != `C:\build\public.tcompbuildseed` || command.passwordEnv != defaultBootstrapSeedPasswordEnv {
		t.Fatalf("bootstrap seed export command = %#v", command)
	}

	for _, arguments := range [][]string{
		{"--export-bootstrap-seed"},
		{"--export-bootstrap-seed", "--output", "seed.tcompbuildseed", "--password", "secret"},
	} {
		if _, _, err := parseBootstrapSeedExportCommand(arguments); err == nil {
			t.Fatalf("parseBootstrapSeedExportCommand(%q) succeeded", arguments)
		}
	}
}

func TestExistingGeneratorBootstrapSeedExportPreservesSchema2GrantWithoutStateMutation(t *testing.T) {
	repository := repositoryWithGeneratedState(t)
	service, err := newExistingGeneratorService(repository)
	if err != nil {
		t.Fatalf("newExistingGeneratorService() error = %v", err)
	}
	defer service.Close()

	artifact, err := service.CreateBootstrapSeedExport("bootstrap export password")
	if err != nil {
		t.Fatalf("CreateBootstrapSeedExport() error = %v", err)
	}
	seedID, seedKey, err := licenseissuer.RestoreBootstrapSeedExport(artifact, "bootstrap export password")
	if err != nil {
		t.Fatalf("RestoreBootstrapSeedExport() error = %v", err)
	}
	defer wipeBytes(seedKey)
	if seedID != repository.state.SeedID || !bytes.Equal(seedKey, repository.state.SeedKey) || repository.saveCalls != 0 {
		t.Fatalf("exported seed grant = (%q, %x), saves = %d", seedID, seedKey, repository.saveCalls)
	}
}

func TestExistingGeneratorBootstrapSeedExportRefusesMissingState(t *testing.T) {
	repository := &memoryStateRepository{loadErr: licenseissuer.ErrStateNotFound}
	if _, err := newExistingGeneratorService(repository); err == nil {
		t.Fatal("newExistingGeneratorService() succeeded without existing state")
	}
	if repository.saveCalls != 0 {
		t.Fatalf("Save() calls = %d, want 0", repository.saveCalls)
	}
}

func TestExistingGeneratorBootstrapSeedExportMigratesLegacySigningState(t *testing.T) {
	repository := repositoryWithGeneratedState(t)
	repository.state.SeedID = ""
	repository.state.SeedKey = nil
	repository.saveCalls = 0

	service, err := newExistingGeneratorService(repository)
	if err != nil {
		t.Fatalf("newExistingGeneratorService() error = %v", err)
	}
	defer service.Close()
	if repository.saveCalls != 1 {
		t.Fatalf("Save() calls = %d, want 1", repository.saveCalls)
	}
	if !validSeedGrant(repository.state.SeedID, repository.state.SeedKey) {
		t.Fatal("legacy signing state was not migrated with a stable seed grant")
	}

	artifact, err := service.CreateBootstrapSeedExport("bootstrap export password")
	if err != nil {
		t.Fatalf("CreateBootstrapSeedExport() error = %v", err)
	}
	seedID, seedKey, err := licenseissuer.RestoreBootstrapSeedExport(artifact, "bootstrap export password")
	if err != nil {
		t.Fatalf("RestoreBootstrapSeedExport() error = %v", err)
	}
	defer wipeBytes(seedKey)
	if seedID != repository.state.SeedID || !bytes.Equal(seedKey, repository.state.SeedKey) {
		t.Fatal("migrated bootstrap seed export does not match persisted state")
	}
}

func TestWriteNewBootstrapSeedExportNeverOverwritesOutput(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "public.tcompbuildseed")
	if err := os.WriteFile(path, []byte("existing artifact"), 0o600); err != nil {
		t.Fatalf("WriteFile(existing) error = %v", err)
	}
	if err := writeNewBootstrapSeedExport(path, []byte("replacement artifact\n")); err == nil {
		t.Fatal("writeNewBootstrapSeedExport() overwrote an existing artifact")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(existing) error = %v", err)
	}
	if string(contents) != "existing artifact" {
		t.Fatalf("existing artifact = %q", contents)
	}

	newPath := filepath.Join(directory, "new.tcompbuildseed")
	if err := writeNewBootstrapSeedExport(newPath, []byte("TCBUILDSEED1.encrypted\n")); err != nil {
		t.Fatalf("writeNewBootstrapSeedExport(new) error = %v", err)
	}
	contents, err = os.ReadFile(newPath)
	if err != nil || string(contents) != "TCBUILDSEED1.encrypted\n" {
		t.Fatalf("new artifact = (%q, %v)", contents, err)
	}
}

func TestWindowsRevocationProvisioningArtifactsAllowOnlyMatchingRetries(t *testing.T) {
	directory := t.TempDir()
	artifacts := windowsRevocationProvisioningArtifacts{}
	backupPath := filepath.Join(directory, "revocation.tcompkeybackup")
	backup := []byte(generatorBackupV3Format + ".encrypted")
	validateCalls := 0
	validate := func(candidate []byte) error {
		validateCalls++
		if !bytes.Equal(candidate, backup) {
			return errors.New("unexpected backup")
		}
		return nil
	}
	if err := artifacts.EnsureBackup(backupPath, backup, validate); err != nil {
		t.Fatalf("EnsureBackup(new) error = %v", err)
	}
	if err := artifacts.EnsureBackup(backupPath, []byte("different generated ciphertext"), validate); err != nil {
		t.Fatalf("EnsureBackup(matching retry) error = %v", err)
	}
	if validateCalls != 3 {
		t.Fatalf("backup validation calls = %d", validateCalls)
	}

	publicPath := filepath.Join(directory, "revocation-public.json")
	public := []byte(`{"key_id":"revocation-2026-01","public_key":"public"}`)
	if err := artifacts.EnsurePublic(publicPath, public); err != nil {
		t.Fatalf("EnsurePublic(new) error = %v", err)
	}
	if err := artifacts.EnsurePublic(publicPath, public); err != nil {
		t.Fatalf("EnsurePublic(matching retry) error = %v", err)
	}
	if err := artifacts.EnsurePublic(publicPath, []byte(`{"key_id":"other"}`)); err == nil {
		t.Fatal("EnsurePublic() overwrote a mismatched existing export")
	}
	stored, err := os.ReadFile(publicPath)
	if err != nil || !bytes.Equal(stored, public) {
		t.Fatalf("stored public export = %q, %v", stored, err)
	}
}

func TestWindowsRevocationPreparationManifestIsNewOrValidatedWithoutRegeneration(t *testing.T) {
	directory := t.TempDir()
	artifacts := windowsRevocationProvisioningArtifacts{}
	path := filepath.Join(directory, "revocations-seq0.tcrev")
	want := []byte("TCREV1.exact-sequence-zero")
	createCalls := 0
	validateCalls := 0
	create := func() ([]byte, error) {
		createCalls++
		return append([]byte(nil), want...), nil
	}
	validate := func(candidate []byte) error {
		validateCalls++
		if !bytes.Equal(candidate, want) {
			return ErrGeneratorRevocationUnavailable
		}
		return nil
	}
	if err := artifacts.EnsureManifest(path, create, validate); err != nil {
		t.Fatalf("EnsureManifest(new) error = %v", err)
	}
	if err := artifacts.EnsureManifest(path, func() ([]byte, error) {
		createCalls++
		return nil, errors.New("existing manifest must not be regenerated")
	}, validate); err != nil {
		t.Fatalf("EnsureManifest(existing) error = %v", err)
	}
	stored, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(stored, want) || createCalls != 1 || validateCalls != 3 {
		t.Fatalf("stored manifest = (%q, %v), create calls = %d, validate calls = %d", stored, err, createCalls, validateCalls)
	}

	tamperedPath := filepath.Join(directory, "tampered.tcrev")
	if err := os.WriteFile(tamperedPath, []byte("tampered"), 0o600); err != nil {
		t.Fatalf("WriteFile(tampered) error = %v", err)
	}
	if err := artifacts.EnsureManifest(tamperedPath, create, validate); err == nil {
		t.Fatal("EnsureManifest(tampered) succeeded")
	}
	tampered, err := os.ReadFile(tamperedPath)
	if err != nil || string(tampered) != "tampered" || createCalls != 1 || validateCalls != 4 {
		t.Fatalf("tampered manifest changed = (%q, %v), create calls = %d, validate calls = %d", tampered, err, createCalls, validateCalls)
	}
}
