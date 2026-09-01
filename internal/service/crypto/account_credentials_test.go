package crypto_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"telegram-companion/internal/domain"
	appcrypto "telegram-companion/internal/service/crypto"
)

func TestAccountCredentialsCipherRoundTripsWithAccountAAD(t *testing.T) {
	cipher, err := appcrypto.NewAccountCredentialsCipher(bytes.Repeat([]byte{0x31}, 32))
	require.NoError(t, err)
	credentials := appcrypto.AppCredentials{AppID: 12345, AppHash: "telegram-app-hash"}

	ciphertext, nonce, err := cipher.Encrypt(context.Background(), "account-one", credentials)
	require.NoError(t, err)
	require.NotContains(t, string(ciphertext), credentials.AppHash)

	got, err := cipher.Decrypt(context.Background(), "account-one", ciphertext, nonce)
	require.NoError(t, err)
	require.Equal(t, credentials, got)
}

func TestAccountCredentialsCipherRejectsWrongAccountAndTampering(t *testing.T) {
	cipher, err := appcrypto.NewAccountCredentialsCipher(bytes.Repeat([]byte{0x42}, 32))
	require.NoError(t, err)
	credentials := appcrypto.AppCredentials{AppID: 777, AppHash: "private-hash"}
	ciphertext, nonce, err := cipher.Encrypt(context.Background(), "account-one", credentials)
	require.NoError(t, err)

	_, err = cipher.Decrypt(context.Background(), "account-two", ciphertext, nonce)
	require.Error(t, err)

	tampered := append([]byte(nil), ciphertext...)
	tampered[0] ^= 0x01
	_, err = cipher.Decrypt(context.Background(), "account-one", tampered, nonce)
	require.Error(t, err)
	require.NotContains(t, err.Error(), credentials.AppHash)
}

func TestAccountCredentialsCipherValidatesInputs(t *testing.T) {
	_, err := appcrypto.NewAccountCredentialsCipher(make([]byte, 31))
	require.Error(t, err)

	cipher, err := appcrypto.NewAccountCredentialsCipher(make([]byte, 32))
	require.NoError(t, err)
	_, _, err = cipher.Encrypt(context.Background(), "", appcrypto.AppCredentials{AppID: 1, AppHash: "hash"})
	require.Error(t, err)
	_, _, err = cipher.Encrypt(context.Background(), domain.ID("account"), appcrypto.AppCredentials{})
	require.Error(t, err)
}
