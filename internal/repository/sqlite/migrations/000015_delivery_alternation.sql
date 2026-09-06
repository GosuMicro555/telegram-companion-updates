-- +goose Up
-- +goose NO TRANSACTION
PRAGMA foreign_keys=OFF;
BEGIN IMMEDIATE;

ALTER TABLE accounts ADD COLUMN next_delivery TEXT NOT NULL DEFAULT 'private'
  CHECK(next_delivery IN ('private','public'));
ALTER TABLE accounts ADD COLUMN private_messages_closed INTEGER NOT NULL DEFAULT 0
  CHECK(private_messages_closed >= 0);

CREATE TABLE outgoing_message_jobs_v15 (
  id TEXT PRIMARY KEY,
  type TEXT NOT NULL CHECK(type IN ('public_reply','private_message','keyword_response')),
  account_id TEXT REFERENCES accounts(id) ON DELETE SET NULL,
  channel_id TEXT NOT NULL,
  rule_id TEXT NOT NULL DEFAULT '',
  target_telegram_id TEXT NOT NULL DEFAULT '',
  reply_to_message_id TEXT NOT NULL DEFAULT '',
  text TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'queued' CHECK(status IN ('queued','delayed','done')),
  attempts INTEGER NOT NULL DEFAULT 0 CHECK(attempts >= 0),
  next_attempt_at TEXT NOT NULL,
  created_at TEXT NOT NULL,
  last_error TEXT NOT NULL DEFAULT '',
  lease_token TEXT NOT NULL DEFAULT '',
  lease_until TEXT,
  dead_lettered_at TEXT,
  allow_private INTEGER NOT NULL DEFAULT 0 CHECK(allow_private IN (0,1))
);

INSERT INTO outgoing_message_jobs_v15 (
  id, type, account_id, channel_id, rule_id, target_telegram_id,
  reply_to_message_id, text, status, attempts, next_attempt_at, created_at,
  last_error, lease_token, lease_until, dead_lettered_at, allow_private
)
SELECT
  id, type, account_id, channel_id, rule_id, target_telegram_id,
  reply_to_message_id, text, status, attempts, next_attempt_at, created_at,
  last_error, lease_token, lease_until, dead_lettered_at, 0
FROM outgoing_message_jobs;

DROP TABLE outgoing_message_jobs;
ALTER TABLE outgoing_message_jobs_v15 RENAME TO outgoing_message_jobs;
CREATE INDEX idx_outgoing_jobs_due ON outgoing_message_jobs(status, next_attempt_at, created_at);
CREATE INDEX idx_outgoing_jobs_lease ON outgoing_message_jobs(status, lease_until, next_attempt_at);

COMMIT;
PRAGMA foreign_keys=ON;

-- +goose Down
BEGIN IMMEDIATE;
CREATE TEMP TABLE delivery_v15_down_guard (
  marker INTEGER PRIMARY KEY ON CONFLICT ROLLBACK
);
INSERT INTO delivery_v15_down_guard(marker) VALUES (1);
INSERT INTO delivery_v15_down_guard(marker)
SELECT 1 FROM outgoing_message_jobs WHERE type = 'keyword_response' LIMIT 1;
DROP TABLE delivery_v15_down_guard;
COMMIT;

PRAGMA foreign_keys=OFF;
BEGIN IMMEDIATE;

CREATE TABLE outgoing_message_jobs_v14 (
  id TEXT PRIMARY KEY,
  type TEXT NOT NULL CHECK(type IN ('public_reply','private_message')),
  account_id TEXT REFERENCES accounts(id) ON DELETE SET NULL,
  channel_id TEXT NOT NULL,
  rule_id TEXT NOT NULL DEFAULT '',
  target_telegram_id TEXT NOT NULL DEFAULT '',
  reply_to_message_id TEXT NOT NULL DEFAULT '',
  text TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'queued' CHECK(status IN ('queued','delayed','done')),
  attempts INTEGER NOT NULL DEFAULT 0 CHECK(attempts >= 0),
  next_attempt_at TEXT NOT NULL,
  created_at TEXT NOT NULL,
  last_error TEXT NOT NULL DEFAULT '',
  lease_token TEXT NOT NULL DEFAULT '',
  lease_until TEXT,
  dead_lettered_at TEXT
);

INSERT INTO outgoing_message_jobs_v14 (
  id, type, account_id, channel_id, rule_id, target_telegram_id,
  reply_to_message_id, text, status, attempts, next_attempt_at, created_at,
  last_error, lease_token, lease_until, dead_lettered_at
)
SELECT
  id, type, account_id, channel_id, rule_id, target_telegram_id,
  reply_to_message_id, text, status, attempts, next_attempt_at, created_at,
  last_error, lease_token, lease_until, dead_lettered_at
FROM outgoing_message_jobs
WHERE type IN ('public_reply','private_message');

DROP TABLE outgoing_message_jobs;
ALTER TABLE outgoing_message_jobs_v14 RENAME TO outgoing_message_jobs;
CREATE INDEX idx_outgoing_jobs_due ON outgoing_message_jobs(status, next_attempt_at, created_at);
CREATE INDEX idx_outgoing_jobs_lease ON outgoing_message_jobs(status, lease_until, next_attempt_at);
ALTER TABLE accounts DROP COLUMN private_messages_closed;
ALTER TABLE accounts DROP COLUMN next_delivery;

COMMIT;
PRAGMA foreign_keys=ON;
