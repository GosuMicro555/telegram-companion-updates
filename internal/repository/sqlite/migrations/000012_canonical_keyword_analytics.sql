-- +goose Up
CREATE TABLE canonical_keywords (
  id TEXT PRIMARY KEY,
  canonical_value TEXT NOT NULL,
  language TEXT NOT NULL CHECK(language IN ('ru','en')),
  class TEXT NOT NULL DEFAULT 'neutral' CHECK(class IN ('neutral','positive','negative')),
  decision_source TEXT NOT NULL DEFAULT 'manual' CHECK(decision_source IN ('manual','auto')),
  trigger_active INTEGER NOT NULL DEFAULT 0 CHECK(trigger_active IN (0,1)),
  frequency INTEGER NOT NULL DEFAULT 0 CHECK(frequency >= 0),
  message_count INTEGER NOT NULL DEFAULT 0 CHECK(message_count >= 0),
  last_seen_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(language, canonical_value)
);

CREATE TABLE canonical_keyword_forms (
  normalized_value TEXT PRIMARY KEY,
  canonical_id TEXT NOT NULL REFERENCES canonical_keywords(id) ON DELETE CASCADE,
  display_value TEXT NOT NULL,
  frequency INTEGER NOT NULL DEFAULT 0 CHECK(frequency >= 0),
  manual_override INTEGER NOT NULL DEFAULT 0 CHECK(manual_override IN (0,1)),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX idx_canonical_keywords_class ON canonical_keywords(class, frequency DESC, canonical_value);
CREATE INDEX idx_canonical_forms_canonical ON canonical_keyword_forms(canonical_id, frequency DESC, normalized_value);

DELETE FROM scout_messages;
DELETE FROM scout_message_tombstones;
DELETE FROM keyword_candidates;
DELETE FROM moderation_decisions;
DELETE FROM analysis_runs;
DELETE FROM analysis_profiles;
DELETE FROM imports;
DELETE FROM backup_history;
DELETE FROM outgoing_message_events;
DELETE FROM outgoing_message_jobs;
DELETE FROM app_settings WHERE key LIKE 'analysis_result:%' OR key = 'current_successful_run_id';
UPDATE app_settings
SET value_json = json_set(value_json, '$.keywords', json('[]'), '$.directMessageKeywords', json('[]')),
    revision = revision + 1,
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE key = 'keyword_settings';
UPDATE accounts SET public_replies_sent = 0, private_messages_sent = 0, last_activity_at = NULL;
UPDATE outbound_channels SET sent_count = 0, last_activity_at = NULL;
UPDATE scout_chats SET message_count = 0, last_activity_at = NULL;

-- +goose Down
DROP TABLE canonical_keyword_forms;
DROP TABLE canonical_keywords;
