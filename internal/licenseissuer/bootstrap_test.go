package licenseissuer

import (
	"bytes"
	"crypto/ed25519"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func TestBootstrapSeedExportRoundTripPreservesSeedGrant(t *testing.T) {
	const (
		seedID   = "0123456789abcdef0123456789abcdef"
		password = "a separate build seed password"
	)
	seedKey := bytes.Repeat([]byte{0x29}, 32)

	artifact, err := CreateBootstrapSeedExport(seedID, seedKey, password)
	if err != nil {
		t.Fatalf("CreateBootstrapSeedExport() error = %v", err)
	}
	parts := strings.Split(artifact, ".")
	if len(parts) != 2 || parts[0] != "TCBUILDSEED1" {
		t.Fatalf("CreateBootstrapSeedExport() format = %q, want TCBUILDSEED1 envelope", artifact)
	}
	if decoded, err := base64.RawURLEncoding.DecodeString(parts[1]); err != nil ||
		base64.RawURLEncoding.EncodeToString(decoded) != parts[1] {
		t.Fatalf("CreateBootstrapSeedExport() payload is not canonical raw URL base64: %q", parts[1])
	}

	restoredID, restoredKey, err := RestoreBootstrapSeedExport(artifact, password)
	if err != nil {
		t.Fatalf("RestoreBootstrapSeedExport() error = %v", err)
	}
	if restoredID != seedID || !bytes.Equal(restoredKey, seedKey) {
		t.Fatalf("RestoreBootstrapSeedExport() = (%q, %x), want (%q, %x)", restoredID, restoredKey, seedID, seedKey)
	}
}

func TestBootstrapSeedExportRejectsInvalidSeedGrant(t *testing.T) {
	tests := []struct {
		name    string
		seedID  string
		seedKey []byte
	}{
		{name: "short seed id", seedID: "0123456789abcdef", seedKey: bytes.Repeat([]byte{1}, 32)},
		{name: "uppercase seed id", seedID: "0123456789ABCDEF0123456789abcdef", seedKey: bytes.Repeat([]byte{1}, 32)},
		{name: "short seed key", seedID: "0123456789abcdef0123456789abcdef", seedKey: bytes.Repeat([]byte{1}, 31)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := CreateBootstrapSeedExport(test.seedID, test.seedKey, "portable password"); !errors.Is(err, ErrInvalidSeedGrant) {
				t.Fatalf("CreateBootstrapSeedExport() error = %v, want ErrInvalidSeedGrant", err)
			}
		})
	}
}

func TestBootstrapSeedExportRejectsWrongPasswordAndTampering(t *testing.T) {
	fixedRandom, err := hex.DecodeString("e0d11540c44213f481abacdfb4474601c874935cac2636d095ef8336")
	if err != nil {
		t.Fatalf("hex.DecodeString(test random fixture) error = %v", err)
	}
	originalRandomReader := cryptorand.Reader
	cryptorand.Reader = bytes.NewReader(fixedRandom)
	t.Cleanup(func() {
		cryptorand.Reader = originalRandomReader
	})

	artifact, err := CreateBootstrapSeedExport(
		"0123456789abcdef0123456789abcdef",
		bytes.Repeat([]byte{0x37}, 32),
		"portable build password",
	)
	if err != nil {
		t.Fatalf("CreateBootstrapSeedExport() error = %v", err)
	}

	if _, _, err := RestoreBootstrapSeedExport(artifact, "wrong build password"); !errors.Is(err, ErrInvalidBackup) {
		t.Fatalf("RestoreBootstrapSeedExport(wrong password) error = %v, want ErrInvalidBackup", err)
	}

	parts := strings.Split(artifact, ".")
	if parts[1][len(parts[1])-1] != 'A' {
		t.Fatalf("deterministic test artifact suffix = %q, want A", parts[1][len(parts[1])-1])
	}
	encrypted, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("DecodeString(bootstrap payload) error = %v", err)
	}
	encryptedParts := strings.Split(string(encrypted), ".")
	if len(encryptedParts) != 4 {
		t.Fatalf("encrypted backup has %d parts, want 4", len(encryptedParts))
	}
	ciphertext := decodeBackupTestPart(t, encryptedParts[3])
	ciphertext[len(ciphertext)-1] ^= 0x01
	encryptedParts[3] = base64.RawURLEncoding.EncodeToString(ciphertext)
	tamperedEncrypted := strings.Join(encryptedParts, ".")
	tampered := parts[0] + "." + base64.RawURLEncoding.EncodeToString([]byte(tamperedEncrypted))
	if tampered == artifact {
		t.Fatal("ciphertext tampering did not change the bootstrap artifact")
	}
	if _, _, err := RestoreBootstrapSeedExport(tampered, "portable build password"); !errors.Is(err, ErrInvalidBackup) {
		t.Fatalf("RestoreBootstrapSeedExport(tampered) error = %v, want ErrInvalidBackup", err)
	}
}

func TestBootstrapSeedExportRestoresLegacyTCBuildSeed1Artifact(t *testing.T) {
	const (
		seedID   = "fedcba9876543210fedcba9876543210"
		password = "legacy compatibility password"
	)
	seedIDBytes, err := hex.DecodeString(seedID)
	if err != nil {
		t.Fatalf("hex.DecodeString() error = %v", err)
	}
	seedKey := bytes.Repeat([]byte{0x4c}, 32)
	seedMaterial := make(ed25519.PrivateKey, ed25519.PrivateKeySize)
	copy(seedMaterial, seedIDBytes)
	copy(seedMaterial[len(seedIDBytes):], seedKey)
	copy(seedMaterial[len(seedIDBytes)+len(seedKey):], bootstrapSeedExportTestBinding(seedIDBytes, seedKey))
	legacyEncrypted, err := BackupPrivateKey(seedMaterial, password)
	if err != nil {
		t.Fatalf("BackupPrivateKey() error = %v", err)
	}
	legacyArtifact := "TCBUILDSEED1." + base64.RawURLEncoding.EncodeToString([]byte(legacyEncrypted))

	restoredID, restoredKey, err := RestoreBootstrapSeedExport(legacyArtifact, password)
	if err != nil {
		t.Fatalf("RestoreBootstrapSeedExport() error = %v", err)
	}
	if restoredID != seedID || !bytes.Equal(restoredKey, seedKey) {
		t.Fatalf("RestoreBootstrapSeedExport() = (%q, %x), want (%q, %x)", restoredID, restoredKey, seedID, seedKey)
	}
}

func bootstrapSeedExportTestBinding(seedID, seedKey []byte) []byte {
	digest := sha256.New()
	_, _ = digest.Write([]byte("TCBUILDSEED1"))
	_, _ = digest.Write(seedID)
	_, _ = digest.Write(seedKey)
	return digest.Sum(nil)[:16]
}
