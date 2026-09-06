package licenseissuer

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

const (
	bootstrapSeedExportFormat = "TCBUILDSEED1"
	bootstrapSeedIDSize       = 16
	bootstrapSeedKeySize      = 32
	bootstrapSeedBindingSize  = 16
)

// ErrInvalidSeedGrant is returned when a bootstrap seed ID or key is invalid.
var ErrInvalidSeedGrant = errors.New("licenseissuer: invalid seed grant")

// CreateBootstrapSeedExport encrypts a seed grant into the compatible
// TCBUILDSEED1 operator artifact format.
func CreateBootstrapSeedExport(seedID string, seedKey []byte, password string) (string, error) {
	seedIDBytes, ok := parseBootstrapSeedGrant(seedID, seedKey)
	if !ok {
		return "", ErrInvalidSeedGrant
	}

	seedMaterial := make(ed25519.PrivateKey, ed25519.PrivateKeySize)
	defer wipe(seedMaterial)
	copy(seedMaterial, seedIDBytes)
	copy(seedMaterial[bootstrapSeedIDSize:], seedKey)
	binding := bootstrapSeedExportBinding(seedIDBytes, seedKey)
	defer wipe(binding)
	copy(seedMaterial[bootstrapSeedIDSize+bootstrapSeedKeySize:], binding)

	encrypted, err := BackupPrivateKey(seedMaterial, password)
	if err != nil {
		return "", err
	}
	return bootstrapSeedExportFormat + "." + base64.RawURLEncoding.EncodeToString([]byte(encrypted)), nil
}

// RestoreBootstrapSeedExport decrypts a compatible TCBUILDSEED1 artifact and
// returns its authenticated seed grant.
func RestoreBootstrapSeedExport(artifact, password string) (string, []byte, error) {
	parts := strings.Split(artifact, ".")
	if len(parts) != 2 || parts[0] != bootstrapSeedExportFormat {
		return "", nil, ErrInvalidBackup
	}
	encrypted, ok := decodeBootstrapSeedExportPart(parts[1])
	if !ok {
		return "", nil, ErrInvalidBackup
	}

	seedMaterial, err := RestorePrivateKey(encrypted, password)
	if err != nil {
		return "", nil, err
	}
	seedIDBytes := seedMaterial[:bootstrapSeedIDSize]
	seedID := hex.EncodeToString(seedIDBytes)
	seedKey := append([]byte(nil), seedMaterial[bootstrapSeedIDSize:bootstrapSeedIDSize+bootstrapSeedKeySize]...)
	wantBinding := bootstrapSeedExportBinding(seedIDBytes, seedKey)
	bindingValid := subtle.ConstantTimeCompare(seedMaterial[bootstrapSeedIDSize+bootstrapSeedKeySize:], wantBinding) == 1
	wipe(wantBinding)
	wipe(seedMaterial)
	if !bindingValid || !validBootstrapSeedGrant(seedID, seedKey) {
		wipe(seedKey)
		return "", nil, ErrInvalidBackup
	}
	return seedID, seedKey, nil
}

func parseBootstrapSeedGrant(seedID string, seedKey []byte) ([]byte, bool) {
	if !validBootstrapSeedGrant(seedID, seedKey) {
		return nil, false
	}
	seedIDBytes, err := hex.DecodeString(seedID)
	if err != nil {
		return nil, false
	}
	return seedIDBytes, true
}

func validBootstrapSeedGrant(seedID string, seedKey []byte) bool {
	if len(seedKey) != bootstrapSeedKeySize || len(seedID) != bootstrapSeedIDSize*2 {
		return false
	}
	seedIDBytes, err := hex.DecodeString(seedID)
	return err == nil && len(seedIDBytes) == bootstrapSeedIDSize && hex.EncodeToString(seedIDBytes) == seedID
}

func bootstrapSeedExportBinding(seedID, seedKey []byte) []byte {
	digest := sha256.New()
	_, _ = digest.Write([]byte(bootstrapSeedExportFormat))
	_, _ = digest.Write(seedID)
	_, _ = digest.Write(seedKey)
	return digest.Sum(nil)[:bootstrapSeedBindingSize]
}

func decodeBootstrapSeedExportPart(value string) (string, bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) == 0 || base64.RawURLEncoding.EncodeToString(decoded) != value {
		return "", false
	}
	return string(decoded), true
}
