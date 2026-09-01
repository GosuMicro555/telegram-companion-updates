-- +goose Up
ALTER TABLE accounts ADD COLUMN legacy_pause_review_required INTEGER NOT NULL DEFAULT 0
  CHECK(legacy_pause_review_required IN (0,1));

-- Migration 10 cannot distinguish a prior user pause from RuntimeStopped once
-- either row has activity history. Restore every row v10 could have changed to
-- a safe pause and require an explicit per-account resume decision.
UPDATE accounts
SET status = 'paused', status_source = 'user', legacy_pause_review_required = 1
WHERE status_source = 'runtime';

-- +goose Down
ALTER TABLE accounts DROP COLUMN legacy_pause_review_required;

