-- +goose Up
CREATE TABLE telegram_account_credentials (
  account_id TEXT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
  nonce BLOB NOT NULL,
  ciphertext BLOB NOT NULL,
  updated_at TEXT NOT NULL
);

-- +goose Down
DROP TABLE telegram_account_credentials;
