-- +goose Up
ALTER TABLE outgoing_message_jobs ADD COLUMN keyword_delivery_mode TEXT NOT NULL DEFAULT '';
ALTER TABLE outgoing_message_jobs ADD COLUMN fallback_to_public INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE outgoing_message_jobs DROP COLUMN fallback_to_public;
ALTER TABLE outgoing_message_jobs DROP COLUMN keyword_delivery_mode;
