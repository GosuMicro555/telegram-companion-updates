-- +goose Up
UPDATE backup_history
SET error = 'backup_failed'
WHERE error <> ''
  AND error NOT IN (
    'backup_cancelled',
    'backup_timeout',
    'backup_unsafe_path',
    'backup_source_unavailable',
    'backup_source_unreadable',
    'backup_history_failed',
    'backup_failed'
  );

-- +goose Down
-- Sanitized failure details cannot and must not be reconstructed.

