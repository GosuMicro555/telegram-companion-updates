# Scout Keyword Analytics Design

**Date:** 2026-07-11  
**Status:** Approved design  
**Primary locale:** Russian, with matching English strings  
**Target platforms:** macOS first; Ubuntu build and acceptance environment

## Goal

Extend Telegram Companion with a pure-Go Telegram runtime, per-account roles, dedicated scout chats, local anonymized 30-day message storage, keyword analytics, permitted local AI/ML imports, moderation, export, adaptive UI scaling, hot reply configuration, and verified daily backups.

## Compliance Boundary

The application has two strictly separated analysis paths:

1. Telegram messages are processed locally with deterministic normalization, morphology, frequency, n-gram, diversity, recency, and rule-based relevance analysis. No AI/ML model receives Telegram message content.
2. Local AI/ML analysis is available only for manually imported files for which the operator confirms that they have the necessary rights. The confirmation, import hash, timestamp, and model version are recorded.

Analytics must not build author profiles. The analytics database must not store sender ID, username, phone number, profile link, display name, or any reversible author identifier. Source messages are associated only with a configured scout chat and Telegram message ID.

The scout role never sends Telegram messages. Analytics-derived keywords may be copied to the existing keyword configuration, but outgoing automation remains limited to separately configured outbound channels and spammer-role accounts.

## Architecture

The production runtime is one Wails/Go process. Python and Telethon are removed after session migration acceptance.

```text
Wails React UI
    |
Wails bindings
    |
Application use cases
    |-- Account Registry
    |-- Runtime Coordinator
    |-- Outbound Automation
    |-- Scout Collector
    |-- Keyword Analytics
    |-- Import AI Analyzer
    |-- Retention Worker
    |-- Backup Manager
    `-- Export Service
    |
Repositories (SQLite)
    |
gotd/td MTProto clients
```

Dependencies point inward through domain ports. The gotd adapter, SQLite repository, keyring, ONNX provider, Wails bindings, and OS file opener are replaceable adapters.

## Telegram Runtime

### Session Migration

- Use the current maintained `github.com/gotd/td` session and tdesktop packages.
- Import each account from a copy of its Telegram Desktop `tdata` using `session.TDesktopSession`.
- Never mutate the source archives or extracted tdata directories.
- Persist each converted gotd session in its own protected directory.
- Validate authorization and account identity before activating the converted session.
- Keep the existing runtime available until all three accounts pass migration acceptance.
- A failed account migration does not affect already validated accounts.

### Account Roles

Each account has exactly one role:

- `spammer`: default for existing and newly imported accounts; eligible only for outbound automation.
- `scout_analyst`: receives and stores updates only from active scout chats; never eligible for any send method.

Role changes are persisted and applied immediately by the runtime coordinator. The coordinator replaces an immutable runtime configuration snapshot and updates handlers without restarting the application.

### Channel Isolation

The system maintains separate catalogs:

- `outbound_channels` for spammer-role automation;
- `scout_chats` for scout collection.

A scout account must never join an outbound channel as part of outbound-channel import. A spammer account must never join a scout chat as part of scout-chat import. Existing outbound channels are not migrated into scout chats automatically.

### Update Handling

- Use the gotd update/gap engine for ordering, reconnect recovery, and missed-update reconciliation.
- Collection begins when a scout role and scout chat become active. No historical backfill is performed.
- Deduplicate across accounts with a unique `(telegram_chat_id, telegram_message_id)` constraint.
- Message edits replace the encrypted local text and derived state.
- Telegram deletion updates remove the corresponding local raw message.
- FloodWait and connection errors remain scoped to the affected account.
- Reconnect uses bounded exponential backoff with jitter and respects context cancellation.

## Channel Topics

Every newly added outbound channel and scout chat requires a source topic.

- A topic is free-form text normalized for comparison but retains the entered display form.
- The import form supports choosing an existing topic or entering a new one.
- Bulk import applies one required topic to every link in the submitted list.
- The topic can later be edited for one channel or changed in bulk.
- Existing legacy channels migrate to `Без тематики`; the UI requests a real topic before further bulk operations.
- Analytics supports `Все тематики`, one selected topic, and comparison across topics.

Source topic and semantic analysis profile are separate concepts:

- source topic describes the configured chat, such as `Игры` or `Покупки`;
- analysis profile describes the content sought, such as `Нехватка денег`.

## Data Model

Use embedded SQLite with versioned migrations. Configuration currently stored in BoltDB is migrated transactionally after an automatic pre-migration backup.

### Core Tables

- `accounts`: identity, role, status, session path, proxy profile, counters, last activity, last error.
- `outbound_channels`: outbound-only chat configuration and topic.
- `scout_chats`: scout-only chat configuration, topic, status, message count, last activity.
- `account_channel_memberships`: membership state by account and catalog type.
- `scout_messages`: chat ID, message ID, encrypted text, message timestamp, received timestamp, edit timestamp, source kind.
- `imports`: file name, SHA-256, rights confirmation, timestamp, status, model version.
- `analysis_profiles`: name, description, positive examples, exclusions, deterministic rule groups.
- `analysis_runs`: source scope, profile, status, progress, counts, start/end timestamps, error.
- `keyword_candidates`: normalized value, display value, kind, frequency, source diversity, score, source, moderation state, run ID.
- `moderation_decisions`: stable normalized candidate key, state, timestamp.
- `app_settings`: versioned runtime settings, rate limits, direct-message settings, shared reply, UI scale.
- `backup_history`: archive, type, size, hash, verification status, timestamps, error.

### Message Privacy

- Encrypt raw message text at the application layer.
- Store the encryption key in macOS Keychain or Linux Secret Service.
- Database and session files use owner-only filesystem permissions.
- Do not log raw text or session material.
- Do not export source messages.
- Protected-content chats cannot expose source text through copy or export.

## Retention and Storage Metrics

- Raw Telegram messages and message-level derived data expire after 30 days.
- The retention worker runs hourly and removes expired rows within one hour of expiry.
- Approved/rejected keyword moderation decisions and aggregate counts may remain without source text or author data.
- SQLite incremental maintenance runs after pruning; disruptive full compaction is scheduled only when justified by free-page thresholds.
- The UI refreshes record count and physical database size every 60 seconds.
- Low-disk thresholds are configurable. Collection pauses before the database can exhaust the disk and reports a visible error.

## Keyword Analysis

Pressing `Анализ keyword` starts one cancellable background run. A new partial run never replaces the last successful result.

### Candidate Generation

1. Normalize Unicode, lowercase, and map `ё` to `е` for matching while retaining the most common surface form.
2. Segment sentences before punctuation filtering.
3. Tokenize words, numbers, currency markers, links, and usernames separately.
4. Normalize Russian word forms using a deterministic tested morphology/stemming component.
5. Generate contiguous 1-4 word n-grams within sentence boundaries.
6. Remove service tokens, links, username mentions, low-quality fragments, and configured stopwords after phrase generation.
7. Deduplicate exact messages and near-duplicate reposts before frequency ranking.

### Outputs

- General top 100 keyword phrases, ranked without a semantic theme.
- General top 30 individual words.
- Thematic top 100 for the selected analysis profile.
- Topic filters based on configured channel topics.
- Separate source labels: `Правила Telegram` and `AI: импорт`.

### Deterministic Telegram Ranking

Telegram content uses explainable scoring only:

```text
relevance confidence
  * log(frequency + 1)
  * source-chat diversity
  * recency weight
  * phrase association
  * novelty
  * duplicate penalty
```

The default `Нехватка денег` profile contains auditable concept groups for money, shortage, debt, loss, purchase intent, and suppressors. Profiles are editable and can be added later without changing the analyzer.

### AI/ML Import Ranking

- AI/ML runs only for rights-confirmed imported files.
- Use a local multilingual sentence-embedding model in ONNX format.
- Run inference in-process through a Go AI provider with platform-specific ONNX Runtime assets.
- No cloud API receives imported or Telegram content.
- Semantic similarity uses the selected profile description, positive examples, and exclusions.
- Model score is combined with the same frequency, diversity, phrase quality, and duplicate controls used by deterministic analysis.
- Store model name, model hash, tokenizer hash, and thresholds with each run for reproducibility.

## Moderation and Export

Candidate states are:

- `new`;
- `accepted`;
- `rejected`;
- `added_to_keywords`.

Requirements:

- Copy one candidate.
- Copy all currently filtered candidates.
- Accept or reject one candidate inline.
- Rejecting a candidate persists a suppression key so it does not reappear in later runs until moderation is reset.
- `Добавить всё` adds new and accepted candidates to the existing keyword list, preserving existing values and removing case-insensitive duplicates.
- Export writes an atomic UTF-8 file at `data/exports/keywords.txt`.
- `Открыть файл с ключами` reveals or opens the file through an injected OS adapter using Finder on macOS and the configured file manager on Ubuntu.

## Hot Configuration

All mutable runtime settings use immutable, versioned snapshots.

- Saving `SharedReply` persists the exact entered text without trimming or substituting a default, except validation that the value is not empty when a rule requires a reply.
- The next outbound message reads the latest committed reply snapshot without STOP/START.
- Keyword, role, channel, topic, rate-limit, direct-message, and proxy changes also publish a new revision.
- Consumers acknowledge applied revisions for UI status and diagnostics.
- Snapshot replacement is race-safe and does not mutate slices or maps shared by active goroutines.

## Daily Backups

Create backups every day at 03:00 local time.

Backup scope:

- application SQLite database;
- analytics data;
- role/channel configuration;
- gotd session and peer storage;
- exported keyword file;
- manifest containing schema version, timestamps, sizes, and SHA-256 values.

Policy:

- Store encrypted archives in `data/backups`.
- Retain 14 daily backups and 3 monthly backups.
- Create an additional backup before every schema migration.
- Use the SQLite online backup mechanism so the application does not need to stop.
- Coordinate a short read barrier for gotd session and peer stores, copy them to a temporary snapshot, and release the barrier before archive compression.
- Verify archive integrity and open a copied database before marking a backup successful.
- Rotate old archives only after the new archive is verified.
- Show last successful backup, size, and failure state in the UI.
- Provide a documented restore procedure with schema compatibility checks.
- Keep the backup-encryption key in the OS keyring and provide an explicit recovery-key export flow for restoration on another machine.

## User Interface

Preserve the existing yellow Telegram logo, application icon, Lucide icons, Russian primary copy, and matching English translations.

### Navigation

- `Каналы`
- `Аккаунты`
- `Ключевые слова`
- `Аналитика keyword`
- `Статистика`
- `Настройки`

### Accounts

Each account has a two-option segmented role control:

- `Бот спамер`;
- `Бот разведчик-аналитик`.

Changing to scout immediately starts eligible collection and displays the collection state.

### Channels

Top tabs:

- `Каналы ботов-спамеров`;
- `Каналы разведчиков`;
- `Тематики`.

Single and bulk imports require a topic. Scout rows show title, topic, collection status, message count, last activity, and active toggle. Status and message count are separate columns with sufficient spacing.

### Analytics

The header contains `Анализ keyword`, database record count, database size, last successful analysis, and last backup.

Tabs:

- `Тематические`;
- `Общие слова`;
- `Импорты AI`.

Controls include profile/topic filters, copy all, add all, and open file. Tables reuse the keyword-table visual language and use a 25 px CSS row height at 100% scale. Actions use compact icon buttons with tooltips.

### Responsive Layout and Zoom

The interface must be visually verified at:

- Full HD `1920x1080`;
- `2880x1864` while preserving a 16:10 composition inside the available workspace.

Responsive breakpoints adjust sidebar width, content padding, table columns, and toolbar wrapping. Text and actions must not overlap or become inaccessible.

UI scale steps:

- 80%;
- 90%;
- 100%;
- 110%;
- 125%;
- 150%.

Keyboard behavior:

- `Ctrl++`: next scale step;
- `Ctrl+-`: previous scale step;
- `Ctrl+0`: automatic scale;
- persist the selected scale in settings;
- automatic default is 100% for Full HD and 125% for the 2880x1864 target, subject to detected DPI and usable viewport.

## Error Handling and Observability

- Structured `slog` logging without content or secrets.
- Per-account states: ready, joining, partial, error, flood wait, paused, collecting.
- Per-analysis states: idle, queued, running, cancelling, complete, partial, error.
- Errors remain scoped to the affected account, chat, analysis run, or backup.
- Graceful shutdown cancels workers, flushes state, closes SQLite, and closes all gotd clients.
- The application must recover cleanly after abrupt termination using SQLite transactions and idempotent update keys.

## Testing and Acceptance

### Unit Tests

- Account role eligibility and strict no-send scout invariant.
- Runtime snapshot revision and concurrent readers.
- Exact preservation and hot application of shared reply text.
- Topic normalization and required import validation.
- Russian normalization, n-grams, stopwords, phrase scoring, and rule profiles.
- Candidate moderation and suppression persistence.
- Backup retention selection.

### Integration Tests

- SQLite migrations and BoltDB compatibility import.
- Message encryption/decryption and retention pruning.
- Deduplication of the same Telegram update received by multiple accounts.
- gotd session conversion from copied tdata fixtures without source mutation.
- Backup, simulated database loss, restore, and hash verification.
- ONNX analysis only for rights-confirmed import records.

### Frontend Tests

- Role changes, scout/outbound tabs, and required topic fields.
- 25 px compact tables and stable action dimensions.
- Search, moderation, copy, add-all, and file-open actions.
- Database metrics refresh every 60 seconds.
- Zoom shortcuts, clamping, reset, and persistence.
- Russian and English translation parity.

### Desktop Acceptance

- Visual screenshots and overlap checks at both target resolutions and every zoom step.
- Import all three copied tdata accounts and verify identity.
- Confirm scout accounts never send during instrumented collection.
- Confirm outbound accounts do not join scout chats through scout import.
- Verify new Telegram messages are collected without author fields.
- Verify no historical messages are backfilled.
- Verify shared reply changes affect the next message without STOP/START.
- Run a deterministic Telegram analysis and a separate rights-confirmed AI import analysis.
- Verify daily backup status and perform one restore drill.

## Migration Strategy

1. Add SQLite and migration infrastructure without changing the running listener.
2. Import existing BoltDB settings and preserve existing keywords, direct-message semantics, reply text, and channel state.
3. Convert tdata copies to gotd sessions and validate accounts individually.
4. Run gotd in read-only shadow mode against the test chat while Telethon remains authoritative.
5. Switch collection to gotd and verify gap recovery.
6. Switch outbound automation to gotd after rate-limit and round-robin acceptance.
7. Remove Python/Telethon runtime assets only after all acceptance tests pass.

## Out of Scope

- Historical message backfill.
- AI/ML inference over Telegram message content.
- Author-level analytics or profiles.
- Cloud AI APIs.
- Server-cluster deployment.
- Proxy implementation changes beyond preserving the existing adapter boundary.
