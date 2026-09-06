package crypto

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"io"
)

const proxyCredentialsKeySize = 32

type ProxyCredentialsCipher struct {
	aead cipher.AEAD
}

func NewProxyCredentialsCipher(key []byte) (*ProxyCredentialsCipher, error) {
	if len(key) != proxyCredentialsKeySize {
		return nil, errors.New("proxy credentials key must be exactly 32 bytes")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("create proxy credentials cipher")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("create proxy credentials cipher")
	}
	return &ProxyCredentialsCipher{aead: aead}, nil
}

func (c *ProxyCredentialsCipher) Encrypt(ctx context.Context, plaintext string) ([]byte, []byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}

	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, errors.New("generate proxy credentials nonce")
	}
	return c.aead.Seal(nil, nonce, []byte(plaintext), nil), nonce, nil
}

func (c *ProxyCredentialsCipher) Decrypt(ctx context.Context, ciphertext, nonce []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(nonce) != c.aead.NonceSize() {
		return "", errors.New("invalid proxy credentials nonce")
	}

	plaintext, err := c.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", errors.New("decrypt proxy credentials")
	}
	return string(plaintext), nil
}
