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
)

func TestPortableBackupV3RoundTripBindsAllThreeSecrets(t *testing.T) {
	const password = "a long portable v3 password"
	material := testGeneratorBackupV3Material(0x31)
	backup, err := createGeneratorBackupV3(material, password)
	if err != nil {
		t.Fatalf("createGeneratorBackupV3() error = %v", err)
	}
	if !strings.HasPrefix(backup, "TCPKEYBACKUP3.") || len(strings.Split(backup, ".")) != 4 {
		t.Fatalf("createGeneratorBackupV3() format = %q", backup)
	}
	for _, secret := range [][]byte{material.LicensePrivateKey, material.SeedKey, material.RevocationPrivateKey} {
		for _, exposed := range []string{
			base64.StdEncoding.EncodeToString(secret),
			base64.RawURLEncoding.EncodeToString(secret),
			hex.EncodeToString(secret),
		} {
			if strings.Contains(backup, exposed) {
				t.Fatal("createGeneratorBackupV3() exposes plaintext secret material")
			}
		}
	}

	restored, err := restoreGeneratorBackupMaterial(backup, password)
	if err != nil {
		t.Fatalf("restoreGeneratorBackupMaterial() error = %v", err)
	}
	defer wipeGeneratorBackupMaterial(&restored)
	if !bytes.Equal(restored.LicensePrivateKey, material.LicensePrivateKey) ||
		restored.SeedID != material.SeedID || !bytes.Equal(restored.SeedKey, material.SeedKey) ||
		!bytes.Equal(restored.RevocationPrivateKey, material.RevocationPrivateKey) {
		t.Fatal("v3 round-trip did not preserve all three secret inputs")
	}
}

func TestPortableBackupV3RejectsWrongPasswordSplicingAndNoncanonicalParts(t *testing.T) {
	const password = "one shared portable v3 password"
	first, err := createGeneratorBackupV3(testGeneratorBackupV3Material(0x31), password)
	if err != nil {
		t.Fatalf("create first v3 backup: %v", err)
	}
	second, err := createGeneratorBackupV3(testGeneratorBackupV3Material(0x42), password)
	if err != nil {
		t.Fatalf("create second v3 backup: %v", err)
	}
	firstParts := strings.Split(first, ".")
	secondParts := strings.Split(second, ".")
	spliced := strings.Join([]string{firstParts[0], firstParts[1], firstParts[2], secondParts[3]}, ".")
	nonCanonical := strings.Join([]string{firstParts[0], firstParts[1] + "=", firstParts[2], firstParts[3]}, ".")
	truncated := strings.Join(firstParts[:3], ".")

	for name, test := range map[string]struct {
		backup   string
		password string
	}{
		"wrong password":        {backup: first, password: "a different password"},
		"spliced third part":    {backup: spliced, password: password},
		"noncanonical encoding": {backup: nonCanonical, password: password},
		"truncated":             {backup: truncated, password: password},
	} {
		t.Run(name, func(t *testing.T) {
			material, err := restoreGeneratorBackupMaterial(test.backup, test.password)
			wipeGeneratorBackupMaterial(&material)
			if !errors.Is(err, licenseissuer.ErrInvalidBackup) {
				t.Fatalf("restoreGeneratorBackupMaterial() error = %v, want ErrInvalidBackup", err)
			}
		})
	}
}

func TestRestoreGeneratorBackupMaterialKeepsLegacyFormatsRevocationFree(t *testing.T) {
	const password = "a legacy portable password"
	material := testGeneratorBackupV3Material(0x53)
	v2, err := createGeneratorBackup(material.LicensePrivateKey, material.SeedID, material.SeedKey, password)
	if err != nil {
		t.Fatalf("createGeneratorBackup(v2) error = %v", err)
	}
	v1, err := licenseissuer.BackupPrivateKey(material.LicensePrivateKey, password)
	if err != nil {
		t.Fatalf("BackupPrivateKey(v1) error = %v", err)
	}

	for name, backup := range map[string]string{"v1": v1, "v2": v2} {
		t.Run(name, func(t *testing.T) {
			restored, err := restoreGeneratorBackupMaterial(backup, password)
			if err != nil {
				t.Fatalf("restoreGeneratorBackupMaterial() error = %v", err)
			}
			defer wipeGeneratorBackupMaterial(&restored)
			if !bytes.Equal(restored.LicensePrivateKey, material.LicensePrivateKey) {
				t.Fatal("legacy restore changed the license signing key")
			}
			if name == "v2" && (restored.SeedID != material.SeedID || !bytes.Equal(restored.SeedKey, material.SeedKey)) {
				t.Fatal("v2 restore changed the seed grant")
			}
			if name == "v1" && (restored.SeedID != "" || len(restored.SeedKey) != 0) {
				t.Fatal("v1 restore unexpectedly returned a seed grant")
			}
			if len(restored.RevocationPrivateKey) != 0 {
				t.Fatal("legacy backup claimed to contain a revocation key")
			}
		})
	}
}

func testGeneratorBackupV3Material(value byte) generatorBackupMaterial {
	licensePrivateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{value}, ed25519.SeedSize))
	revocationPrivateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{value + 1}, ed25519.SeedSize))
	return generatorBackupMaterial{
		LicensePrivateKey:    licensePrivateKey,
		SeedID:               hex.EncodeToString(bytes.Repeat([]byte{value + 2}, generatorBackupSeedIDSize)),
		SeedKey:              bytes.Repeat([]byte{value + 3}, generatorBackupSeedKeySize),
		RevocationPrivateKey: revocationPrivateKey,
	}
}

func TestPortableBackupRoundTripPreservesSigningKeyAndSeedGrant(t *testing.T) {
	const password = "a long portable password"
	sourceRepository := repositoryWithGeneratedState(t)
	sourceService, err := newGeneratorService(sourceRepository, fixedNow, deterministicRandom())
	if err != nil {
		t.Fatalf("newGeneratorService(source) error = %v", err)
	}
	defer sourceService.Close()

	backup, err := sourceService.CreateBackup(password)
	if err != nil {
		t.Fatalf("CreateBackup() error = %v", err)
	}
	if !strings.HasPrefix(backup, "TCPKEYBACKUP2.") {
		t.Fatalf("CreateBackup() format = %q, want TCPKEYBACKUP2", backup)
	}
	for _, exposed := range []string{
		sourceRepository.state.SeedID,
		base64.RawURLEncoding.EncodeToString(sourceRepository.state.SeedKey),
		hex.EncodeToString(sourceRepository.state.SeedKey),
	} {
		if strings.Contains(backup, exposed) {
			t.Fatal("CreateBackup() exposes plaintext seed material")
		}
	}

	targetRepository := repositoryWithGeneratedState(t)
	targetRepository.state.SeedID = strings.Repeat("b", 32)
	targetRepository.state.SeedKey = bytes.Repeat([]byte{0x29}, 32)
	targetService, err := newGeneratorService(targetRepository, fixedNow, deterministicRandom())
	if err != nil {
		t.Fatalf("newGeneratorService(target) error = %v", err)
	}
	defer targetService.Close()

	if err := targetService.RestoreBackup(backup, password); err != nil {
		t.Fatalf("RestoreBackup() error = %v", err)
	}
	if !bytes.Equal(targetRepository.state.PrivateKey, sourceRepository.state.PrivateKey) ||
		!bytes.Equal(targetRepository.state.PublicKey, sourceRepository.state.PublicKey) {
		t.Fatal("RestoreBackup() did not preserve the signing key pair")
	}
	if targetRepository.state.SeedID != sourceRepository.state.SeedID ||
		!bytes.Equal(targetRepository.state.SeedKey, sourceRepository.state.SeedKey) {
		t.Fatal("RestoreBackup() did not preserve the stable seed grant")
	}
}

func TestRestoreBackupRejectsSeedGrantSplicedFromAnotherEncryptedBackup(t *testing.T) {
	const password = "one shared portable password"
	firstRepository := repositoryWithGeneratedState(t)
	firstService, err := newGeneratorService(firstRepository, fixedNow, deterministicRandom())
	if err != nil {
		t.Fatalf("newGeneratorService(first) error = %v", err)
	}
	defer firstService.Close()
	firstBackup, err := firstService.CreateBackup(password)
	if err != nil {
		t.Fatalf("CreateBackup(first) error = %v", err)
	}

	secondRepository := repositoryWithGeneratedState(t)
	secondRepository.state.SeedID = strings.Repeat("c", 32)
	secondRepository.state.SeedKey = bytes.Repeat([]byte{0x37}, 32)
	secondService, err := newGeneratorService(secondRepository, fixedNow, deterministicRandom())
	if err != nil {
		t.Fatalf("newGeneratorService(second) error = %v", err)
	}
	defer secondService.Close()
	secondBackup, err := secondService.CreateBackup(password)
	if err != nil {
		t.Fatalf("CreateBackup(second) error = %v", err)
	}

	firstParts := strings.Split(firstBackup, ".")
	secondParts := strings.Split(secondBackup, ".")
	if len(firstParts) != 3 || len(secondParts) != 3 {
		t.Fatalf("backup part counts = %d and %d, want 3", len(firstParts), len(secondParts))
	}
	spliced := strings.Join([]string{firstParts[0], firstParts[1], secondParts[2]}, ".")

	targetRepository := repositoryWithGeneratedState(t)
	originalState := cloneGeneratorState(targetRepository.state)
	targetService, err := newGeneratorService(targetRepository, fixedNow, deterministicRandom())
	if err != nil {
		t.Fatalf("newGeneratorService(target) error = %v", err)
	}
	defer targetService.Close()
	if err := targetService.RestoreBackup(spliced, password); !errors.Is(err, licenseissuer.ErrInvalidBackup) {
		t.Fatalf("RestoreBackup(spliced) error = %v, want ErrInvalidBackup", err)
	}
	if !bytes.Equal(targetRepository.state.PrivateKey, originalState.PrivateKey) ||
		targetRepository.state.SeedID != originalState.SeedID ||
		!bytes.Equal(targetRepository.state.SeedKey, originalState.SeedKey) {
		t.Fatal("RestoreBackup(spliced) changed generator state")
	}
}

func TestBootstrapSeedExportRoundTripIsPasswordEncrypted(t *testing.T) {
	const password = "a separate build seed password"
	repository := repositoryWithGeneratedState(t)
	service, err := newGeneratorService(repository, fixedNow, deterministicRandom())
	if err != nil {
		t.Fatalf("newGeneratorService() error = %v", err)
	}
	defer service.Close()

	artifact, err := service.CreateBootstrapSeedExport(password)
	if err != nil {
		t.Fatalf("CreateBootstrapSeedExport() error = %v", err)
	}
	if repository.saveCalls != 0 || len(service.Status().History) != 0 {
		t.Fatal("CreateBootstrapSeedExport() persisted export data or changed issuance history")
	}
	if !strings.HasPrefix(artifact, "TCBUILDSEED1.") {
		t.Fatalf("CreateBootstrapSeedExport() format = %q", artifact)
	}
	for _, exposed := range []string{
		repository.state.SeedID,
		base64.RawURLEncoding.EncodeToString(repository.state.SeedKey),
		hex.EncodeToString(repository.state.SeedKey),
		strings.ToUpper(hex.EncodeToString(repository.state.SeedKey)),
		base64.RawURLEncoding.EncodeToString(repository.state.PrivateKey),
		hex.EncodeToString(repository.state.PrivateKey),
	} {
		if strings.Contains(artifact, exposed) {
			t.Fatal("CreateBootstrapSeedExport() exposes plaintext key material")
		}
	}

	seedID, seedKey, err := restoreBootstrapSeedExport(artifact, password)
	if err != nil {
		t.Fatalf("restoreBootstrapSeedExport() error = %v", err)
	}
	defer wipeBytes(seedKey)
	if seedID != repository.state.SeedID || !bytes.Equal(seedKey, repository.state.SeedKey) {
		t.Fatal("restoreBootstrapSeedExport() did not preserve the seed grant")
	}
	if _, _, err := restoreBootstrapSeedExport(artifact, "a different password"); err == nil {
		t.Fatal("restoreBootstrapSeedExport() accepted a wrong password")
	}
}
