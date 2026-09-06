-- +goose Up
DELETE FROM outgoing_message_events
WHERE job_id IN (
  SELECT id FROM outgoing_message_jobs
  WHERE channel_id IN ('boat-chat-outbound', 'boat-chat-scout')
);
DELETE FROM outgoing_message_jobs
WHERE channel_id IN ('boat-chat-outbound', 'boat-chat-scout');

-- +goose Down
-- Removed acceptance-only delivery work must never be recreated by rollback.
