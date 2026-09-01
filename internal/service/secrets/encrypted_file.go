package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	keyring "github.com/zalando/go-keyring"
)

const encryptedFileService = "telegram-companion-public-v1"

var encryptedFileAAD = []byte("telegram-companion/encrypted-secret-store/v1")

type encryptedFileEnvelope struct {
	Version    int    `json:"version"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

type encryptedFileBackend struct {
	path   string
	key    [32]byte
	random io.Reader
}

// NewEncryptedFileSecretStore creates a SecretStore that does not depend on
// the platform keyring. The credential is normally the signed license token.
func NewEncryptedFileSecretStore(path, credential string) (*SecretStore, error) {
	path = strings.TrimSpace(path)
	credential = strings.TrimSpace(credential)
	if path == "" {
		return nil, errors.New("encrypted secret store path is required")
	}
	if credential == "" {
		return nil, errors.New("encrypted secret store credential is required")
	}

	key := sha256.Sum256(append(append([]byte(nil), encryptedFileAAD...), []byte("\x00"+credential)...))
	backend := &encryptedFileBackend{path: path, key: key, random: rand.Reader}
	return newSecretStore(encryptedFileService, backend, rand.Reader), nil
}

func (b *encryptedFileBackend) Get(service, name string) (string, error) {
	values, err := b.read()
	if err != nil {
		return "", err
	}
	value, ok := values[encryptedFileKey(service, name)]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return value, nil
}

func (b *encryptedFileBackend) Set(service, name, value string) error {
	values, err := b.read()
	if errors.Is(err, keyring.ErrNotFound) {
		values = make(map[string]string)
	} else if err != nil {
		return err
	}
	values[encryptedFileKey(service, name)] = value
	return b.write(values)
}

func (b *encryptedFileBackend) Delete(service, name string) error {
	values, err := b.read()
	if err != nil {
		return err
	}
	key := encryptedFileKey(service, name)
	if _, ok := values[key]; !ok {
		return keyring.ErrNotFound
	}
	delete(values, key)
	return b.write(values)
}

func (b *encryptedFileBackend) read() (map[string]string, error) {
	raw, err := os.ReadFile(b.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, keyring.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read encrypted secret store: %w", err)
	}

	var envelope encryptedFileEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Version != 1 {
		return nil, errors.New("decode encrypted secret store")
	}
	nonce, err := base64.RawStdEncoding.DecodeString(envelope.Nonce)
	if err != nil {
		return nil, errors.New("decode encrypted secret store nonce")
	}
	ciphertext, err := base64.RawStdEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return nil, errors.New("decode encrypted secret store ciphertext")
	}
	aead, err := b.aead()
	if err != nil {
		return nil, err
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, encryptedFileAAD)
	if err != nil {
		return nil, errors.New("decrypt encrypted secret store")
	}
	defer clear(plaintext)

	values := make(map[string]string)
	if err := json.Unmarshal(plaintext, &values); err != nil {
		return nil, errors.New("decode encrypted secret store values")
	}
	return values, nil
}

func (b *encryptedFileBackend) write(values map[string]string) error {
	plaintext, err := json.Marshal(values)
	if err != nil {
		return errors.New("encode encrypted secret store values")
	}
	defer clear(plaintext)
	aead, err := b.aead()
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(b.random, nonce); err != nil {
		return fmt.Errorf("generate encrypted secret store nonce: %w", err)
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, encryptedFileAAD)
	envelope, err := json.Marshal(encryptedFileEnvelope{
		Version:    1,
		Nonce:      base64.RawStdEncoding.EncodeToString(nonce),
		Ciphertext: base64.RawStdEncoding.EncodeToString(ciphertext),
	})
	if err != nil {
		return errors.New("encode encrypted secret store")
	}

	dir := filepath.Dir(b.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create encrypted secret store directory: %w", err)
	}
	temp, err := os.CreateTemp(dir, ".runtime-secrets-*")
	if err != nil {
		return fmt.Errorf("create encrypted secret store temporary file: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("secure encrypted secret store temporary file: %w", err)
	}
	if _, err := temp.Write(envelope); err != nil {
		temp.Close()
		return fmt.Errorf("write encrypted secret store: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("sync encrypted secret store: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close encrypted secret store: %w", err)
	}
	if err := os.Rename(tempPath, b.path); err != nil {
		return fmt.Errorf("replace encrypted secret store: %w", err)
	}
	return nil
}

func (b *encryptedFileBackend) aead() (cipher.AEAD, error) {
	block, err := aes.NewCipher(b.key[:])
	if err != nil {
		return nil, errors.New("initialize encrypted secret store")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("initialize encrypted secret store authentication")
	}
	return aead, nil
}

func encryptedFileKey(service, name string) string {
	return service + "\x00" + name
}
