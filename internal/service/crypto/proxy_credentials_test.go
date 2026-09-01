package crypto_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	proxycrypto "telegram-companion/internal/service/crypto"

	"github.com/stretchr/testify/require"
)

func TestProxyCredentialsCipherRoundTripsPasswordsAndUsesFreshNonces(t *testing.T) {
	cipher, err := proxycrypto.NewProxyCredentialsCipher(bytes.Repeat([]byte{0x42}, 32))
	require.NoError(t, err)

	for _, plaintext := range []string{"", "proxy-password-123"} {
		t.Run(map[bool]string{true: "empty", false: "non-empty"}[plaintext == ""], func(t *testing.T) {
			ciphertextOne, nonceOne, err := cipher.Encrypt(context.Background(), plaintext)
			require.NoError(t, err)
			ciphertextTwo, nonceTwo, err := cipher.Encrypt(context.Background(), plaintext)
			require.NoError(t, err)

			require.Len(t, nonceOne, 12)
			require.Len(t, nonceTwo, 12)
			require.NotEqual(t, nonceOne, nonceTwo)
			require.NotEqual(t, ciphertextOne, ciphertextTwo)

			decrypted, err := cipher.Decrypt(context.Background(), ciphertextOne, nonceOne)
			require.NoError(t, err)
			require.Equal(t, plaintext, decrypted)
		})
	}
}

func TestNewProxyCredentialsCipherRequiresExactly32ByteKey(t *testing.T) {
	for _, keyLength := range []int{31, 33} {
		t.Run("key length", func(t *testing.T) {
			_, err := proxycrypto.NewProxyCredentialsCipher(make([]byte, keyLength))
			require.Error(t, err)
		})
	}
}

func TestProxyCredentialsCipherRejectsWrongKeyTamperingAndBadNonce(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	cipher, err := proxycrypto.NewProxyCredentialsCipher(key)
	require.NoError(t, err)
	ciphertext, nonce, err := cipher.Encrypt(context.Background(), "super-secret-password")
	require.NoError(t, err)

	wrongKey := bytes.Repeat([]byte{0x24}, 32)
	wrongCipher, err := proxycrypto.NewProxyCredentialsCipher(wrongKey)
	require.NoError(t, err)

	tests := map[string]func() error{
		"wrong key": func() error {
			_, err := wrongCipher.Decrypt(context.Background(), ciphertext, nonce)
			return err
		},
		"tampered ciphertext": func() error {
			modified := append([]byte(nil), ciphertext...)
			modified[0] ^= 0x01
			_, err := cipher.Decrypt(context.Background(), modified, nonce)
			return err
		},
		"short nonce": func() error {
			_, err := cipher.Decrypt(context.Background(), ciphertext, nonce[:len(nonce)-1])
			return err
		},
		"long nonce": func() error {
			_, err := cipher.Decrypt(context.Background(), ciphertext, append(append([]byte(nil), nonce...), 0))
			return err
		},
	}

	for name, operation := range tests {
		t.Run(name, func(t *testing.T) {
			err := operation()
			require.Error(t, err)
			require.NotContains(t, err.Error(), "super-secret-password")
			require.NotContains(t, err.Error(), string(ciphertext))
		})
	}
}

func TestProxyCredentialsCipherHonorsCancelledContextBeforeCryptoWork(t *testing.T) {
	cipher, err := proxycrypto.NewProxyCredentialsCipher(bytes.Repeat([]byte{0x42}, 32))
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ciphertext, nonce, err := cipher.Encrypt(ctx, "secret")
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, ciphertext)
	require.Nil(t, nonce)

	decrypted, err := cipher.Decrypt(ctx, []byte("ciphertext"), []byte("bad"))
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, decrypted)
}

func TestProxyCredentialsCipherErrorsDoNotEchoInputs(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	cipher, err := proxycrypto.NewProxyCredentialsCipher(key)
	require.NoError(t, err)
	plaintext := "password-that-must-not-appear-in-errors"
	ciphertext, nonce, err := cipher.Encrypt(context.Background(), plaintext)
	require.NoError(t, err)

	wrongKey := bytes.Repeat([]byte{0x24}, 32)
	wrongCipher, err := proxycrypto.NewProxyCredentialsCipher(wrongKey)
	require.NoError(t, err)
	_, err = wrongCipher.Decrypt(context.Background(), ciphertext, nonce)
	require.Error(t, err)
	require.False(t, strings.Contains(err.Error(), plaintext))
	require.False(t, strings.Contains(err.Error(), string(ciphertext)))
}
