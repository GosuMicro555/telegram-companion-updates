-- +goose Up
CREATE TABLE accounts (
  id TEXT PRIMARY KEY,
  phone_masked TEXT NOT NULL,
  display_name TEXT NOT NULL DEFAULT '',
  role TEXT NOT NULL DEFAULT 'spammer' CHECK(role IN ('spammer','scout_analyst')),
  status TEXT NOT NULL DEFAULT 'paused' CHECK(status IN ('ready','joining','partial','error','flood_wait','paused','collecting')),
  session_path TEXT NOT NULL,
  proxy_profile_id TEXT,
  public_replies_sent INTEGER NOT NULL DEFAULT 0 CHECK(public_replies_sent >= 0),
  private_messages_sent INTEGER NOT NULL DEFAULT 0 CHECK(private_messages_sent >= 0),
  last_activity_at TEXT,
  last_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE outbound_channels (
  id TEXT PRIMARY KEY,
  telegram_chat_id TEXT NOT NULL UNIQUE,
  title TEXT NOT NULL DEFAULT '',
  link TEXT NOT NULL UNIQUE,
  topic TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'paused' CHECK(status IN ('ready','joining','partial','error','flood_wait','paused')),
  active INTEGER NOT NULL DEFAULT 1 CHECK(active IN (0,1)),
  sent_count INTEGER NOT NULL DEFAULT 0 CHECK(sent_count >= 0),
  last_activity_at TEXT,
  last_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE scout_chats (
  id TEXT PRIMARY KEY,
  telegram_chat_id TEXT NOT NULL UNIQUE,
  title TEXT NOT NULL DEFAULT '',
  link TEXT NOT NULL UNIQUE,
  topic TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'paused' CHECK(status IN ('ready','joining','partial','error','flood_wait','paused','collecting')),
  active INTEGER NOT NULL DEFAULT 1 CHECK(active IN (0,1)),
  message_count INTEGER NOT NULL DEFAULT 0 CHECK(message_count >= 0),
  last_activity_at TEXT,
  last_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE account_channel_memberships (
  account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  catalog TEXT NOT NULL CHECK(catalog IN ('outbound','scout')),
  channel_id TEXT NOT NULL,
  is_member INTEGER NOT NULL DEFAULT 0 CHECK(is_member IN (0,1)),
  status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','joining','member','error','paused')),
  last_check_at TEXT,
  last_error TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(account_id,catalog,channel_id)
);

CREATE TABLE scout_messages (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  telegram_chat_id TEXT NOT NULL REFERENCES scout_chats(telegram_chat_id) ON DELETE CASCADE,
  telegram_message_id INTEGER NOT NULL,
  encrypted_text BLOB NOT NULL,
  nonce BLOB NOT NULL,
  message_at TEXT NOT NULL,
  received_at TEXT NOT NULL,
  edited_at TEXT,
  source_kind TEXT NOT NULL DEFAULT 'telegram' CHECK(source_kind IN ('telegram')),
  UNIQUE(telegram_chat_id, telegram_message_id)
);
CREATE INDEX idx_scout_messages_retention ON scout_messages(message_at);
CREATE INDEX idx_scout_messages_chat_time ON scout_messages(telegram_chat_id, message_at);

CREATE TABLE imports (
  id TEXT PRIMARY KEY,
  file_name TEXT NOT NULL,
  file_path TEXT NOT NULL,
  sha256 TEXT NOT NULL UNIQUE,
  rights_confirmed INTEGER NOT NULL CHECK(rights_confirmed IN (0,1)),
  imported_at TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','running','complete','error')),
  model_name TEXT NOT NULL DEFAULT '',
  model_sha256 TEXT NOT NULL DEFAULT '',
  tokenizer_sha256 TEXT NOT NULL DEFAULT '',
  last_error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_imports_imported_at ON imports(imported_at);

CREATE TABLE analysis_profiles (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  description TEXT NOT NULL DEFAULT '',
  positive_examples_json TEXT NOT NULL DEFAULT '[]',
  exclusions_json TEXT NOT NULL DEFAULT '[]',
  rule_groups_json TEXT NOT NULL DEFAULT '[]',
  enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE analysis_runs (
  id TEXT PRIMARY KEY,
  source_scope TEXT NOT NULL CHECK(source_scope IN ('all','topic')),
  profile_id TEXT REFERENCES analysis_profiles(id) ON DELETE SET NULL,
  topic TEXT,
  status TEXT NOT NULL DEFAULT 'queued' CHECK(status IN ('idle','queued','running','cancelling','complete','partial','error')),
  progress INTEGER NOT NULL DEFAULT 0 CHECK(progress BETWEEN 0 AND 100),
  input_count INTEGER NOT NULL DEFAULT 0 CHECK(input_count >= 0),
  candidate_count INTEGER NOT NULL DEFAULT 0 CHECK(candidate_count >= 0),
  model_metadata_json TEXT NOT NULL DEFAULT '{}',
  started_at TEXT NOT NULL,
  completed_at TEXT,
  error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_analysis_runs_started_at ON analysis_runs(started_at);

CREATE TABLE keyword_candidates (
  id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL REFERENCES analysis_runs(id) ON DELETE CASCADE,
  normalized_value TEXT NOT NULL,
  display_value TEXT NOT NULL,
  kind TEXT NOT NULL CHECK(kind IN ('word','phrase')),
  frequency INTEGER NOT NULL DEFAULT 0 CHECK(frequency >= 0),
  source_diversity INTEGER NOT NULL DEFAULT 0 CHECK(source_diversity >= 0),
  score REAL NOT NULL DEFAULT 0,
  source TEXT NOT NULL CHECK(source IN ('telegram','import_ai')),
  moderation_state TEXT NOT NULL DEFAULT 'new' CHECK(moderation_state IN ('new','accepted','rejected','added_to_keywords')),
  created_at TEXT NOT NULL,
  UNIQUE(run_id,normalized_value,kind,source)
);
CREATE INDEX idx_keyword_candidates_created_at ON keyword_candidates(created_at);

CREATE TABLE moderation_decisions (
  normalized_value TEXT NOT NULL,
  kind TEXT NOT NULL CHECK(kind IN ('word','phrase')),
  state TEXT NOT NULL CHECK(state IN ('new','accepted','rejected','added_to_keywords')),
  decided_at TEXT NOT NULL,
  PRIMARY KEY(normalized_value,kind)
);

CREATE TABLE app_settings (
  key TEXT PRIMARY KEY,
  value_json TEXT NOT NULL,
  revision INTEGER NOT NULL DEFAULT 1 CHECK(revision >= 1),
  updated_at TEXT NOT NULL
);

CREATE TABLE backup_history (
  id TEXT PRIMARY KEY,
  archive_path TEXT NOT NULL UNIQUE,
  kind TEXT NOT NULL CHECK(kind IN ('daily','monthly','pre_migration')),
  size_bytes INTEGER NOT NULL DEFAULT 0 CHECK(size_bytes >= 0),
  sha256 TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','running','verified','error')),
  created_at TEXT NOT NULL,
  verified_at TEXT,
  error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_backup_history_created_at ON backup_history(created_at);

-- +goose Down
DROP TABLE backup_history;
DROP TABLE app_settings;
DROP TABLE moderation_decisions;
DROP TABLE keyword_candidates;
DROP TABLE analysis_runs;
DROP TABLE analysis_profiles;
DROP TABLE imports;
DROP TABLE scout_messages;
DROP TABLE account_channel_memberships;
DROP TABLE scout_chats;
DROP TABLE outbound_channels;
DROP TABLE accounts;
