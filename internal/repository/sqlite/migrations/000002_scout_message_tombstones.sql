-- +goose Up
-- Tombstones retain only message identity and deletion time for at least the
-- same 30-day window as scout messages. They never contain text or author data.
CREATE TABLE scout_message_tombstones (
  telegram_chat_id TEXT NOT NULL,
  telegram_message_id INTEGER NOT NULL,
  deleted_at TEXT NOT NULL,
  PRIMARY KEY(telegram_chat_id, telegram_message_id)
);
CREATE INDEX idx_scout_message_tombstones_retention
  ON scout_message_tombstones(deleted_at);

-- Normalize timestamps written by Task 7 before this migration so lexical
-- comparisons retain full nanosecond ordering.
UPDATE scout_messages SET
  message_at = CASE
    WHEN instr(message_at, '.') = 0 THEN substr(message_at, 1, 19) || '.000000000Z'
    ELSE substr(message_at, 1, 20) || substr(substr(message_at, 21, length(message_at) - 21) || '000000000', 1, 9) || 'Z'
  END,
  received_at = CASE
    WHEN instr(received_at, '.') = 0 THEN substr(received_at, 1, 19) || '.000000000Z'
    ELSE substr(received_at, 1, 20) || substr(substr(received_at, 21, length(received_at) - 21) || '000000000', 1, 9) || 'Z'
  END,
  edited_at = CASE
    WHEN edited_at IS NULL THEN NULL
    WHEN instr(edited_at, '.') = 0 THEN substr(edited_at, 1, 19) || '.000000000Z'
    ELSE substr(edited_at, 1, 20) || substr(substr(edited_at, 21, length(edited_at) - 21) || '000000000', 1, 9) || 'Z'
  END;

-- +goose Down
DROP TABLE scout_message_tombstones;
