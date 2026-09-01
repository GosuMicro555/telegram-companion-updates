-- +goose Up

CREATE TABLE account_channel_memberships_new (
  account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  catalog TEXT NOT NULL CHECK(catalog IN ('outbound','scout')),
  channel_id TEXT NOT NULL,
  is_member INTEGER NOT NULL DEFAULT 0 CHECK(is_member IN (0,1)),
  status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','joining','pending_approval','member','leaving','flood_wait','error','paused')),
  last_check_at TEXT,
  request_submitted_at TEXT,
  joined_at TEXT,
  join_not_before TEXT,
  rest_started_at TEXT,
  rest_until TEXT,
  rest_duration_hours INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(account_id,catalog,channel_id)
);
INSERT INTO account_channel_memberships_new
  (account_id,catalog,channel_id,is_member,status,last_check_at,request_submitted_at,joined_at,join_not_before,rest_started_at,rest_until,rest_duration_hours,last_error)
SELECT account_id,catalog,channel_id,is_member,status,last_check_at,request_submitted_at,joined_at,join_not_before,rest_started_at,rest_until,rest_duration_hours,last_error
FROM account_channel_memberships;
DROP TABLE account_channel_memberships;
ALTER TABLE account_channel_memberships_new RENAME TO account_channel_memberships;

-- Inactive catalog channels used to leave scheduled joins behind. Only
-- unsubmitted joins are safe to cancel; submitted moderation requests and
-- established memberships retain their state for explicit reconciliation.
DELETE FROM account_channel_memberships AS membership
WHERE membership.status = 'joining'
  AND membership.is_member = 0
  AND membership.request_submitted_at IS NULL
  AND (
    EXISTS (
      SELECT 1
      FROM outbound_channels AS channel
      WHERE membership.catalog = 'outbound'
        AND channel.id = membership.channel_id
        AND channel.active = 0
    )
    OR EXISTS (
      SELECT 1
      FROM scout_chats AS channel
      WHERE membership.catalog = 'scout'
        AND channel.id = membership.channel_id
        AND channel.active = 0
    )
  );

-- +goose Down
CREATE TABLE account_channel_memberships_old (
  account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  catalog TEXT NOT NULL CHECK(catalog IN ('outbound','scout')),
  channel_id TEXT NOT NULL,
  is_member INTEGER NOT NULL DEFAULT 0 CHECK(is_member IN (0,1)),
  status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','joining','pending_approval','member','flood_wait','error','paused')),
  last_check_at TEXT,
  request_submitted_at TEXT,
  joined_at TEXT,
  join_not_before TEXT,
  rest_started_at TEXT,
  rest_until TEXT,
  rest_duration_hours INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(account_id,catalog,channel_id)
);
INSERT INTO account_channel_memberships_old
  (account_id,catalog,channel_id,is_member,status,last_check_at,request_submitted_at,joined_at,join_not_before,rest_started_at,rest_until,rest_duration_hours,last_error)
SELECT account_id,catalog,channel_id,is_member,
  CASE
    WHEN status='leaving' AND is_member=1 THEN 'member'
    WHEN status='leaving' AND request_submitted_at IS NOT NULL THEN 'pending_approval'
    WHEN status='leaving' THEN 'joining'
    ELSE status
  END,
  last_check_at,request_submitted_at,joined_at,join_not_before,rest_started_at,rest_until,rest_duration_hours,last_error
FROM account_channel_memberships;
DROP TABLE account_channel_memberships;
ALTER TABLE account_channel_memberships_old RENAME TO account_channel_memberships;
