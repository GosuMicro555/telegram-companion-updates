-- +goose Up
DELETE FROM account_channel_memberships
WHERE (catalog = 'outbound' AND channel_id = 'boat-chat-outbound')
   OR (catalog = 'scout' AND channel_id = 'boat-chat-scout');
DELETE FROM outbound_channels WHERE id = 'boat-chat-outbound';
DELETE FROM scout_chats WHERE id = 'boat-chat-scout';

-- +goose Down
-- Removed acceptance-only rows must never be recreated by rollback.
