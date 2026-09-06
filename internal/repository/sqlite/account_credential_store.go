package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"telegram-companion/internal/domain"
	appcrypto "telegram-companion/internal/service/crypto"
)

type AccountCredentialStore struct {
	db     *sql.DB
	cipher *appcrypto.AccountCredentialsCipher
}

func NewAccountCredentialStore(db *sql.DB, cipher *appcrypto.AccountCredentialsCipher) *AccountCredentialStore {
	return &AccountCredentialStore{db: db, cipher: cipher}
}

func (s *AccountCredentialStore) Save(ctx context.Context, accountID domain.ID, credentials appcrypto.AppCredentials) error {
	if s == nil || s.db == nil || s.cipher == nil {
		return errors.New("account credential store is not configured")
	}
	if strings.TrimSpace(string(accountID)) == "" {
		return errors.New("account id is required")
	}
	ciphertext, nonce, err := s.cipher.Encrypt(ctx, accountID, credentials)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO telegram_account_credentials(account_id,nonce,ciphertext,updated_at)
		VALUES(?,?,?,?)
		ON CONFLICT(account_id) DO UPDATE SET nonce=excluded.nonce,ciphertext=excluded.ciphertext,updated_at=excluded.updated_at`,
		accountID, nonce, ciphertext, formatTime(time.Now().UTC()))
	if err != nil {
		return fmt.Errorf("save account credentials: %w", err)
	}
	return nil
}

func (s *AccountCredentialStore) Load(ctx context.Context, accountID domain.ID) (appcrypto.AppCredentials, error) {
	if s == nil || s.db == nil || s.cipher == nil {
		return appcrypto.AppCredentials{}, errors.New("account credential store is not configured")
	}
	var nonce, ciphertext []byte
	if err := s.db.QueryRowContext(ctx,
		`SELECT nonce,ciphertext FROM telegram_account_credentials WHERE account_id=?`, accountID,
	).Scan(&nonce, &ciphertext); err != nil {
		return appcrypto.AppCredentials{}, fmt.Errorf("load account credentials: %w", err)
	}
	return s.cipher.Decrypt(ctx, accountID, ciphertext, nonce)
}

func (s *AccountCredentialStore) List(ctx context.Context) (map[domain.ID]appcrypto.AppCredentials, error) {
	if s == nil || s.db == nil || s.cipher == nil {
		return nil, errors.New("account credential store is not configured")
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT account_id,nonce,ciphertext FROM telegram_account_credentials ORDER BY account_id`)
	if err != nil {
		return nil, fmt.Errorf("list account credentials: %w", err)
	}
	defer rows.Close()

	result := make(map[domain.ID]appcrypto.AppCredentials)
	for rows.Next() {
		var accountID string
		var nonce, ciphertext []byte
		if err := rows.Scan(&accountID, &nonce, &ciphertext); err != nil {
			return nil, fmt.Errorf("scan account credentials: %w", err)
		}
		credentials, err := s.cipher.Decrypt(ctx, domain.ID(accountID), ciphertext, nonce)
		if err != nil {
			return nil, err
		}
		result[domain.ID(accountID)] = credentials
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate account credentials: %w", err)
	}
	return result, nil
}
