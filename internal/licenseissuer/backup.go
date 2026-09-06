package licenseissuer

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"strings"

	"golang.org/x/crypto/scrypt"
)

const (
	backupFormat                = "TCPKEYBACKUP1"
	scryptN                     = 32768
	scryptR                     = 8
	scryptP                     = 1
	backupKeyLen                = 32
	backupSaltLen               = 16
	backupNonceLen              = 12
	backupTagLen                = 16
	backupPasswordMinimumLength = 12
)

var (
	// ErrPasswordRequired is returned when no portable-backup password is supplied.
	ErrPasswordRequired = errors.New("licenseissuer: backup password required")
	// ErrPasswordTooShort prevents trivially guessable offline backup encryption.
	ErrPasswordTooShort = errors.New("licenseissuer: backup password must contain at least 12 characters")
	// ErrInvalidBackup intentionally covers malformed data, authentication
	// failure, and a wrong password without distinguishing between them.
	ErrInvalidBackup = errors.New("licenseissuer: invalid encrypted backup")
)

// BackupPrivateKey encrypts an Ed25519 private key into a portable
// TCPKEYBACKUP1 envelope.
func BackupPrivateKey(privateKey ed25519.PrivateKey, password string) (string, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return "", ErrInvalidPrivateKey
	}
	if password == "" {
		return "", ErrPasswordRequired
	}
	if len([]rune(password)) < backupPasswordMinimumLength {
		return "", ErrPasswordTooShort
	}

	salt := make([]byte, backupSaltLen)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return "", errors.New("licenseissuer: random source unavailable")
	}
	nonce := make([]byte, backupNonceLen)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", errors.New("licenseissuer: random source unavailable")
	}

	key, err := scrypt.Key([]byte(password), salt, scryptN, scryptR, scryptP, backupKeyLen)
	if err != nil {
		return "", errors.New("licenseissuer: key derivation failed")
	}
	defer wipe(key)
	aead, err := backupAEAD(key)
	if err != nil {
		return "", errors.New("licenseissuer: encryption unavailable")
	}
	ciphertext := aead.Seal(nil, nonce, privateKey, []byte(backupFormat))

	return strings.Join([]string{
		backupFormat,
		base64.RawURLEncoding.EncodeToString(salt),
		base64.RawURLEncoding.EncodeToString(nonce),
		base64.RawURLEncoding.EncodeToString(ciphertext),
	}, "."), nil
}

// RestorePrivateKey decrypts and authenticates a portable TCPKEYBACKUP1
// envelope. Wrong passwords and tampering return the same safe error.
func RestorePrivateKey(backup, password string) (ed25519.PrivateKey, error) {
	if password == "" {
		return nil, ErrPasswordRequired
	}
	salt, nonce, ciphertext, ok := parseBackup(backup)
	if !ok {
		return nil, ErrInvalidBackup
	}

	key, err := scrypt.Key([]byte(password), salt, scryptN, scryptR, scryptP, backupKeyLen)
	if err != nil {
		return nil, ErrInvalidBackup
	}
	defer wipe(key)
	aead, err := backupAEAD(key)
	if err != nil {
		return nil, ErrInvalidBackup
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, []byte(backupFormat))
	if err != nil || len(plaintext) != ed25519.PrivateKeySize {
		wipe(plaintext)
		return nil, ErrInvalidBackup
	}

	return ed25519.PrivateKey(plaintext), nil
}

func parseBackup(backup string) (salt, nonce, ciphertext []byte, ok bool) {
	parts := strings.Split(backup, ".")
	if len(parts) != 4 || parts[0] != backupFormat {
		return nil, nil, nil, false
	}

	salt, ok = decodeBackupPart(parts[1], backupSaltLen)
	if !ok {
		return nil, nil, nil, false
	}
	nonce, ok = decodeBackupPart(parts[2], backupNonceLen)
	if !ok {
		return nil, nil, nil, false
	}
	ciphertext, ok = decodeBackupPart(parts[3], ed25519.PrivateKeySize+backupTagLen)
	if !ok {
		return nil, nil, nil, false
	}

	return salt, nonce, ciphertext, true
}

func decodeBackupPart(value string, size int) ([]byte, bool) {
	if len(value) != base64.RawURLEncoding.EncodedLen(size) {
		return nil, false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) != size || base64.RawURLEncoding.EncodeToString(decoded) != value {
		return nil, false
	}
	return decoded, true
}

func backupAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func wipe(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
