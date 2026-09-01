package crypto_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	messagecrypto "telegram-companion/internal/service/crypto"

	"github.com/stretchr/testify/require"
)

func TestMessageCipherEncryptsAndAuthenticatesEveryAADField(t *testing.T) {
	cipher, err := messagecrypto.NewMessageCipher(bytes.Repeat([]byte{0x42}, 32), bytes.NewReader(bytes.Repeat([]byte{0x19}, 24)))
	require.NoError(t, err)

	plaintext := []byte("пример текста")
	messageAt := time.Date(2026, 7, 11, 8, 30, 0, 0, time.UTC)
	ciphertext, nonce, err := cipher.Encrypt(context.Background(), plaintext, "1001", 77, messageAt)
	require.NoError(t, err)
	require.Len(t, nonce, 24)
	require.NotContains(t, string(ciphertext), string(plaintext))

	decrypted, err := cipher.Decrypt(context.Background(), ciphertext, nonce, "1001", 77, messageAt)
	require.NoError(t, err)
	require.Equal(t, plaintext, decrypted)

	for name, changed := range map[string]struct {
		chatID    string
		messageID int64
		messageAt time.Time
	}{
		"chat":      {"1002", 77, messageAt},
		"message":   {"1001", 78, messageAt},
		"timestamp": {"1001", 77, messageAt.Add(time.Minute)},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := cipher.Decrypt(context.Background(), ciphertext, nonce, changed.chatID, changed.messageID, changed.messageAt)
			require.Error(t, err)
		})
	}
}

func TestMessageCipherHonorsCancelledContext(t *testing.T) {
	cipher, err := messagecrypto.NewMessageCipher(bytes.Repeat([]byte{0x42}, 32), bytes.NewReader(bytes.Repeat([]byte{0x19}, 24)))
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err = cipher.Encrypt(ctx, []byte("secret"), "1001", 77, time.Now())
	require.ErrorIs(t, err, context.Canceled)
}
