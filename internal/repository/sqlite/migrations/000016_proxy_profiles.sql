-- +goose Up
CREATE TABLE proxy_profiles (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  normalized_name TEXT NOT NULL UNIQUE,
  protocol TEXT NOT NULL CHECK(protocol IN ('socks5','http')),
  host TEXT NOT NULL,
  port INTEGER NOT NULL CHECK(port BETWEEN 1 AND 65535),
  normalized_endpoint TEXT NOT NULL UNIQUE,
  username TEXT NOT NULL DEFAULT '',
  encrypted_password BLOB NOT NULL,
  password_nonce BLOB NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),
  last_health_status TEXT NOT NULL DEFAULT 'checking' CHECK(last_health_status IN ('ready','checking','degraded','disabled','full')),
  last_health_at TEXT,
  last_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

ALTER TABLE accounts ADD COLUMN proxy_mode TEXT NOT NULL DEFAULT 'global'
  CHECK(proxy_mode IN ('global','assigned','unassigned'));
UPDATE accounts SET proxy_mode='global', proxy_profile_id=NULL;
UPDATE accounts SET proxy_mode='unassigned', status='stopped'
WHERE id NOT IN (SELECT id FROM accounts ORDER BY created_at,id LIMIT 10);

-- +goose StatementBegin
CREATE TRIGGER accounts_proxy_assignment_insert
BEFORE INSERT ON accounts
BEGIN
  SELECT CASE
    WHEN NEW.proxy_mode='assigned' AND (NEW.proxy_profile_id IS NULL OR NOT EXISTS(SELECT 1 FROM proxy_profiles WHERE id=NEW.proxy_profile_id AND enabled=1))
      THEN RAISE(ABORT, 'invalid proxy assignment')
    WHEN NEW.proxy_mode IN ('global','unassigned') AND NEW.proxy_profile_id IS NOT NULL
      THEN RAISE(ABORT, 'invalid proxy assignment')
    WHEN NEW.proxy_mode='global' AND (SELECT COUNT(*) FROM accounts WHERE proxy_mode='global') >= 10
      THEN RAISE(ABORT, 'proxy route capacity reached')
    WHEN NEW.proxy_mode='assigned' AND (SELECT COUNT(*) FROM accounts WHERE proxy_mode='assigned' AND proxy_profile_id=NEW.proxy_profile_id) >= 10
      THEN RAISE(ABORT, 'proxy route capacity reached')
  END;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER accounts_proxy_assignment_update
BEFORE UPDATE OF proxy_mode,proxy_profile_id ON accounts
BEGIN
  SELECT CASE
    WHEN NEW.proxy_mode='assigned' AND (NEW.proxy_profile_id IS NULL OR NOT EXISTS(SELECT 1 FROM proxy_profiles WHERE id=NEW.proxy_profile_id AND enabled=1))
      THEN RAISE(ABORT, 'invalid proxy assignment')
    WHEN NEW.proxy_mode IN ('global','unassigned') AND NEW.proxy_profile_id IS NOT NULL
      THEN RAISE(ABORT, 'invalid proxy assignment')
    WHEN NEW.proxy_mode='global' AND (SELECT COUNT(*) FROM accounts WHERE proxy_mode='global' AND id<>NEW.id) >= 10
      THEN RAISE(ABORT, 'proxy route capacity reached')
    WHEN NEW.proxy_mode='assigned' AND (SELECT COUNT(*) FROM accounts WHERE proxy_mode='assigned' AND proxy_profile_id=NEW.proxy_profile_id AND id<>NEW.id) >= 10
      THEN RAISE(ABORT, 'proxy route capacity reached')
  END;
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER accounts_proxy_assignment_update;
DROP TRIGGER accounts_proxy_assignment_insert;
UPDATE accounts SET proxy_profile_id=NULL;
ALTER TABLE accounts DROP COLUMN proxy_mode;
DROP TABLE proxy_profiles;
