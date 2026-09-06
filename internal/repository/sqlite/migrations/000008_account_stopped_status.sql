-- +goose Up
-- +goose NO TRANSACTION
PRAGMA foreign_keys=OFF;
BEGIN IMMEDIATE;

CREATE TABLE accounts_v8 (
  id TEXT PRIMARY KEY,
  phone_masked TEXT NOT NULL,
  display_name TEXT NOT NULL DEFAULT '',
  role TEXT NOT NULL DEFAULT 'spammer' CHECK(role IN ('spammer','scout_analyst')),
  status TEXT NOT NULL DEFAULT 'paused' CHECK(status IN ('ready','joining','partial','error','flood_wait','paused','collecting','stopped')),
  session_path TEXT NOT NULL,
  proxy_profile_id TEXT,
  public_replies_sent INTEGER NOT NULL DEFAULT 0 CHECK(public_replies_sent >= 0),
  private_messages_sent INTEGER NOT NULL DEFAULT 0 CHECK(private_messages_sent >= 0),
  last_activity_at TEXT,
  last_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  flood_wait_until TEXT
);

INSERT INTO accounts_v8 (
  id, phone_masked, display_name, role, status, session_path, proxy_profile_id,
  public_replies_sent, private_messages_sent, last_activity_at, last_error,
  created_at, updated_at, flood_wait_until
)
SELECT
  id, phone_masked, display_name, role, status, session_path, proxy_profile_id,
  public_replies_sent, private_messages_sent, last_activity_at, last_error,
  created_at, updated_at, flood_wait_until
FROM accounts;

DROP TABLE accounts;
ALTER TABLE accounts_v8 RENAME TO accounts;
COMMIT;
PRAGMA foreign_keys=ON;

-- +goose Down
UPDATE accounts SET status = 'ready' WHERE status = 'stopped';
PRAGMA foreign_keys=OFF;
BEGIN IMMEDIATE;
CREATE TABLE accounts_v7 (
  id TEXT PRIMARY KEY,
  phone_masked TEXT NOT NULL,
  display_name TEXT NOT NULL DEFAULT '',
  role TEXT NOT NULL DEFAULT 'spammer' CHECK(role IN ('spammer','scout_analyst')),
  status TEXT NOT NULL DEFAULT 'paused' CHECK(status IN ('ready','joining','partial','error','flood_wait','paused','collecting')),
  session_path TEXT NOT NULL,
  proxy_profile_id TEXT,
  public_replies_sent INTEGER NOT NULL DEFAULT 0 CHECK(public_replies_sent >= 0),
  private_messages_sent INTEGER NOT NULL DEFAULT 0 CHECK(private_messages_sent >= 0),
  last_activity_at TEXT,
  last_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  flood_wait_until TEXT
);
INSERT INTO accounts_v7 SELECT * FROM accounts;
DROP TABLE accounts;
ALTER TABLE accounts_v7 RENAME TO accounts;
COMMIT;
PRAGMA foreign_keys=ON;

