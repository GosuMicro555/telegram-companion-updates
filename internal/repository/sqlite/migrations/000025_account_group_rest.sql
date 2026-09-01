-- +goose Up
ALTER TABLE account_channel_memberships ADD COLUMN rest_started_at TEXT;
ALTER TABLE account_channel_memberships ADD COLUMN rest_until TEXT;
ALTER TABLE account_channel_memberships ADD COLUMN rest_duration_hours INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE account_channel_memberships DROP COLUMN rest_duration_hours;
ALTER TABLE account_channel_memberships DROP COLUMN rest_until;
ALTER TABLE account_channel_memberships DROP COLUMN rest_started_at;
