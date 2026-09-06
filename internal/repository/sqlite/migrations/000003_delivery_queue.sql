-- +goose Up
ALTER TABLE accounts ADD COLUMN flood_wait_until TEXT;

-- Earlier builds persisted flood_wait without an expiry. A migration cannot
-- reconstruct that deadline, so release those legacy rows instead of leaving
-- them permanently ineligible.
UPDATE accounts SET status = 'ready', last_error = ''
WHERE status = 'flood_wait' AND flood_wait_until IS NULL;

CREATE TABLE outgoing_message_jobs (
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
  last_error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_outgoing_jobs_due ON outgoing_message_jobs(status, next_attempt_at, created_at);

CREATE TABLE outgoing_message_events (
  id TEXT PRIMARY KEY,
  job_id TEXT NOT NULL REFERENCES outgoing_message_jobs(id) ON DELETE CASCADE,
  account_id TEXT NOT NULL,
  channel_id TEXT NOT NULL,
  type TEXT NOT NULL,
  success INTEGER NOT NULL CHECK(success IN (0,1)),
  error_code TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE INDEX idx_outgoing_events_account ON outgoing_message_events(account_id, created_at);

-- +goose Down
DROP TABLE outgoing_message_events;
DROP TABLE outgoing_message_jobs;
ALTER TABLE accounts DROP COLUMN flood_wait_until;
