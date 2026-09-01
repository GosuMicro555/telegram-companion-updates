-- +goose Up
ALTER TABLE accounts ADD COLUMN status_source TEXT NOT NULL DEFAULT 'runtime'
  CHECK(status_source IN ('runtime','user'));

-- Before status_source existed, paused mixed durable user intent with the
-- buggy RuntimeStopped callback. A successful gotd validation persisted
-- last_activity_at before RuntimeStopped; cancellation during reconnect
-- persisted the bounded cancelled code. Recover only those durable runtime
-- markers. Ambiguous rows remain paused rather than overriding user intent.
UPDATE accounts
SET status_source = 'user'
WHERE status = 'paused';

UPDATE accounts
SET status = 'stopped', status_source = 'runtime'
WHERE status = 'paused'
  AND (last_activity_at IS NOT NULL OR last_error = 'cancelled');

-- +goose Down
ALTER TABLE accounts DROP COLUMN status_source;

