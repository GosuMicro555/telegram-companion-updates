-- +goose Up
CREATE TABLE proxy_profiles (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL,
    protocol TEXT NOT NULL CHECK (protocol IN ('socks5', 'http')),
    host TEXT NOT NULL,
    port INTEGER NOT NULL CHECK (port > 0),
    username TEXT NOT NULL DEFAULT '',
    password_secret TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT true,
    last_health_status TEXT NOT NULL DEFAULT 'unknown',
    last_health_at TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE accounts (
    id UUID PRIMARY KEY,
    display_name TEXT NOT NULL DEFAULT '',
    username TEXT NOT NULL DEFAULT '',
    phone_masked TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    proxy_profile_id UUID REFERENCES proxy_profiles(id),
    proxy_mode TEXT NOT NULL CHECK (proxy_mode IN ('assigned', 'global', 'direct')),
    public_replies_sent BIGINT NOT NULL DEFAULT 0,
    private_messages_sent BIGINT NOT NULL DEFAULT 0,
    last_activity_at TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE account_sessions (
    account_id UUID PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    session_path TEXT NOT NULL,
    imported_from TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE channels (
    id UUID PRIMARY KEY,
    telegram_id TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL,
    link TEXT NOT NULL UNIQUE,
    username TEXT NOT NULL DEFAULT '',
    type TEXT NOT NULL,
    status TEXT NOT NULL,
    active BOOLEAN NOT NULL DEFAULT true,
    sent_count BIGINT NOT NULL DEFAULT 0,
    last_activity_at TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE channel_memberships (
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    is_member BOOLEAN NOT NULL DEFAULT false,
    status TEXT NOT NULL DEFAULT 'unknown',
    last_check_at TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (account_id, channel_id)
);

CREATE TABLE keyword_rules (
    id UUID PRIMARY KEY,
    keyword TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT true,
    action_mode TEXT NOT NULL CHECK (action_mode IN ('public_reply', 'private_message', 'both')),
    public_reply_text TEXT NOT NULL DEFAULT '',
    private_message_text TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE incoming_message_events (
    id UUID PRIMARY KEY,
    channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    telegram_message_id TEXT NOT NULL,
    sender_telegram_id TEXT NOT NULL,
    text_hash TEXT NOT NULL DEFAULT '',
    received_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE outgoing_message_jobs (
    id UUID PRIMARY KEY,
    type TEXT NOT NULL CHECK (type IN ('public_reply', 'private_message')),
    account_id UUID REFERENCES accounts(id),
    channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    rule_id UUID NOT NULL REFERENCES keyword_rules(id) ON DELETE CASCADE,
    target_telegram_id TEXT NOT NULL,
    reply_to_message_id TEXT NOT NULL DEFAULT '',
    text TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'queued',
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE outgoing_message_events (
    id UUID PRIMARY KEY,
    job_id UUID NOT NULL REFERENCES outgoing_message_jobs(id) ON DELETE CASCADE,
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    type TEXT NOT NULL,
    success BOOLEAN NOT NULL,
    error_code TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE app_settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_channels_active ON channels(active);
CREATE INDEX idx_jobs_due ON outgoing_message_jobs(status, next_attempt_at);
CREATE INDEX idx_events_account ON outgoing_message_events(account_id, created_at);
CREATE INDEX idx_events_channel ON outgoing_message_events(channel_id, created_at);

-- +goose Down
DROP TABLE IF EXISTS app_settings;
DROP TABLE IF EXISTS outgoing_message_events;
DROP TABLE IF EXISTS outgoing_message_jobs;
DROP TABLE IF EXISTS incoming_message_events;
DROP TABLE IF EXISTS keyword_rules;
DROP TABLE IF EXISTS channel_memberships;
DROP TABLE IF EXISTS channels;
DROP TABLE IF EXISTS account_sessions;
DROP TABLE IF EXISTS accounts;
DROP TABLE IF EXISTS proxy_profiles;
