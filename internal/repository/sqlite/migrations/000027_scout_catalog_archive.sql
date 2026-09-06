-- +goose Up
ALTER TABLE scout_chats ADD COLUMN archived_at TEXT;

-- +goose Down
ALTER TABLE scout_chats DROP COLUMN archived_at;
