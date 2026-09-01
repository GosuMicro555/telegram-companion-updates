-- +goose Up
-- SQLite cannot widen a CHECK constraint in place. Copying these two tables
-- preserves canonical rows and forms while allowing the additive service class.
CREATE TABLE canonical_keywords_v13 (
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
INSERT INTO canonical_keywords_v13
  (id, canonical_value, language, class, decision_source, trigger_active, frequency, message_count, last_seen_at, created_at, updated_at)
SELECT id, canonical_value, language, class, decision_source, trigger_active, frequency, message_count, last_seen_at, created_at, updated_at
FROM canonical_keywords;

CREATE TABLE canonical_keyword_forms_v13 (
  normalized_value TEXT PRIMARY KEY,
  canonical_id TEXT NOT NULL REFERENCES canonical_keywords_v13(id) ON DELETE CASCADE,
  display_value TEXT NOT NULL,
  frequency INTEGER NOT NULL DEFAULT 0 CHECK(frequency >= 0),
  manual_override INTEGER NOT NULL DEFAULT 0 CHECK(manual_override IN (0,1)),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
INSERT INTO canonical_keyword_forms_v13
  (normalized_value, canonical_id, display_value, frequency, manual_override, created_at, updated_at)
SELECT normalized_value, canonical_id, display_value, frequency, manual_override, created_at, updated_at
FROM canonical_keyword_forms;
DROP TABLE canonical_keyword_forms;
DROP TABLE canonical_keywords;
ALTER TABLE canonical_keywords_v13 RENAME TO canonical_keywords;
ALTER TABLE canonical_keyword_forms_v13 RENAME TO canonical_keyword_forms;
CREATE INDEX idx_canonical_keywords_class ON canonical_keywords(class, frequency DESC, canonical_value);
CREATE INDEX idx_canonical_forms_canonical ON canonical_keyword_forms(canonical_id, frequency DESC, normalized_value);

CREATE TABLE analytics_scheduler_settings (
  singleton INTEGER PRIMARY KEY CHECK(singleton = 1),
  enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0, 1)),
  interval_minutes INTEGER NOT NULL DEFAULT 10 CHECK(interval_minutes IN (1, 5, 10, 30, 60, 120)),
  updated_at TEXT NOT NULL
);
INSERT INTO analytics_scheduler_settings(singleton, enabled, interval_minutes, updated_at)
VALUES (1, 1, 10, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
ON CONFLICT(singleton) DO NOTHING;

CREATE TABLE analytics_scout_cursors (
  account_id TEXT NOT NULL,
  chat_id TEXT NOT NULL,
  message_id INTEGER NOT NULL CHECK(message_id >= 0),
  updated_at TEXT NOT NULL,
  PRIMARY KEY(account_id, chat_id)
);

CREATE TABLE analytics_scheduler_runs (
  id TEXT PRIMARY KEY,
  started_at TEXT NOT NULL,
  finished_at TEXT,
  new_messages INTEGER NOT NULL DEFAULT 0 CHECK(new_messages >= 0),
  extracted_words INTEGER NOT NULL DEFAULT 0 CHECK(extracted_words >= 0),
  new_canonicals INTEGER NOT NULL DEFAULT 0 CHECK(new_canonicals >= 0),
  processed_groups INTEGER NOT NULL DEFAULT 0 CHECK(processed_groups >= 0),
  duration_ms INTEGER NOT NULL DEFAULT 0 CHECK(duration_ms >= 0),
  errors_json TEXT NOT NULL DEFAULT '[]'
);
CREATE INDEX idx_analytics_scheduler_runs_started_at ON analytics_scheduler_runs(started_at DESC);

CREATE TABLE analytics_service_words (
  language TEXT NOT NULL CHECK(language IN ('ru', 'en')),
  value TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY(language, value)
);
WITH service_word_seed(language, value) AS (VALUES
  ('ru', 'без'), ('ru', 'бы'), ('ru', 'вас'), ('ru', 'во'), ('ru', 'вот'),
  ('ru', 'вы'), ('ru', 'да'), ('ru', 'для'), ('ru', 'до'), ('ru', 'его'),
  ('ru', 'ее'), ('ru', 'если'), ('ru', 'еще'), ('ru', 'же'), ('ru', 'за'),
  ('ru', 'из'), ('ru', 'или'), ('ru', 'им'), ('ru', 'их'), ('ru', 'как'),
  ('ru', 'когда'), ('ru', 'кто'), ('ru', 'ли'), ('ru', 'либо'), ('ru', 'меня'),
  ('ru', 'мне'), ('ru', 'мы'), ('ru', 'на'), ('ru', 'не'), ('ru', 'но'),
  ('ru', 'об'), ('ru', 'он'), ('ru', 'она'), ('ru', 'они'), ('ru', 'от'),
  ('ru', 'по'), ('ru', 'при'), ('ru', 'себя'), ('ru', 'со'), ('ru', 'тебе'),
  ('ru', 'тебя'), ('ru', 'то'), ('ru', 'уже'), ('ru', 'что'), ('ru', 'чтобы'),
  ('ru', 'это'),
  ('en', 'a'), ('en', 'an'), ('en', 'and'), ('en', 'as'), ('en', 'at'),
  ('en', 'but'), ('en', 'by'), ('en', 'for'), ('en', 'from'), ('en', 'he'),
  ('en', 'her'), ('en', 'him'), ('en', 'his'), ('en', 'if'), ('en', 'in'),
  ('en', 'into'), ('en', 'it'), ('en', 'its'), ('en', 'me'), ('en', 'my'),
  ('en', 'nor'), ('en', 'not'), ('en', 'of'), ('en', 'on'), ('en', 'or'),
  ('en', 'our'), ('en', 'she'), ('en', 'so'), ('en', 'than'), ('en', 'that'),
  ('en', 'the'), ('en', 'their'), ('en', 'them'), ('en', 'they'), ('en', 'this'),
  ('en', 'to'), ('en', 'us'), ('en', 'we'), ('en', 'with'), ('en', 'you')
)
INSERT INTO analytics_service_words(language, value, created_at, updated_at)
SELECT language, value, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
FROM service_word_seed;

CREATE TABLE analytics_table_preferences (
  tab TEXT PRIMARY KEY,
  columns_json TEXT NOT NULL DEFAULT '[]',
  sort_by TEXT NOT NULL,
  sort_direction TEXT NOT NULL DEFAULT 'none' CHECK(sort_direction IN ('none', 'ascending', 'descending')),
  page_size INTEGER NOT NULL CHECK(page_size > 0),
  updated_at TEXT NOT NULL
);

-- +goose Down
DROP TABLE analytics_table_preferences;
DROP TABLE analytics_service_words;
DROP TABLE analytics_scheduler_runs;
DROP TABLE analytics_scout_cursors;
DROP TABLE analytics_scheduler_settings;
