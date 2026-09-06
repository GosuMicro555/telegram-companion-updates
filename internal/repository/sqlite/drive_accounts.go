package sqlite

import (
	"context"
	"database/sql"
	"time"

	"telegram-companion/internal/domain"
	"telegram-companion/internal/driveaccounts"
	appcrypto "telegram-companion/internal/service/crypto"
)

// DriveAccountStore inserts new accounts and encrypted application credentials in
// one transaction. In particular it never calls the legacy upsert/EnsureAccounts.
type DriveAccountStore struct {
	db     *sql.DB
	cipher *appcrypto.AccountCredentialsCipher
}

func NewDriveAccountStore(db *sql.DB, cipher *appcrypto.AccountCredentialsCipher) *DriveAccountStore {
	return &DriveAccountStore{db: db, cipher: cipher}
}
func (s *AccountCredentialStore) DriveAccountStore() *DriveAccountStore {
	return NewDriveAccountStore(s.db, s.cipher)
}
func (s *DriveAccountStore) ListAccounts(ctx context.Context) ([]domain.Account, error) {
	return NewProductionStore(s.db).ListAccounts(ctx)
}
func (s *DriveAccountStore) CommitDriveAccounts(ctx context.Context, candidates []driveaccounts.Candidate) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, c := range candidates {
		ciphertext, nonce, e := s.cipher.Encrypt(ctx, c.Account.ID, c.Credentials)
		if e != nil {
			return e
		}
		a := c.Account
		now := formatTime(time.Now().UTC())
		_, err = tx.ExecContext(ctx, `INSERT INTO accounts (id,phone_masked,display_name,role,status,session_path,proxy_mode,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?)`, a.ID, a.PhoneMasked, a.DisplayName, a.Role, encodeAccountStatus(domain.AccountStopped), a.SessionPath, domain.ProxyModeGlobal, now, now)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO telegram_account_credentials (account_id,nonce,ciphertext,updated_at) VALUES (?,?,?,?)`, a.ID, nonce, ciphertext, now)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
