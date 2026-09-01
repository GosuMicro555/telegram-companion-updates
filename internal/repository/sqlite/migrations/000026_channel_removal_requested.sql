-- +goose Up
ALTER TABLE outbound_channels ADD COLUMN removal_requested_at TEXT;
ALTER TABLE scout_chats ADD COLUMN removal_requested_at TEXT;

-- +goose Down
ALTER TABLE outbound_channels DROP COLUMN removal_requested_at;
ALTER TABLE scout_chats DROP COLUMN removal_requested_at;
