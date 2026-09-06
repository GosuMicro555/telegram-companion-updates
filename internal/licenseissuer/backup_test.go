package licenseissuer

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/scrypt"
)

func TestBackupPrivateKeyUsesSpecifiedAuthenticatedFormat(t *testing.T) {
	_, privateKey, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	const password = "correct horse battery staple"

	backup, err := BackupPrivateKey(privateKey, password)
	if err != nil {
		t.Fatalf("BackupPrivateKey() error = %v", err)
	}
	parts := strings.Split(backup, ".")
	if len(parts) != 4 || parts[0] != "TCPKEYBACKUP1" {
		t.Fatalf("BackupPrivateKey() format = %q", backup)
	}

	salt := decodeBackupTestPart(t, parts[1])
	nonce := decodeBackupTestPart(t, parts[2])
	ciphertext := decodeBackupTestPart(t, parts[3])
	if len(salt) != 16 {
		t.Fatalf("salt length = %d, want 16", len(salt))
	}
	if len(nonce) != 12 {
		t.Fatalf("nonce length = %d, want 12", len(nonce))
	}

	key, err := scrypt.Key([]byte(password), salt, 32768, 8, 1, 32)
	if err != nil {
		t.Fatalf("scrypt.Key() error = %v", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes.NewCipher() error = %v", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("cipher.NewGCM() error = %v", err)
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, []byte("TCPKEYBACKUP1"))
	if err != nil {
		t.Fatalf("Open() with the literal format AAD error = %v", err)
	}
	if !bytes.Equal(plaintext, privateKey) {
		t.Fatal("decrypted backup does not contain the original private key")
	}
}

func TestBackupPrivateKeyRoundTrip(t *testing.T) {
	_, privateKey, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}

	backup, err := BackupPrivateKey(privateKey, "portable backup password")
	if err != nil {
		t.Fatalf("BackupPrivateKey() error = %v", err)
	}
	restored, err := RestorePrivateKey(backup, "portable backup password")
	if err != nil {
		t.Fatalf("RestorePrivateKey() error = %v", err)
	}
	if !bytes.Equal(restored, privateKey) {
		t.Fatal("RestorePrivateKey() did not return the original key")
	}
}

func TestBackupPrivateKeyUsesFreshSaltAndNonce(t *testing.T) {
	_, privateKey, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}

	first, err := BackupPrivateKey(privateKey, "same password")
	if err != nil {
		t.Fatalf("first BackupPrivateKey() error = %v", err)
	}
	second, err := BackupPrivateKey(privateKey, "same password")
	if err != nil {
		t.Fatalf("second BackupPrivateKey() error = %v", err)
	}
	firstParts := strings.Split(first, ".")
	secondParts := strings.Split(second, ".")
	if firstParts[1] == secondParts[1] {
		t.Fatal("BackupPrivateKey() reused its salt")
	}
	if firstParts[2] == secondParts[2] {
		t.Fatal("BackupPrivateKey() reused its nonce")
	}
	if first == second {
		t.Fatal("BackupPrivateKey() returned identical randomized backups")
	}
}

func TestBackupPrivateKeyDoesNotExposePlaintextKey(t *testing.T) {
	_, privateKey, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}

	backup, err := BackupPrivateKey(privateKey, "hidden key password")
	if err != nil {
		t.Fatalf("BackupPrivateKey() error = %v", err)
	}
	parts := strings.Split(backup, ".")
	ciphertext := decodeBackupTestPart(t, parts[3])
	if bytes.Contains([]byte(backup), privateKey) || bytes.Contains(ciphertext, privateKey) {
		t.Fatal("backup contains the plaintext private key")
	}
	for _, encoding := range []string{
		base64.RawURLEncoding.EncodeToString(privateKey),
		base64.StdEncoding.EncodeToString(privateKey),
	} {
		if strings.Contains(backup, encoding) {
			t.Fatal("backup contains an encoded plaintext private key")
		}
	}
}

func TestRestorePrivateKeyRejectsWrongPasswordWithSafeError(t *testing.T) {
	_, privateKey, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	const password = "right-password-secret"
	backup, err := BackupPrivateKey(privateKey, password)
	if err != nil {
		t.Fatalf("BackupPrivateKey() error = %v", err)
	}

	const wrongPassword = "wrong-password-secret"
	restored, err := RestorePrivateKey(backup, wrongPassword)
	assertInvalidBackupError(t, restored, err, backup, password, wrongPassword)
}

func TestRestorePrivateKeyRejectsTamperingWithSafeError(t *testing.T) {
	_, privateKey, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	const password = "tamper-test-password"
	backup, err := BackupPrivateKey(privateKey, password)
	if err != nil {
		t.Fatalf("BackupPrivateKey() error = %v", err)
	}

	parts := strings.Split(backup, ".")
	ciphertext := decodeBackupTestPart(t, parts[3])
	ciphertext[len(ciphertext)-1] ^= 0x01
	parts[3] = base64.RawURLEncoding.EncodeToString(ciphertext)
	tampered := strings.Join(parts, ".")

	restored, err := RestorePrivateKey(tampered, password)
	assertInvalidBackupError(t, restored, err, tampered, password)
}

func TestBackupPrivateKeyRejectsMissingPasswordAndInvalidKey(t *testing.T) {
	_, privateKey, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}

	if _, err := BackupPrivateKey(privateKey, ""); !errors.Is(err, ErrPasswordRequired) {
		t.Fatalf("BackupPrivateKey(empty password) error = %v, want ErrPasswordRequired", err)
	}
	if _, err := BackupPrivateKey(privateKey, "too-short"); !errors.Is(err, ErrPasswordTooShort) {
		t.Fatalf("BackupPrivateKey(short password) error = %v, want ErrPasswordTooShort", err)
	}
	if _, err := BackupPrivateKey(ed25519.PrivateKey("short"), "password"); !errors.Is(err, ErrInvalidPrivateKey) {
		t.Fatalf("BackupPrivateKey(short key) error = %v, want ErrInvalidPrivateKey", err)
	}
}

func TestRestorePrivateKeyRejectsMalformedEnvelope(t *testing.T) {
	tests := []string{
		"",
		"TCPKEYBACKUP2.AA.AA.AA",
		"TCPKEYBACKUP1.AA.AA",
		"TCPKEYBACKUP1.!.AA.AA",
		"TCPKEYBACKUP1.AA.AA.AA.extra",
	}

	for _, backup := range tests {
		t.Run(backup, func(t *testing.T) {
			restored, err := RestorePrivateKey(backup, "password")
			assertInvalidBackupError(t, restored, err, backup, "password")
		})
	}
}

func decodeBackupTestPart(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		t.Fatalf("DecodeString(%q) error = %v", value, err)
	}
	if base64.RawURLEncoding.EncodeToString(decoded) != value {
		t.Fatalf("backup part %q is not canonical raw URL base64", value)
	}
	return decoded
}

func assertInvalidBackupError(t *testing.T, restored ed25519.PrivateKey, err error, backup string, secrets ...string) {
	t.Helper()
	if restored != nil {
		t.Fatal("RestorePrivateKey() returned key material on failure")
	}
	if !errors.Is(err, ErrInvalidBackup) {
		t.Fatalf("RestorePrivateKey() error = %v, want ErrInvalidBackup", err)
	}
	errorText := err.Error()
	if backup != "" && strings.Contains(errorText, backup) {
		t.Fatal("RestorePrivateKey() error exposes the backup")
	}
	for _, secret := range secrets {
		if secret != "" && strings.Contains(errorText, secret) {
			t.Fatal("RestorePrivateKey() error exposes a password")
		}
	}
}
