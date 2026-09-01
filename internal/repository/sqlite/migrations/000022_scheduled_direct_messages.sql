-- +goose Up
CREATE TABLE scheduled_dm_tasks (
  id TEXT PRIMARY KEY,
  message_text TEXT NOT NULL,
  status TEXT NOT NULL CHECK(status IN ('paused','active','completed','cancelled','error')),
  start_at TEXT NOT NULL,
  recurrence TEXT NOT NULL CHECK(recurrence IN ('once','5m','10m','30m','1h','2h','3h','4h','5h','6h','7h','8h','9h','10h','11h','12h','daily','weekly')),
  interval_seconds INTEGER,
  max_runs INTEGER NOT NULL CHECK(max_runs BETWEEN 1 AND 12),
  completed_runs INTEGER NOT NULL DEFAULT 0 CHECK(completed_runs BETWEEN 0 AND max_runs),
  next_run_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE scheduled_dm_recipients (
  task_id TEXT NOT NULL REFERENCES scheduled_dm_tasks(id) ON DELETE CASCADE,
  ordinal INTEGER NOT NULL CHECK(ordinal >= 0),
  username TEXT NOT NULL,
  peer_status TEXT NOT NULL CHECK(peer_status IN ('unchecked','resolved','open','closed','invalid','error')),
  last_error TEXT NOT NULL DEFAULT '',
  last_checked_at TEXT,
  PRIMARY KEY(task_id, ordinal),
  UNIQUE(task_id, username)
);

CREATE TABLE scheduled_dm_accounts (
  task_id TEXT NOT NULL REFERENCES scheduled_dm_tasks(id) ON DELETE CASCADE,
  ordinal INTEGER NOT NULL CHECK(ordinal >= 0),
  account_id TEXT NOT NULL REFERENCES accounts(id),
  PRIMARY KEY(task_id, ordinal),
  UNIQUE(task_id, account_id)
);

CREATE TABLE scheduled_dm_runs (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL REFERENCES scheduled_dm_tasks(id) ON DELETE CASCADE,
  run_number INTEGER NOT NULL CHECK(run_number >= 1),
  scheduled_at TEXT NOT NULL,
  completed_at TEXT,
  created_at TEXT NOT NULL,
  UNIQUE(task_id, run_number)
);

CREATE TABLE scheduled_dm_deliveries (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL REFERENCES scheduled_dm_tasks(id) ON DELETE CASCADE,
  run_id TEXT NOT NULL REFERENCES scheduled_dm_runs(id) ON DELETE CASCADE,
  recipient TEXT NOT NULL,
  account_id TEXT NOT NULL REFERENCES accounts(id),
  telegram_random_id INTEGER NOT NULL,
  status TEXT NOT NULL CHECK(status IN ('pending','sending','sent','closed','failed')),
  next_attempt_at TEXT NOT NULL,
  attempted_at TEXT,
  completed_at TEXT,
  error_code TEXT NOT NULL DEFAULT '',
  lease_token TEXT NOT NULL DEFAULT '',
  lease_until TEXT,
  UNIQUE(run_id, recipient)
);

CREATE INDEX idx_scheduled_dm_tasks_due ON scheduled_dm_tasks(status, next_run_at);
CREATE INDEX idx_scheduled_dm_deliveries_due ON scheduled_dm_deliveries(status, next_attempt_at, lease_until);

-- +goose Down
DROP TABLE scheduled_dm_deliveries;
DROP TABLE scheduled_dm_runs;
DROP TABLE scheduled_dm_accounts;
DROP TABLE scheduled_dm_recipients;
DROP TABLE scheduled_dm_tasks;
