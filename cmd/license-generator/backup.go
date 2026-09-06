package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"

	"telegram-companion/internal/licenseissuer"
)

const (
	generatorBackupFormat      = "TCPKEYBACKUP2"
	generatorBackupV3Format    = "TCPKEYBACKUP3"
	generatorBackupSeedIDSize  = 16
	generatorBackupSeedKeySize = 32
	generatorBackupBindingSize = 16
	bootstrapSeedExportFormat  = "TCBUILDSEED1"
)

type generatorBackupMaterial struct {
	LicensePrivateKey    ed25519.PrivateKey
	SeedID               string
	SeedKey              []byte
	RevocationPrivateKey ed25519.PrivateKey
}

func createGeneratorBackupV3(material generatorBackupMaterial, password string) (string, error) {
	if !validGeneratorBackupPrivateKey(material.LicensePrivateKey) ||
		!validSeedGrant(material.SeedID, material.SeedKey) ||
		!validGeneratorBackupPrivateKey(material.RevocationPrivateKey) {
		return "", ErrGeneratorInitialization
	}
	licenseBackup, err := licenseissuer.BackupPrivateKey(material.LicensePrivateKey, password)
	if err != nil {
		return "", err
	}
	revocationBackup, err := licenseissuer.BackupPrivateKey(material.RevocationPrivateKey, password)
	if err != nil {
		return "", err
	}
	seedIDBytes, err := hex.DecodeString(material.SeedID)
	if err != nil || len(seedIDBytes) != generatorBackupSeedIDSize {
		return "", ErrGeneratorInitialization
	}
	seedMaterial := make(ed25519.PrivateKey, ed25519.PrivateKeySize)
	defer wipeBytes(seedMaterial)
	copy(seedMaterial, seedIDBytes)
	copy(seedMaterial[generatorBackupSeedIDSize:], material.SeedKey)
	binding := generatorBackupV3Binding(material)
	defer wipeBytes(binding)
	copy(seedMaterial[generatorBackupSeedIDSize+generatorBackupSeedKeySize:], binding)
	seedBackup, err := licenseissuer.BackupPrivateKey(seedMaterial, password)
	if err != nil {
		return "", err
	}
	return strings.Join([]string{
		generatorBackupV3Format,
		base64.RawURLEncoding.EncodeToString([]byte(licenseBackup)),
		base64.RawURLEncoding.EncodeToString([]byte(seedBackup)),
		base64.RawURLEncoding.EncodeToString([]byte(revocationBackup)),
	}, "."), nil
}

func restoreGeneratorBackupMaterial(backup, password string) (generatorBackupMaterial, error) {
	if !strings.HasPrefix(backup, generatorBackupV3Format+".") {
		privateKey, seedID, seedKey, err := restoreGeneratorBackup(backup, password)
		if err != nil {
			return generatorBackupMaterial{}, err
		}
		return generatorBackupMaterial{
			LicensePrivateKey: privateKey,
			SeedID:            seedID,
			SeedKey:           seedKey,
		}, nil
	}

	parts := strings.Split(backup, ".")
	if len(parts) != 4 || parts[0] != generatorBackupV3Format {
		return generatorBackupMaterial{}, licenseissuer.ErrInvalidBackup
	}
	licenseBackup, ok := decodeGeneratorBackupPart(parts[1])
	if !ok {
		return generatorBackupMaterial{}, licenseissuer.ErrInvalidBackup
	}
	seedBackup, ok := decodeGeneratorBackupPart(parts[2])
	if !ok {
		return generatorBackupMaterial{}, licenseissuer.ErrInvalidBackup
	}
	revocationBackup, ok := decodeGeneratorBackupPart(parts[3])
	if !ok {
		return generatorBackupMaterial{}, licenseissuer.ErrInvalidBackup
	}

	licensePrivateKey, err := licenseissuer.RestorePrivateKey(licenseBackup, password)
	if err != nil {
		return generatorBackupMaterial{}, err
	}
	adoptedLicenseKey := false
	defer func() {
		if !adoptedLicenseKey {
			wipeBytes(licensePrivateKey)
		}
	}()
	seedMaterial, err := licenseissuer.RestorePrivateKey(seedBackup, password)
	if err != nil {
		return generatorBackupMaterial{}, err
	}
	defer wipeBytes(seedMaterial)
	revocationPrivateKey, err := licenseissuer.RestorePrivateKey(revocationBackup, password)
	if err != nil {
		return generatorBackupMaterial{}, err
	}
	adoptedRevocationKey := false
	defer func() {
		if !adoptedRevocationKey {
			wipeBytes(revocationPrivateKey)
		}
	}()

	seedIDBytes := seedMaterial[:generatorBackupSeedIDSize]
	seedID := hex.EncodeToString(seedIDBytes)
	seedKey := append([]byte(nil), seedMaterial[generatorBackupSeedIDSize:generatorBackupSeedIDSize+generatorBackupSeedKeySize]...)
	material := generatorBackupMaterial{
		LicensePrivateKey:    licensePrivateKey,
		SeedID:               seedID,
		SeedKey:              seedKey,
		RevocationPrivateKey: revocationPrivateKey,
	}
	wantBinding := generatorBackupV3Binding(material)
	bindingValid := subtle.ConstantTimeCompare(seedMaterial[generatorBackupSeedIDSize+generatorBackupSeedKeySize:], wantBinding) == 1
	wipeBytes(wantBinding)
	if !validGeneratorBackupPrivateKey(licensePrivateKey) || !validSeedGrant(seedID, seedKey) ||
		!validGeneratorBackupPrivateKey(revocationPrivateKey) || !bindingValid {
		wipeBytes(seedKey)
		return generatorBackupMaterial{}, licenseissuer.ErrInvalidBackup
	}
	adoptedLicenseKey = true
	adoptedRevocationKey = true
	return material, nil
}

func generatorBackupV3Binding(material generatorBackupMaterial) []byte {
	seedID, err := hex.DecodeString(material.SeedID)
	if err != nil {
		return nil
	}
	digest := sha256.New()
	_, _ = digest.Write([]byte(generatorBackupV3Format))
	_, _ = digest.Write(material.LicensePrivateKey)
	_, _ = digest.Write(seedID)
	_, _ = digest.Write(material.SeedKey)
	_, _ = digest.Write(material.RevocationPrivateKey)
	return digest.Sum(nil)[:generatorBackupBindingSize]
}

func validGeneratorBackupPrivateKey(privateKey ed25519.PrivateKey) bool {
	if len(privateKey) != ed25519.PrivateKeySize {
		return false
	}
	canonical := derivePrivateKeyFromCopiedSeed(privateKey, ed25519.NewKeyFromSeed)
	defer wipeBytes(canonical)
	return subtle.ConstantTimeCompare(canonical, privateKey) == 1
}

func wipeGeneratorBackupMaterial(material *generatorBackupMaterial) {
	if material == nil {
		return
	}
	wipeBytes(material.LicensePrivateKey)
	wipeBytes(material.SeedKey)
	wipeBytes(material.RevocationPrivateKey)
	material.LicensePrivateKey = nil
	material.SeedKey = nil
	material.RevocationPrivateKey = nil
	material.SeedID = ""
}

func createGeneratorBackup(privateKey ed25519.PrivateKey, seedID string, seedKey []byte, password string) (string, error) {
	if !validSeedGrant(seedID, seedKey) {
		return "", ErrGeneratorInitialization
	}
	signingBackup, err := licenseissuer.BackupPrivateKey(privateKey, password)
	if err != nil {
		return "", err
	}

	seedMaterial := make(ed25519.PrivateKey, ed25519.PrivateKeySize)
	defer wipeBytes(seedMaterial)
	seedIDBytes, err := hex.DecodeString(seedID)
	if err != nil || len(seedIDBytes) != generatorBackupSeedIDSize {
		return "", ErrGeneratorInitialization
	}
	copy(seedMaterial, seedIDBytes)
	copy(seedMaterial[generatorBackupSeedIDSize:], seedKey)
	binding := generatorBackupBinding(privateKey, seedIDBytes, seedKey)
	defer wipeBytes(binding)
	copy(seedMaterial[generatorBackupSeedIDSize+generatorBackupSeedKeySize:], binding)
	seedBackup, err := licenseissuer.BackupPrivateKey(seedMaterial, password)
	if err != nil {
		return "", err
	}

	return strings.Join([]string{
		generatorBackupFormat,
		base64.RawURLEncoding.EncodeToString([]byte(signingBackup)),
		base64.RawURLEncoding.EncodeToString([]byte(seedBackup)),
	}, "."), nil
}

func restoreGeneratorBackup(backup, password string) (ed25519.PrivateKey, string, []byte, error) {
	if !strings.HasPrefix(backup, generatorBackupFormat+".") {
		privateKey, err := licenseissuer.RestorePrivateKey(backup, password)
		return privateKey, "", nil, err
	}

	parts := strings.Split(backup, ".")
	if len(parts) != 3 || parts[0] != generatorBackupFormat {
		return nil, "", nil, licenseissuer.ErrInvalidBackup
	}
	signingBackup, ok := decodeGeneratorBackupPart(parts[1])
	if !ok {
		return nil, "", nil, licenseissuer.ErrInvalidBackup
	}
	seedBackup, ok := decodeGeneratorBackupPart(parts[2])
	if !ok {
		return nil, "", nil, licenseissuer.ErrInvalidBackup
	}

	privateKey, err := licenseissuer.RestorePrivateKey(signingBackup, password)
	if err != nil {
		return nil, "", nil, err
	}
	seedMaterial, err := licenseissuer.RestorePrivateKey(seedBackup, password)
	if err != nil {
		wipeBytes(privateKey)
		return nil, "", nil, err
	}
	seedIDBytes := seedMaterial[:generatorBackupSeedIDSize]
	seedID := hex.EncodeToString(seedIDBytes)
	seedKey := append([]byte(nil), seedMaterial[generatorBackupSeedIDSize:generatorBackupSeedIDSize+generatorBackupSeedKeySize]...)
	wantBinding := generatorBackupBinding(privateKey, seedIDBytes, seedKey)
	bindingValid := subtle.ConstantTimeCompare(seedMaterial[generatorBackupSeedIDSize+generatorBackupSeedKeySize:], wantBinding) == 1
	wipeBytes(wantBinding)
	wipeBytes(seedMaterial)
	if !validSeedGrant(seedID, seedKey) || !bindingValid {
		wipeBytes(privateKey)
		wipeBytes(seedKey)
		return nil, "", nil, licenseissuer.ErrInvalidBackup
	}
	return privateKey, seedID, seedKey, nil
}

func createBootstrapSeedExport(seedID string, seedKey []byte, password string) (string, error) {
	artifact, err := licenseissuer.CreateBootstrapSeedExport(seedID, seedKey, password)
	if errors.Is(err, licenseissuer.ErrInvalidSeedGrant) {
		return "", ErrGeneratorInitialization
	}
	return artifact, err
}

func restoreBootstrapSeedExport(artifact, password string) (string, []byte, error) {
	return licenseissuer.RestoreBootstrapSeedExport(artifact, password)
}

func generatorBackupBinding(privateKey ed25519.PrivateKey, seedID, seedKey []byte) []byte {
	digest := sha256.New()
	_, _ = digest.Write([]byte(generatorBackupFormat))
	_, _ = digest.Write(privateKey)
	_, _ = digest.Write(seedID)
	_, _ = digest.Write(seedKey)
	return digest.Sum(nil)[:generatorBackupBindingSize]
}

func decodeGeneratorBackupPart(value string) (string, bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) == 0 || base64.RawURLEncoding.EncodeToString(decoded) != value {
		return "", false
	}
	return string(decoded), true
}
