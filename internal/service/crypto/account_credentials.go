package crypto

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"telegram-companion/internal/domain"
)

const accountCredentialsKeySize = 32

type AppCredentials struct {
	AppID   int    `json:"app_id"`
	AppHash string `json:"app_hash"`
}

type AccountCredentialsCipher struct {
	aead cipher.AEAD
}

func NewAccountCredentialsCipher(key []byte) (*AccountCredentialsCipher, error) {
	if len(key) != accountCredentialsKeySize {
		return nil, errors.New("account credentials key must be exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("create account credentials cipher")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("create account credentials cipher")
	}
	return &AccountCredentialsCipher{aead: aead}, nil
}

func (c *AccountCredentialsCipher) Encrypt(ctx context.Context, accountID domain.ID, credentials AppCredentials) ([]byte, []byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if err := validateAppCredentials(accountID, credentials); err != nil {
		return nil, nil, err
	}
	plaintext, err := json.Marshal(credentials)
	if err != nil {
		return nil, nil, errors.New("encode account credentials")
	}
	defer clear(plaintext)

	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, errors.New("generate account credentials nonce")
	}
	ciphertext := c.aead.Seal(nil, nonce, plaintext, accountCredentialsAAD(accountID))
	return ciphertext, nonce, nil
}

func (c *AccountCredentialsCipher) Decrypt(ctx context.Context, accountID domain.ID, ciphertext, nonce []byte) (AppCredentials, error) {
	if err := ctx.Err(); err != nil {
		return AppCredentials{}, err
	}
	if strings.TrimSpace(string(accountID)) == "" {
		return AppCredentials{}, errors.New("account id is required")
	}
	if len(nonce) != c.aead.NonceSize() {
		return AppCredentials{}, errors.New("invalid account credentials nonce")
	}
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, accountCredentialsAAD(accountID))
	if err != nil {
		return AppCredentials{}, errors.New("decrypt account credentials")
	}
	defer clear(plaintext)

	var credentials AppCredentials
	if err := json.Unmarshal(plaintext, &credentials); err != nil {
		return AppCredentials{}, errors.New("decode account credentials")
	}
	if err := validateAppCredentials(accountID, credentials); err != nil {
		return AppCredentials{}, errors.New("invalid account credentials")
	}
	return credentials, nil
}

func validateAppCredentials(accountID domain.ID, credentials AppCredentials) error {
	if strings.TrimSpace(string(accountID)) == "" {
		return errors.New("account id is required")
	}
	if credentials.AppID <= 0 || strings.TrimSpace(credentials.AppHash) == "" {
		return errors.New("valid Telegram app credentials are required")
	}
	return nil
}

func accountCredentialsAAD(accountID domain.ID) []byte {
	return []byte("telegram-app-credentials-v1:" + string(accountID))
}
