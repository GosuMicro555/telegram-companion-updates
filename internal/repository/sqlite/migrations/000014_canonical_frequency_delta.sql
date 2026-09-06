-- +goose Up
ALTER TABLE canonical_keywords ADD COLUMN frequency_delta INTEGER NOT NULL DEFAULT 0;

-- +goose Down
CREATE TABLE canonical_keywords_v14_down (
  id TEXT PRIMARY KEY,
  canonical_value TEXT NOT NULL,
  language TEXT NOT NULL CHECK(language IN ('ru','en')),
  class TEXT NOT NULL DEFAULT 'neutral' CHECK(class IN ('neutral','positive','negative','service')),
  decision_source TEXT NOT NULL DEFAULT 'manual' CHECK(decision_source IN ('manual','auto')),
  trigger_active INTEGER NOT NULL DEFAULT 0 CHECK(trigger_active IN (0,1)),
  frequency INTEGER NOT NULL DEFAULT 0 CHECK(frequency >= 0),
  message_count INTEGER NOT NULL DEFAULT 0 CHECK(message_count >= 0),
  last_seen_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(language, canonical_value)
);
INSERT INTO canonical_keywords_v14_down
  (id, canonical_value, language, class, decision_source, trigger_active, frequency, message_count, last_seen_at, created_at, updated_at)
SELECT id, canonical_value, language, class, decision_source, trigger_active, frequency, message_count, last_seen_at, created_at, updated_at
FROM canonical_keywords;
CREATE TABLE canonical_keyword_forms_v14_down (
  normalized_value TEXT PRIMARY KEY,
  canonical_id TEXT NOT NULL REFERENCES canonical_keywords_v14_down(id) ON DELETE CASCADE,
  display_value TEXT NOT NULL,
  frequency INTEGER NOT NULL DEFAULT 0 CHECK(frequency >= 0),
  manual_override INTEGER NOT NULL DEFAULT 0 CHECK(manual_override IN (0,1)),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
INSERT INTO canonical_keyword_forms_v14_down
  (normalized_value, canonical_id, display_value, frequency, manual_override, created_at, updated_at)
SELECT normalized_value, canonical_id, display_value, frequency, manual_override, created_at, updated_at
FROM canonical_keyword_forms;
DROP TABLE canonical_keyword_forms;
DROP TABLE canonical_keywords;
ALTER TABLE canonical_keywords_v14_down RENAME TO canonical_keywords;
ALTER TABLE canonical_keyword_forms_v14_down RENAME TO canonical_keyword_forms;
CREATE INDEX idx_canonical_keywords_class ON canonical_keywords(class, frequency DESC, canonical_value);
CREATE INDEX idx_canonical_forms_canonical ON canonical_keyword_forms(canonical_id, frequency DESC, normalized_value);
