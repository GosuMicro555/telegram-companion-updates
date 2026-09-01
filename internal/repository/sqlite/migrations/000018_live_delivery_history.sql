-- +goose Up
CREATE TABLE live_delivery_history (
  id TEXT PRIMARY KEY,
  job_id TEXT NOT NULL UNIQUE,
  source_message TEXT NOT NULL,
  trigger_canonical_id TEXT REFERENCES canonical_keywords(id) ON DELETE SET NULL,
  trigger_snapshot TEXT NOT NULL,
  triggered_at TEXT NOT NULL,
  delivery_type TEXT CHECK(delivery_type IS NULL OR delivery_type IN ('private_message','public_reply')),
  account_id TEXT,
  account_title_snapshot TEXT NOT NULL DEFAULT '',
  final_status TEXT CHECK(final_status IS NULL OR final_status IN ('successful','not_delivered')),
  error_code TEXT NOT NULL DEFAULT '',
  finalized_at TEXT
);
CREATE INDEX idx_live_history_triggered ON live_delivery_history(triggered_at DESC,id DESC);
CREATE INDEX idx_live_history_status_time ON live_delivery_history(final_status,triggered_at DESC);
CREATE INDEX idx_live_history_canonical ON live_delivery_history(trigger_canonical_id);

-- +goose Down
DROP INDEX idx_live_history_canonical;
DROP INDEX idx_live_history_status_time;
DROP INDEX idx_live_history_triggered;
DROP TABLE live_delivery_history;
