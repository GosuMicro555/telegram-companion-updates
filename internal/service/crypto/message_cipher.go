package crypto

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
)

type MessageCipher struct {
	aead   cipherAEAD
	random io.Reader
}

type cipherAEAD interface {
	NonceSize() int
	Overhead() int
	Seal(dst, nonce, plaintext, additionalData []byte) []byte
	Open(dst, nonce, ciphertext, additionalData []byte) ([]byte, error)
}

func NewMessageCipher(key []byte, random io.Reader) (*MessageCipher, error) {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, fmt.Errorf("create XChaCha20-Poly1305 cipher: %w", err)
	}
	if random == nil {
		random = rand.Reader
	}
	return &MessageCipher{aead: aead, random: random}, nil
}

func (c *MessageCipher) Encrypt(ctx context.Context, plaintext []byte, chatID string, messageID int64, messageAt time.Time) ([]byte, []byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(c.random, nonce); err != nil {
		return nil, nil, fmt.Errorf("generate message nonce: %w", err)
	}
	ciphertext := c.aead.Seal(nil, nonce, plaintext, messageAAD(chatID, messageID, messageAt))
	return ciphertext, nonce, nil
}

func (c *MessageCipher) Decrypt(ctx context.Context, ciphertext, nonce []byte, chatID string, messageID int64, messageAt time.Time) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(nonce) != c.aead.NonceSize() {
		return nil, errors.New("invalid message nonce size")
	}
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, messageAAD(chatID, messageID, messageAt))
	if err != nil {
		return nil, fmt.Errorf("decrypt scout message: %w", err)
	}
	return plaintext, nil
}

func messageAAD(chatID string, messageID int64, messageAt time.Time) []byte {
	chat := []byte(chatID)
	aad := make([]byte, 4+len(chat)+8+8)
	binary.BigEndian.PutUint32(aad[:4], uint32(len(chat)))
	copy(aad[4:], chat)
	offset := 4 + len(chat)
	binary.BigEndian.PutUint64(aad[offset:offset+8], uint64(messageID))
	binary.BigEndian.PutUint64(aad[offset+8:], uint64(messageAt.UTC().UnixNano()))
	return aad
}
