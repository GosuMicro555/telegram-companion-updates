-- +goose Up
UPDATE account_channel_memberships
SET rest_duration_hours = 36
WHERE rest_duration_hours = 0
  AND status IN ('joining', 'pending_approval');

-- +goose Down
SELECT 1;
