PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;
BEGIN IMMEDIATE;

CREATE TEMP TABLE _seed_version_guard (
  version INTEGER NOT NULL CHECK (version = 32)
);
INSERT INTO _seed_version_guard
SELECT COALESCE(MAX(version_id), 0)
FROM goose_db_version
WHERE is_applied = 1;

UPDATE accounts
SET status = 'stopped',
    status_source = 'runtime',
    legacy_pause_review_required = 0,
    last_error = '',
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE status <> 'stopped'
   OR status_source <> 'runtime'
   OR legacy_pause_review_required <> 0;

UPDATE account_channel_memberships
SET status = 'paused',
    join_not_before = NULL
WHERE status IN ('joining', 'pending_approval', 'leaving');

UPDATE outbound_channels
SET status = 'paused',
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE status = 'joining';

UPDATE scout_chats
SET status = 'paused',
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE status = 'joining';

CREATE TEMP TABLE _seed_cancelled_jobs (id TEXT PRIMARY KEY);
INSERT INTO _seed_cancelled_jobs
SELECT id FROM outgoing_message_jobs WHERE status IN ('queued', 'delayed');

UPDATE live_delivery_history
SET final_status = 'not_delivered',
    error_code = 'seed_build_cancelled',
    finalized_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE final_status IS NULL
  AND job_id IN (SELECT id FROM _seed_cancelled_jobs);

UPDATE outgoing_message_jobs
SET status = 'done',
    last_error = 'seed_build_cancelled',
    lease_token = '',
    lease_until = NULL
WHERE id IN (SELECT id FROM _seed_cancelled_jobs);

CREATE TEMP TABLE _seed_cancelled_dm_runs (id TEXT PRIMARY KEY);
INSERT INTO _seed_cancelled_dm_runs
SELECT DISTINCT delivery.run_id
FROM scheduled_dm_deliveries AS delivery
JOIN scheduled_dm_tasks AS task ON task.id = delivery.task_id
WHERE delivery.status IN ('pending', 'sending')
  AND task.status NOT IN ('completed', 'cancelled');

UPDATE scheduled_dm_tasks
SET status = 'cancelled',
    next_run_at = NULL,
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id IN (
  SELECT DISTINCT delivery.task_id
  FROM scheduled_dm_deliveries AS delivery
  WHERE delivery.run_id IN (SELECT id FROM _seed_cancelled_dm_runs)
);

UPDATE scheduled_dm_tasks
SET status = 'paused',
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE status = 'active';

UPDATE scheduled_dm_deliveries
SET status = 'failed',
    completed_at = COALESCE(completed_at, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    error_code = 'seed_build_cancelled',
    lease_token = '',
    lease_until = NULL
WHERE run_id IN (SELECT id FROM _seed_cancelled_dm_runs)
  AND status IN ('pending', 'sending');

UPDATE scheduled_dm_runs
SET completed_at = COALESCE(completed_at, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
WHERE id IN (SELECT id FROM _seed_cancelled_dm_runs);

UPDATE analytics_scheduler_settings
SET enabled = 0,
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE singleton = 1;

CREATE TEMP TABLE _seed_zero_guard (
  pending INTEGER NOT NULL CHECK (pending = 0)
);
INSERT INTO _seed_zero_guard
SELECT
  (SELECT COUNT(*) FROM accounts WHERE status <> 'stopped' OR status_source <> 'runtime') +
  (SELECT COUNT(*) FROM outgoing_message_jobs WHERE status IN ('queued', 'delayed')) +
  (SELECT COUNT(*) FROM account_channel_memberships
    WHERE status IN ('joining', 'pending_approval', 'leaving')) +
  (SELECT COUNT(*) FROM scheduled_dm_tasks WHERE status = 'active') +
  (SELECT COUNT(*)
   FROM scheduled_dm_deliveries AS delivery
   JOIN scheduled_dm_tasks AS task ON task.id = delivery.task_id
   WHERE delivery.status IN ('pending', 'sending')
     AND task.status NOT IN ('completed', 'cancelled')) +
  (SELECT COUNT(*) FROM analytics_scheduler_settings WHERE enabled = 1);

DROP TABLE _seed_zero_guard;
DROP TABLE _seed_cancelled_dm_runs;
DROP TABLE _seed_cancelled_jobs;
DROP TABLE _seed_version_guard;

COMMIT;

PRAGMA foreign_key_check;
PRAGMA integrity_check;
