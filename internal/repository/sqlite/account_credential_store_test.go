package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"telegram-companion/internal/domain"
	appcrypto "telegram-companion/internal/service/crypto"
)

func TestAccountCredentialStoreEncryptsAndLoadsPerAccountCredentials(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	defer db.Close()
	accountID := domain.ID("account-credentials")
	saveCredentialTestAccount(t, db, accountID)
	cipher, err := appcrypto.NewAccountCredentialsCipher(make([]byte, 32))
	require.NoError(t, err)
	store := NewAccountCredentialStore(db, cipher)
	want := appcrypto.AppCredentials{AppID: 12345, AppHash: "secret-app-hash"}

	require.NoError(t, store.Save(ctx, accountID, want))

	var nonce, ciphertext []byte
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT nonce,ciphertext FROM telegram_account_credentials WHERE account_id=?`, accountID,
	).Scan(&nonce, &ciphertext))
	require.NotEmpty(t, nonce)
	require.NotEmpty(t, ciphertext)
	require.NotContains(t, string(ciphertext), want.AppHash)

	got, err := store.Load(ctx, accountID)
	require.NoError(t, err)
	require.Equal(t, want, got)

	all, err := store.List(ctx)
	require.NoError(t, err)
	require.Equal(t, map[domain.ID]appcrypto.AppCredentials{accountID: want}, all)
}

func TestAccountCredentialStoreUpsertsAndRejectsWrongCipher(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	require.NoError(t, err)
	defer db.Close()
	accountID := domain.ID("account-upsert")
	saveCredentialTestAccount(t, db, accountID)
	cipher, err := appcrypto.NewAccountCredentialsCipher(make([]byte, 32))
	require.NoError(t, err)
	store := NewAccountCredentialStore(db, cipher)
	require.NoError(t, store.Save(ctx, accountID, appcrypto.AppCredentials{AppID: 1, AppHash: "first"}))
	want := appcrypto.AppCredentials{AppID: 2, AppHash: "second"}
	require.NoError(t, store.Save(ctx, accountID, want))

	got, err := store.Load(ctx, accountID)
	require.NoError(t, err)
	require.Equal(t, want, got)

	wrongKey := make([]byte, 32)
	wrongKey[0] = 1
	wrongCipher, err := appcrypto.NewAccountCredentialsCipher(wrongKey)
	require.NoError(t, err)
	_, err = NewAccountCredentialStore(db, wrongCipher).Load(ctx, accountID)
	require.Error(t, err)
}

func saveCredentialTestAccount(t *testing.T, db *sql.DB, id domain.ID) {
	t.Helper()
	now := time.Now().UTC()
	account := domain.Account{
		ID:          id,
		Role:        domain.AccountRoleSpammer,
		Status:      domain.AccountStopped,
		SessionPath: filepath.Join(t.TempDir(), string(id)+".session"),
		ProxyMode:   domain.ProxyModeGlobal,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	require.NoError(t, NewProductionStore(db).Accounts().Save(context.Background(), account))
}
