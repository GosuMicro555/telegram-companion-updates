-- +goose Up
CREATE TABLE account_channel_memberships_new (
  account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  catalog TEXT NOT NULL CHECK(catalog IN ('outbound','scout')),
  channel_id TEXT NOT NULL,
  is_member INTEGER NOT NULL DEFAULT 0 CHECK(is_member IN (0,1)),
  status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','joining','pending_approval','member','flood_wait','error','paused')),
  last_check_at TEXT,
  last_error TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(account_id,catalog,channel_id)
);
INSERT INTO account_channel_memberships_new
  (account_id,catalog,channel_id,is_member,status,last_check_at,last_error)
SELECT account_id,catalog,channel_id,is_member,status,last_check_at,last_error
FROM account_channel_memberships;
DROP TABLE account_channel_memberships;
ALTER TABLE account_channel_memberships_new RENAME TO account_channel_memberships;

-- +goose Down
CREATE TABLE account_channel_memberships_old (
  account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  catalog TEXT NOT NULL CHECK(catalog IN ('outbound','scout')),
  channel_id TEXT NOT NULL,
  is_member INTEGER NOT NULL DEFAULT 0 CHECK(is_member IN (0,1)),
  status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','joining','member','error','paused')),
  last_check_at TEXT,
  last_error TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(account_id,catalog,channel_id)
);
INSERT INTO account_channel_memberships_old
  (account_id,catalog,channel_id,is_member,status,last_check_at,last_error)
SELECT account_id,catalog,channel_id,is_member,
  CASE status WHEN 'pending_approval' THEN 'joining' WHEN 'flood_wait' THEN 'error' ELSE status END,
  last_check_at,last_error
FROM account_channel_memberships;
DROP TABLE account_channel_memberships;
ALTER TABLE account_channel_memberships_old RENAME TO account_channel_memberships;
