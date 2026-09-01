-- +goose Up
ALTER TABLE outgoing_message_jobs ADD COLUMN lease_token TEXT NOT NULL DEFAULT '';
ALTER TABLE outgoing_message_jobs ADD COLUMN lease_until TEXT;
ALTER TABLE outgoing_message_jobs ADD COLUMN dead_lettered_at TEXT;
CREATE INDEX idx_outgoing_jobs_lease ON outgoing_message_jobs(status, lease_until, next_attempt_at);

-- +goose Down
DROP INDEX idx_outgoing_jobs_lease;
ALTER TABLE outgoing_message_jobs DROP COLUMN dead_lettered_at;
ALTER TABLE outgoing_message_jobs DROP COLUMN lease_until;
ALTER TABLE outgoing_message_jobs DROP COLUMN lease_token;

