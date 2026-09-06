# Scout Keyword Analytics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the Python/Telethon runtime with a pure-Go gotd runtime and add anonymized scout collection, channel topics, keyword analytics, permitted local AI imports, hot settings, adaptive UI scaling, and verified daily backups.

**Architecture:** The Wails desktop process owns application use cases, an embedded SQLite database, one gotd client per account, deterministic Telegram analytics, and an optional in-process ONNX analyzer for rights-confirmed imports. Domain ports isolate Telegram, storage, secrets, AI, file opening, and backup behavior. Migration is additive and Telethon remains authoritative until gotd shadow-mode acceptance succeeds for all three existing accounts.

**Tech Stack:** Go 1.25.7+, Wails v2.13+, React/TypeScript, gotd/td v0.160.0, modernc.org/sqlite v1.53.0, go-keyring v0.2.8, Testify v1.11.1, Hugot v0.7.5, ONNX Runtime 1.24.x, Vitest, golangci-lint.

## Global Constraints

- macOS is the primary target; Ubuntu is the current build and acceptance environment.
- Telegram content never enters AI/ML inference; AI/ML accepts only manually imported files with recorded rights confirmation.
- Analytics stores no sender ID, username, phone number, display name, profile link, or reversible author identifier.
- Raw Telegram messages and message-level derived rows expire after 30 days; pruning runs hourly.
- A `scout_analyst` account never calls a Telegram send method.
- Existing accounts default to `spammer`; outbound and scout channel catalogs remain separate.
- New single and bulk channel imports require a topic; bulk import applies one topic to all submitted links.
- Collection starts at activation time and does not backfill old history.
- Shared reply text is preserved exactly and applies to the next outbound message without STOP/START.
- Rate limits remain at most 19 messages/minute and at least 2 seconds between outgoing messages.
- Backups run daily at 03:00 local time and retain 14 daily plus 3 monthly verified encrypted archives.
- Preserve the existing yellow Telegram logo, application icon, Lucide icons, Russian primary copy, and matching English translations.
- Keyword and analytics table rows are 25 CSS px at 100% scale.
- Verify `1920x1080` and `2880x1864`; zoom steps are 80, 90, 100, 110, 125, and 150 percent.

---

### Task 1: Embedded SQLite Foundation

**Files:**
- Create: `internal/repository/sqlite/db.go`
- Create: `internal/repository/sqlite/db_test.go`
- Create: `internal/repository/sqlite/migrations/embed.go`
- Create: `internal/repository/sqlite/migrations/000001_init.sql`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**
- Produces: `sqlite.Open(ctx context.Context, path string) (*sql.DB, error)`
- Produces: `sqlite.Migrate(ctx context.Context, db *sql.DB) error`
- Produces schema version `1` with all tables and indexes from the approved spec.

- [ ] **Step 1: Write the failing migration test**

```go
func TestOpenMigratesCompleteSchema(t *testing.T) {
    ctx := context.Background()
    db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "app.sqlite"))
    require.NoError(t, err)
    t.Cleanup(func() { require.NoError(t, db.Close()) })

    for _, table := range []string{
        "accounts", "outbound_channels", "scout_chats",
        "account_channel_memberships", "scout_messages", "imports",
        "analysis_profiles", "analysis_runs", "keyword_candidates",
        "moderation_decisions", "app_settings", "backup_history",
    } {
        var count int
        err := db.QueryRowContext(ctx,
            `SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table,
        ).Scan(&count)
        require.NoError(t, err)
        require.Equal(t, 1, count, table)
    }
}
```

- [ ] **Step 2: Run the test and verify RED**

Run: `/usr/local/go/bin/go test ./internal/repository/sqlite -run TestOpenMigratesCompleteSchema -v`
Expected: FAIL because `internal/repository/sqlite` does not exist.

- [ ] **Step 3: Add the migration and database opener**

Use `modernc.org/sqlite@v1.53.0` and `github.com/stretchr/testify@v1.11.1`. `Open` must create the parent directory with `0700`, open with `mode=rwc`, set one writer plus bounded readers, and execute:

```go
pragmas := []string{
    `PRAGMA journal_mode=WAL`,
    `PRAGMA foreign_keys=ON`,
    `PRAGMA busy_timeout=5000`,
    `PRAGMA secure_delete=ON`,
    `PRAGMA auto_vacuum=INCREMENTAL`,
}
```

The migration must include these critical constraints:

```sql
CREATE TABLE accounts (
  id TEXT PRIMARY KEY,
  phone_masked TEXT NOT NULL,
  display_name TEXT NOT NULL DEFAULT '',
  role TEXT NOT NULL DEFAULT 'spammer' CHECK(role IN ('spammer','scout_analyst')),
  status TEXT NOT NULL DEFAULT 'paused',
  session_path TEXT NOT NULL,
  proxy_profile_id TEXT,
  public_replies_sent INTEGER NOT NULL DEFAULT 0,
  private_messages_sent INTEGER NOT NULL DEFAULT 0,
  last_activity_at TEXT,
  last_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
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
  source_kind TEXT NOT NULL DEFAULT 'telegram',
  UNIQUE(telegram_chat_id, telegram_message_id)
);
CREATE INDEX idx_scout_messages_retention ON scout_messages(message_at);
CREATE INDEX idx_scout_messages_chat_time ON scout_messages(telegram_chat_id, message_at);
```

The remaining migration contract is exact:

```text
outbound_channels(id PK, telegram_chat_id UNIQUE, title, link UNIQUE, topic,
  status, active, sent_count, last_activity_at, last_error, created_at, updated_at)
scout_chats(id PK, telegram_chat_id UNIQUE, title, link UNIQUE, topic,
  status, active, message_count, last_activity_at, last_error, created_at, updated_at)
account_channel_memberships(account_id, catalog, channel_id, is_member,
  status, last_check_at, last_error, PK(account_id,catalog,channel_id))
imports(id PK, file_name, file_path, sha256 UNIQUE, rights_confirmed,
  imported_at, status, model_name, model_sha256, tokenizer_sha256, last_error)
analysis_profiles(id PK, name UNIQUE, description, positive_examples_json,
  exclusions_json, rule_groups_json, enabled, created_at, updated_at)
analysis_runs(id PK, source_scope, profile_id, topic, status, progress,
  input_count, candidate_count, model_metadata_json, started_at, completed_at, error)
keyword_candidates(id PK, run_id, normalized_value, display_value, kind,
  frequency, source_diversity, score, source, moderation_state, created_at,
  UNIQUE(run_id,normalized_value,kind,source))
moderation_decisions(normalized_value, kind, state, decided_at,
  PK(normalized_value,kind))
app_settings(key PK, value_json, revision, updated_at)
backup_history(id PK, archive_path UNIQUE, kind, size_bytes, sha256,
  status, created_at, verified_at, error)
```

Add foreign keys to accounts/catalogs/runs where named IDs exist, `CHECK` constraints for every enum state, and timestamp indexes used by retention, run history, and backup rotation. Store timestamps as RFC3339Nano UTC strings.

- [ ] **Step 4: Verify GREEN and migration idempotence**

Run twice: `/usr/local/go/bin/go test ./internal/repository/sqlite -v`
Expected: PASS both times, including a second `Migrate` call against the same database.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/repository/sqlite
git commit -m "feat: add embedded sqlite foundation"
```

---

### Task 2: Domain Models, Repository Ports, and BoltDB Import

**Files:**
- Create: `internal/domain/analytics.go`
- Create: `internal/domain/backup.go`
- Modify: `internal/domain/account.go`
- Modify: `internal/domain/channel.go`
- Modify: `internal/domain/ports.go`
- Create: `internal/repository/sqlite/catalog_store.go`
- Create: `internal/repository/sqlite/catalog_store_test.go`
- Create: `internal/repository/sqlite/legacy_import.go`
- Create: `internal/repository/sqlite/legacy_import_test.go`

**Interfaces:**
- Produces: `type AccountRole string` with `AccountRoleSpammer` and `AccountRoleScoutAnalyst`.
- Produces: `CatalogRepository`, `ScoutMessageRepository`, `AnalyticsRepository`, `BackupRepository` domain ports.
- Produces: `LegacyImporter.Import(ctx, boltPath) (ImportSummary, error)`.

- [ ] **Step 1: Write failing repository and compatibility tests**

Test these invariants with a temporary SQLite database and a temporary legacy BoltDB fixture:

```go
func TestLegacyImportPreservesReplyAndDMNilSemantics(t *testing.T) {
    legacy := createLegacyBoltFixture(t, domain.KeywordSettings{
        Keywords: []string{"слил", "тест1"},
        SharedReply: "  ответ как введён  ",
        DirectMessageKeywords: nil,
    })
    summary, err := importer.Import(context.Background(), legacy)
    require.NoError(t, err)
    require.Equal(t, 2, summary.Keywords)
    got, err := settings.Load(context.Background())
    require.NoError(t, err)
    require.Equal(t, "  ответ как введён  ", got.SharedReply)
    require.Nil(t, got.DirectMessageKeywords)
}
```

Also test that legacy channels become outbound channels with topic `Без тематики` and existing accounts default to `spammer`.

- [ ] **Step 2: Run tests and verify RED**

Run: `/usr/local/go/bin/go test ./internal/repository/sqlite -run 'TestLegacy|TestCatalog' -v`
Expected: FAIL because repository ports and importer are missing.

- [ ] **Step 3: Add exact domain types and ports**

```go
type AccountRole string

const (
    AccountRoleSpammer AccountRole = "spammer"
    AccountRoleScoutAnalyst AccountRole = "scout_analyst"
)

type SourceCatalog string
const (
    SourceCatalogOutbound SourceCatalog = "outbound"
    SourceCatalogScout SourceCatalog = "scout"
)

type ScoutMessage struct {
    ChatTelegramID string
    MessageID int64
    Text string
    MessageAt time.Time
    ReceivedAt time.Time
    EditedAt *time.Time
}
```

Repository methods must accept `context.Context` and use transactions for multi-row imports. The legacy importer records a durable import marker keyed by the BoltDB SHA-256 so a restart cannot duplicate data.

- [ ] **Step 4: Preserve shared reply exactly**

Remove `strings.TrimSpace` and default substitution from both load and save paths for `SharedReply`. Validation belongs in the outbound use case, not persistence.

- [ ] **Step 5: Run full persistence tests**

Run: `/usr/local/go/bin/go test ./internal/repository/... ./internal/domain/...`
Expected: PASS; existing DM `nil` versus empty tests remain green.

- [ ] **Step 6: Commit**

```bash
git add internal/domain internal/repository/sqlite internal/repository/localdb
git commit -m "feat: add analytics domain and legacy import"
```

---

### Task 3: Versioned Hot Runtime Configuration

**Files:**
- Create: `internal/usecase/runtimeconfig/store.go`
- Create: `internal/usecase/runtimeconfig/store_test.go`
- Modify: `internal/transport/wails/bindings.go`
- Modify: `internal/transport/wails/bindings_test.go`

**Interfaces:**
- Produces: `runtimeconfig.Snapshot`, `Store.Current() Snapshot`, `Store.Publish(next Snapshot) uint64`, and `Store.Subscribe(after uint64) <-chan Snapshot`.
- `Snapshot` contains roles, catalog assignments, keywords, exact shared reply, rate limits, direct-message settings, and proxy assignments.

- [ ] **Step 1: Write concurrency and hot-reply tests**

```go
func TestPublishReplacesImmutableSnapshot(t *testing.T) {
    store := runtimeconfig.NewStore(runtimeconfig.Snapshot{SharedReply: "old"})
    next := store.Current()
    next.SharedReply = "  новый ответ\nс новой строкой  "
    rev := store.Publish(next)
    require.Equal(t, uint64(2), rev)
    require.Equal(t, "  новый ответ\nс новой строкой  ", store.Current().SharedReply)
}
```

Add a `-race` test with 32 readers and 100 publishes. Mutating maps or slices returned by `Current` must not change the stored snapshot.

- [ ] **Step 2: Run tests and verify RED**

Run: `/usr/local/go/bin/go test -race ./internal/usecase/runtimeconfig -v`
Expected: FAIL because the package is missing.

- [ ] **Step 3: Implement atomic snapshot publication**

Use `atomic.Pointer[Snapshot]` for reads, a mutex for revision publication/subscriber registration, and deep clone helpers for maps/slices. Subscribers receive the newest revision through a size-one channel; publication must replace stale pending values instead of blocking.

- [ ] **Step 4: Wire settings saves to Publish**

`SaveKeywordSettings`, `SaveAppSettings`, account role changes, topic changes, and catalog changes must persist in SQLite first and publish only after the transaction commits. Return the applied revision in the DTO.

- [ ] **Step 5: Verify**

Run: `/usr/local/go/bin/go test -race ./internal/usecase/runtimeconfig ./internal/transport/wails`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/usecase/runtimeconfig internal/transport/wails
git commit -m "feat: add hot runtime configuration"
```

---

### Task 4: Account Roles, Topics, and Separate Catalog UI

**Files:**
- Create: `internal/usecase/accounts.go`
- Create: `internal/usecase/accounts_test.go`
- Create: `internal/usecase/catalogs.go`
- Create: `internal/usecase/catalogs_test.go`
- Modify: `internal/transport/wails/bindings.go`
- Modify: `internal/transport/wails/bindings_test.go`
- Create: `frontend/src/accounts.ts`
- Create: `frontend/src/accounts.test.ts`
- Create: `frontend/src/catalogs.ts`
- Create: `frontend/src/catalogs.test.ts`
- Modify: `frontend/src/App.tsx`
- Modify: `frontend/src/i18n.ts`
- Regenerate: `frontend/wailsjs/go/wails/Bindings.d.ts`
- Regenerate: `frontend/wailsjs/go/wails/Bindings.js`

**Interfaces:**
- Produces Wails methods: `GetAccounts`, `SetAccountRole`, `GetCatalog`, `AddCatalogLinks`, `SetChannelTopic`, `SetChannelTopics`, `ToggleCatalogEntry`.
- `AddCatalogLinks(input, catalog, topic)` rejects blank topics and applies one normalized topic to every unique link.
- `SetChannelTopics(catalog, channelIDs, topic)` updates one or many selected rows atomically and rejects a blank topic.

- [ ] **Step 1: Write failing use-case tests**

```go
func TestBulkScoutImportRequiresAndAppliesOneTopic(t *testing.T) {
    _, err := service.AddLinks(ctx, domain.SourceCatalogScout,
        []string{"https://t.me/one", "https://t.me/two"}, "")
    require.ErrorIs(t, err, usecase.ErrTopicRequired)

    got, err := service.AddLinks(ctx, domain.SourceCatalogScout,
        []string{"https://t.me/one", "https://t.me/two"}, "Личные финансы")
    require.NoError(t, err)
    require.Len(t, got, 2)
    require.Equal(t, "Личные финансы", got[0].Topic)
    require.Equal(t, "Личные финансы", got[1].Topic)
}
```

Test that a scout account is excluded from outbound membership work and a spammer account is excluded from scout membership work.

Add a bulk-edit test that selects two existing scout rows, applies one new topic, and verifies that both rows change in one transaction while an unknown ID leaves every row unchanged.

- [ ] **Step 2: Verify RED**

Run: `/usr/local/go/bin/go test ./internal/usecase -run 'TestBulkScout|TestRoleCatalog' -v`
Expected: FAIL with missing services.

- [ ] **Step 3: Implement use cases and Wails DTOs**

Role transitions publish runtime snapshots. Catalog rows expose title, link, topic, status, message count, last activity, and active state. Existing `parseChannelLinks` remains the canonical link parser.

- [ ] **Step 4: Write frontend reducers before components**

```ts
export function applyBulkTopic(
  links: string[], topic: string
): { links: string[]; topic: string; error?: "topic_required" } {
  const cleaned = topic.trim();
  if (!cleaned) return { links: [], topic: "", error: "topic_required" };
  return { links: uniqueTelegramLinks(links), topic: cleaned };
}
```

Run: `cd frontend && npm test -- src/accounts.test.ts src/catalogs.test.ts`
Expected before implementation: FAIL; after implementation: PASS.

- [ ] **Step 5: Build the UI**

Use an icon-backed segmented control for roles. Add channel tabs `Каналы ботов-спамеров`, `Каналы разведчиков`, and `Тематики`. The bulk form contains links plus one required topic input with existing-topic autocomplete. Checked table rows expose one compact `Изменить тематику` action that calls `SetChannelTopics` once for the selection.

- [ ] **Step 6: Regenerate bindings, verify, and commit**

Run:

```bash
/usr/local/go/bin/go test ./internal/usecase ./internal/transport/wails
PATH=/usr/local/go/bin:/home/codex/go/bin:/usr/local/bin:/usr/bin:/bin \
  /home/codex/go/bin/wails build -clean -tags desktop,webkit2_41
/home/codex/projects/telegram-companion/data/acceptance-venv/bin/python scripts/clean_wailsjs.py
cd frontend && npm test && npm run build
```

```bash
git add internal/usecase internal/transport/wails frontend/src frontend/wailsjs
git commit -m "feat: add account roles and topic catalogs"
```

---

### Task 5: gotd Session Importer and Account Validation

**Files:**
- Create: `internal/telegram/gotd/session_importer.go`
- Create: `internal/telegram/gotd/session_importer_test.go`
- Create: `internal/telegram/gotd/client_factory.go`
- Create: `internal/telegram/gotd/client_factory_test.go`
- Create: `internal/service/secrets/keyring.go`
- Create: `internal/service/secrets/keyring_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**
- Produces: `SessionImporter.ImportTData(ctx, sourceDir, destination string) ([]ImportedSession, error)`.
- Produces: `ClientFactory.New(account domain.Account) (TelegramClient, error)`.
- Produces: `SecretStore.GetOrCreate(ctx, name string, bytes int) ([]byte, error)`.

- [ ] **Step 1: Write source-mutation and identity tests**

Use a sanitized tdata fixture created from a dedicated test account, not the three production sessions.

```go
func TestImportTDataNeverMutatesSource(t *testing.T) {
    source := copyFixture(t, "testdata/tdata")
    before := treeHashes(t, source)
    sessions, err := importer.ImportTData(ctx, source, t.TempDir())
    require.NoError(t, err)
    require.NotEmpty(t, sessions)
    require.Equal(t, before, treeHashes(t, source))
}
```

- [ ] **Step 2: Verify RED**

Run: `/usr/local/go/bin/go test ./internal/telegram/gotd -run TestImportTData -v`
Expected: FAIL because importer is missing.

- [ ] **Step 3: Add pinned dependencies and importer**

Use `github.com/gotd/td@v0.160.0` and `github.com/zalando/go-keyring@v0.2.8`. Read copied tdata with `tdesktop.Read`, convert with `session.TDesktopSession`, and save through `session.Loader` into an owner-only file. Never log auth keys or session bytes.

- [ ] **Step 4: Add validation mode**

Connect the gotd client, call `users.getFullUser` for self identity, compare expected masked phone/account label, then disconnect. Persist `validated_at` only after success.

- [ ] **Step 5: Run fixture tests and a manual copy-only dry run**

Run: `/usr/local/go/bin/go test -race ./internal/telegram/gotd ./internal/service/secrets`
Expected: PASS.

Manual acceptance uses copied paths under `data/gotd-import-staging`; source tdata hashes must remain unchanged.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/telegram/gotd internal/service/secrets
git commit -m "feat: import tdata sessions into gotd"
```

---

### Task 6: gotd Client Manager and Strict Role Capabilities

**Files:**
- Create: `internal/telegram/gotd/manager.go`
- Create: `internal/telegram/gotd/manager_test.go`
- Create: `internal/telegram/gotd/capabilities.go`
- Create: `internal/telegram/gotd/capabilities_test.go`
- Create: `internal/telegram/gotd/catalog_gateway.go`
- Create: `internal/telegram/gotd/catalog_gateway_test.go`

**Interfaces:**
- Produces: `Manager.Run(ctx) error`, `Manager.Apply(snapshot runtimeconfig.Snapshot) error`.
- Produces separate ports: `UpdateSource`, `CatalogJoiner`, and `OutboundSender`.
- Scout client wrappers do not implement `OutboundSender`.

- [ ] **Step 1: Write compile-time and behavioral no-send tests**

```go
func TestScoutCapabilityHasNoSender(t *testing.T) {
    scout := NewScoutCapability(fakeClient{})
    _, implements := any(scout).(domain.OutboundSender)
    require.False(t, implements)
}
```

Also instrument fake RPC calls and assert zero send-method invocations after role transitions and scout updates.

Add `TestScoutActivationDoesNotRequestHistory`: activation may register the live update handler and join the configured chat, but the fake RPC log must contain no `messages.getHistory`, `messages.search`, or iterator/backfill call.

- [ ] **Step 2: Verify RED**

Run: `/usr/local/go/bin/go test ./internal/telegram/gotd -run 'TestScoutCapability|TestManager' -v`
Expected: FAIL.

- [ ] **Step 3: Implement one supervised client per account**

Use one goroutine group per account, account-scoped backoff, context cancellation, and status callbacks. `Apply` computes a diff between revisions and changes handlers/capabilities without reconnecting unaffected clients.

- [ ] **Step 4: Implement catalog resolution and joining**

Normalize invite/public links, resolve title and Telegram ID, check membership, join only with accounts eligible for that catalog, and persist per-account membership state. Surface FloodWait duration without rotating accounts to evade limits.

- [ ] **Step 5: Verify**

Run: `/usr/local/go/bin/go test -race ./internal/telegram/gotd/...`
Expected: PASS with no goroutine leak in repeated Apply/Close tests.

- [ ] **Step 6: Commit**

```bash
git add internal/telegram/gotd
git commit -m "feat: supervise role-scoped gotd clients"
```

---

### Task 7: Anonymized Scout Ingestion, Retention, and Metrics

**Files:**
- Create: `internal/usecase/scouting/collector.go`
- Create: `internal/usecase/scouting/collector_test.go`
- Create: `internal/usecase/scouting/retention.go`
- Create: `internal/usecase/scouting/retention_test.go`
- Create: `internal/repository/sqlite/message_store.go`
- Create: `internal/repository/sqlite/message_store_test.go`
- Create: `internal/service/crypto/message_cipher.go`
- Create: `internal/service/crypto/message_cipher_test.go`
- Modify: `internal/transport/wails/bindings.go`

**Interfaces:**
- Produces: `Collector.Ingest(ctx, IncomingUpdate) error`.
- Produces: `Retention.RunOnce(ctx, before time.Time) (PruneResult, error)`.
- Produces: `GetStorageMetrics() StorageMetricsDTO`.

- [ ] **Step 1: Write privacy and deduplication tests**

```go
func TestCollectorPersistsNoAuthorFieldsAndDeduplicates(t *testing.T) {
    update := IncomingUpdate{
        ChatID: "1001", MessageID: 77, Text: "пример текста",
        SenderID: "must-not-persist", SenderUsername: "private",
        MessageAt: clock.Now(),
    }
    require.NoError(t, collector.Ingest(ctx, update))
    require.NoError(t, collector.Ingest(ctx, update))
    require.Equal(t, int64(1), store.Count(ctx))
    raw := dumpSchemaAndRows(t, db)
    require.NotContains(t, raw, "must-not-persist")
    require.NotContains(t, raw, "private")
    require.NotContains(t, raw, "пример текста")
}
```

- [ ] **Step 2: Verify RED**

Run: `/usr/local/go/bin/go test ./internal/usecase/scouting ./internal/repository/sqlite ./internal/service/crypto -v`
Expected: FAIL.

- [ ] **Step 3: Implement encryption and idempotent writes**

Use XChaCha20-Poly1305 from `golang.org/x/crypto/chacha20poly1305`. Bind chat ID, message ID, and message timestamp as additional authenticated data. The repository receives a privacy-safe command with no author fields.

- [ ] **Step 4: Implement edit/delete and retention**

Edits replace ciphertext and nonce transactionally. Deletions remove the row. Hourly scheduling calls `RunOnce(now.Add(-30*24*time.Hour))`; tests use a fake clock. Remove expired message-level derived rows in the same transaction.

- [ ] **Step 5: Implement 60-second storage metrics**

Return row count, physical database bytes, oldest/newest timestamps, paused-for-low-disk state, and last refresh. The frontend polls every 60 seconds; backend queries remain independently callable.

- [ ] **Step 6: Verify and commit**

Run: `/usr/local/go/bin/go test -race ./internal/usecase/scouting ./internal/repository/sqlite ./internal/service/crypto ./internal/transport/wails`

```bash
git add internal/usecase/scouting internal/repository/sqlite internal/service/crypto internal/transport/wails
git commit -m "feat: collect anonymized scout messages"
```

---

### Task 8: Deterministic Keyword Analytics, Moderation, and Export

**Files:**
- Create: `internal/analytics/text/normalize.go`
- Create: `internal/analytics/text/normalize_test.go`
- Create: `internal/analytics/text/ngram.go`
- Create: `internal/analytics/text/ngram_test.go`
- Create: `internal/analytics/ranking.go`
- Create: `internal/analytics/ranking_test.go`
- Create: `internal/analytics/profiles.go`
- Create: `internal/analytics/profiles_test.go`
- Create: `internal/usecase/analytics/service.go`
- Create: `internal/usecase/analytics/service_test.go`
- Create: `internal/service/export/keywords.go`
- Create: `internal/service/export/keywords_test.go`
- Create: `internal/service/export/opener.go`
- Create: `internal/service/export/opener_test.go`

**Interfaces:**
- Produces: `Analyzer.Analyze(ctx, AnalysisRequest) (AnalysisResult, error)` where `AnalysisRequest` contains one source scope, one profile ID, and zero or more source topics.
- Produces: general phrase top 100, general word top 30, and profile top 100.
- Produces: `CompareTopics(ctx, TopicComparisonRequest) ([]TopicSummary, error)` with count, distinct candidate count, top phrases, and relative share for every selected topic.
- Produces: `Moderate`, `ResetModeration`, `AddCandidatesToKeywords`, `ExportKeywords`.

- [ ] **Step 1: Write golden Russian text tests**

Use fixtures that cover `ё/е`, negation, punctuation, links, username mentions, repeated forwards, short phrases, and the default money-shortage profile.

```go
func TestNGramsKeepMeaningfulStopwordsInsidePhrase(t *testing.T) {
    got := Generate("Мне не хватает денег на покупку", 1, 4)
    require.Contains(t, got, "не хватает денег")
    require.Contains(t, got, "не хватает денег на")
}
```

- [ ] **Step 2: Verify RED**

Run: `/usr/local/go/bin/go test ./internal/analytics/... -v`
Expected: FAIL.

- [ ] **Step 3: Implement deterministic pipeline**

Normalize, sentence-split, tokenize, stem/lemmatize through an internal interface, generate 1-4 grams, retain the most frequent surface form, cluster exact and SimHash near-duplicates, then rank with named score components. Do not import or call an ML library in this package.

Filtering by one topic constrains the source rows before aggregation. Topic comparison executes the same deterministic pipeline per selected topic and calculates relative shares from aggregate counts only; it never exposes message text or author-level data.

- [ ] **Step 4: Implement analysis-run transactions**

Write candidates under a new run ID. Mark a run complete and switch `current_successful_run_id` only after every candidate and aggregate commit succeeds. Cancellation marks the run `partial` and keeps the previous successful run current.

- [ ] **Step 5: Implement moderation and atomic UTF-8 export**

Rejected normalized keys remain suppressed across runs. `AddCandidatesToKeywords` adds only visible `new` and `accepted` rows and deduplicates case-insensitively. Export writes a temporary file, fsyncs it, and atomically renames it to `data/exports/keywords.txt`.

Add an `OSFileOpener` adapter that validates the requested path is exactly the configured export file, then invokes `/usr/bin/open` on macOS or `/usr/bin/xdg-open` on Linux through `exec.CommandContext`. Tests inject a fake command runner and assert that arbitrary paths are rejected.

- [ ] **Step 6: Verify and commit**

Run: `/usr/local/go/bin/go test -race ./internal/analytics/... ./internal/usecase/analytics ./internal/service/export`

```bash
git add internal/analytics internal/usecase/analytics internal/service/export
git commit -m "feat: add deterministic keyword analytics"
```

---

### Task 9: Rights-Confirmed AI Imports

**Files:**
- Create: `internal/domain/imports.go`
- Create: `internal/usecase/imports/service.go`
- Create: `internal/usecase/imports/service_test.go`
- Create: `internal/analytics/ai/provider.go`
- Create: `internal/analytics/ai/provider_test.go`
- Create: `internal/analytics/ai/hugot_provider.go`
- Create: `internal/analytics/ai/hugot_provider_stub.go`
- Create: `internal/repository/sqlite/import_store.go`
- Create: `internal/repository/sqlite/import_store_test.go`
- Create: `models/manifest.json`
- Create: `scripts/fetch_model.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**
- Produces: `ImportService.Import(ctx, ImportRequest) (domain.Import, error)`.
- Produces: `EmbeddingProvider.Embed(ctx, []string) ([][]float32, ModelMetadata, error)`.
- `ImportRequest` requires `RightsConfirmed bool`, local path, SHA-256, and selected profile.

- [ ] **Step 1: Write the permission boundary test**

```go
func TestImportRejectsAIWithoutRightsConfirmation(t *testing.T) {
    _, err := service.Import(ctx, ImportRequest{
        Path: fixturePath, RightsConfirmed: false, UseAI: true,
    })
    require.ErrorIs(t, err, imports.ErrRightsConfirmationRequired)
    require.Zero(t, fakeProvider.Calls())
}
```

Add a test that Telegram source kind cannot be submitted to `EmbeddingProvider`, even if the caller constructs the request manually.

- [ ] **Step 2: Verify RED**

Run: `/usr/local/go/bin/go test ./internal/usecase/imports ./internal/analytics/ai -v`
Expected: FAIL.

- [ ] **Step 3: Add Hugot and model metadata**

Pin `github.com/knights-analytics/hugot@v0.7.5`. Use `intfloat/multilingual-e5-small` at immutable Hugging Face revision `614241f622f53c4eeff9890bdc4f31cfecc418b3`. The default build calls `hugot.NewGoSession(ctx)` for cross-platform correctness; an `ORT` build tag calls `hugot.NewORTSession(ctx, ...)` only after both Ubuntu and macOS artifact tests pass.

Commit this exact `models/manifest.json`; the hashes are the immutable Hugging Face LFS SHA-256 values:

```json
{
  "schema_version": 1,
  "repository": "intfloat/multilingual-e5-small",
  "revision": "614241f622f53c4eeff9890bdc4f31cfecc418b3",
  "destination": "models/multilingual-e5-small",
  "files": [
    {
      "source": "onnx/model.onnx",
      "destination": "model.onnx",
      "size": 470268510,
      "sha256": "ca456c06b3a9505ddfd9131408916dd79290368331e7d76bb621f1cba6bc8665"
    },
    {
      "source": "tokenizer.json",
      "destination": "tokenizer.json",
      "size": 17082730,
      "sha256": "0b44a9d7b51c3c62626640cda0e2c2f70fdacdc25bbbd68038369d14ebdf4c39"
    }
  ]
}
```

`scripts/fetch_model.go` downloads only files named in the manifest from `https://huggingface.co/{repository}/resolve/{revision}/{source}`, writes through owner-only temporary files, verifies byte count and SHA-256, and atomically renames each verified file. Redirects must remain HTTPS and may target only `huggingface.co`, subdomains of `huggingface.co`, or subdomains of `hf.co` such as the Hugging Face Xet/LFS hosts. Model binaries stay out of Git; the manifest and downloader are committed.

- [ ] **Step 4: Implement import parsing and semantic ranking**

Support UTF-8 `.txt`, `.csv`, and `.json`. Stream records with bounded memory, hash the source file, store rights confirmation, embed profile examples and imported records, calculate cosine similarity, and combine semantic score with deterministic phrase quality/frequency. Never store embeddings for Telegram rows.

- [ ] **Step 5: Verify model reproducibility**

Golden tests assert identical ordering for a fixed model/tokenizer hash and fixture. Record model name, model SHA-256, tokenizer SHA-256, thresholds, and app version in `analysis_runs`.

- [ ] **Step 6: Verify and commit**

Run: `/usr/local/go/bin/go test -race ./internal/usecase/imports ./internal/analytics/ai ./internal/repository/sqlite`

```bash
git add go.mod go.sum internal/domain/imports.go internal/usecase/imports internal/analytics/ai internal/repository/sqlite/import_store.go internal/repository/sqlite/import_store_test.go models/manifest.json scripts/fetch_model.go
git commit -m "feat: add rights-confirmed local ai imports"
```

---

### Task 10: Verified Daily Backups and Restore

**Files:**
- Create: `internal/service/backup/manager.go`
- Create: `internal/service/backup/manager_test.go`
- Create: `internal/service/backup/archive.go`
- Create: `internal/service/backup/archive_test.go`
- Create: `internal/service/backup/recovery_key.go`
- Create: `internal/service/backup/recovery_key_test.go`
- Create: `internal/usecase/backups.go`
- Create: `internal/usecase/backups_test.go`
- Modify: `internal/transport/wails/bindings.go`
- Modify: `internal/transport/wails/bindings_test.go`
- Create: `docs/operations/restore-backup.md`

**Interfaces:**
- Produces: `BackupManager.Create(ctx, BackupKind) (domain.BackupRecord, error)`.
- Produces: `BackupManager.Restore(ctx, archive, destination string) error`.
- Produces: `BackupScheduler.Next(now time.Time) time.Time` for 03:00 local time.

- [ ] **Step 1: Write backup/restore and retention tests**

```go
func TestVerifiedBackupRestoresDatabaseAndRotatesAfterSuccess(t *testing.T) {
    record, err := manager.Create(ctx, domain.BackupDaily)
    require.NoError(t, err)
    require.Equal(t, domain.BackupVerified, record.Status)
    restored := filepath.Join(t.TempDir(), "restore")
    require.NoError(t, manager.Restore(ctx, record.Path, restored))
    require.Equal(t, sourceLogicalDump(t), restoredLogicalDump(t, restored))
}
```

Add deterministic clock tests for 14 daily and 3 monthly retention. Failed verification must leave previous archives untouched.

- [ ] **Step 2: Verify RED**

Run: `/usr/local/go/bin/go test ./internal/service/backup ./internal/usecase -run Backup -v`
Expected: FAIL.

- [ ] **Step 3: Implement consistent encrypted snapshots**

Use SQLite online backup for the application database. Acquire the gotd session-store read barrier, copy session/peer files to a temporary snapshot, release the barrier, then archive. Encrypt with XChaCha20-Poly1305 streaming chunks and a key from `SecretStore`; write a manifest with schema version, sizes, and SHA-256 hashes.

- [ ] **Step 4: Verify before rotation**

Decrypt into a temporary restore directory, validate every manifest hash, open the copied SQLite database, run `PRAGMA integrity_check`, and compare schema compatibility. Only then mark verified and rotate old archives.

- [ ] **Step 5: Add scheduling, pre-migration backup, DTO, and restore guide**

Schedule daily at 03:00 local time, produce monthly copies on the first successful day of each month, expose status in Wails, and require a verified pre-migration backup before SQLite schema upgrades. Add an explicit recovery-key export method that returns the key only after a user command, never logs it, and writes it only to a user-selected owner-only path.

- [ ] **Step 6: Verify and commit**

Run: `/usr/local/go/bin/go test -race ./internal/service/backup ./internal/usecase ./internal/transport/wails`

```bash
git add internal/service/backup internal/usecase/backups.go internal/usecase/backups_test.go internal/transport/wails docs/operations/restore-backup.md
git commit -m "feat: add verified daily backups"
```

---

### Task 11: Analytics UI, Adaptive Layout, and Zoom Shortcuts

**Files:**
- Create: `frontend/src/analytics.ts`
- Create: `frontend/src/analytics.test.ts`
- Create: `frontend/src/AnalyticsView.tsx`
- Create: `frontend/src/AnalyticsView.test.tsx`
- Create: `frontend/src/uiScale.ts`
- Create: `frontend/src/uiScale.test.ts`
- Modify: `frontend/src/App.tsx`
- Modify: `frontend/src/KeywordTable.tsx`
- Modify: `frontend/src/styles.css`
- Modify: `frontend/src/i18n.ts`
- Modify: `internal/transport/wails/bindings.go`
- Regenerate: `frontend/wailsjs/go/wails/Bindings.d.ts`
- Regenerate: `frontend/wailsjs/go/wails/Bindings.js`

**Interfaces:**
- Produces Wails methods for analysis start/cancel/status/results, moderation, add-all, copy data, imports, storage metrics, backup status, and file opening.
- Produces: `nextScale(current, direction)`, `autoScale(viewport, dpi)`, `installScaleShortcuts`.

- [ ] **Step 1: Write scale and analytics reducer tests**

```ts
const steps = [80, 90, 100, 110, 125, 150] as const;

test("scale shortcuts clamp and reset", () => {
  expect(nextScale(100, 1)).toBe(110);
  expect(nextScale(150, 1)).toBe(150);
  expect(nextScale(80, -1)).toBe(80);
  expect(autoScale({ width: 1920, height: 1080 }, 1)).toBe(100);
  expect(autoScale({ width: 2880, height: 1864 }, 1)).toBe(125);
});
```

Reducer tests cover filters, 60-second metrics refresh scheduling, moderation, hidden rejected rows, and add-all deduplication.

- [ ] **Step 2: Verify RED**

Run: `cd frontend && npm test -- src/analytics.test.ts src/uiScale.test.ts`
Expected: FAIL.

- [ ] **Step 3: Implement responsive scale behavior**

Intercept `Ctrl++`, `Ctrl+-`, and `Ctrl+0`; prevent the WebView default; publish a persisted scale setting. Apply scale through a root CSS variable and layout-aware zoom class. Responsive breakpoints must cover compact, Full HD, and 2880-wide modes without fixed viewport font scaling.

- [ ] **Step 4: Implement the approved Analytics UI**

Keep the existing yellow logo and Lucide icons. Add navigation `Аналитика keyword`, metrics band, the approved tabs `Тематические`, `Общие слова`, `Импорты AI`, profile/topic filters, compact table, copy/moderate actions, add all, and open file. In `Тематические`, the topic control supports one topic or a compact comparison mode for selected topics without adding a fourth top-level tab. Separate `Собирает` and message count into distinct spaced columns.

- [ ] **Step 5: Enforce 25 px table rows**

At 100% scale both keyword tables use `min-height: 25px` with stable action buttons no larger than 21 CSS px. At increased zoom, the whole UI scales proportionally while preserving column visibility.

- [ ] **Step 6: Verify translations, tests, and production build**

Run:

```bash
PATH=/usr/local/go/bin:/home/codex/go/bin:/usr/local/bin:/usr/bin:/bin \
  /home/codex/go/bin/wails build -clean -tags desktop,webkit2_41
/home/codex/projects/telegram-companion/data/acceptance-venv/bin/python scripts/clean_wailsjs.py
cd frontend && npm test && npm run build
```

Expected: all tests and TypeScript build pass; Russian and English key sets match exactly.

- [ ] **Step 7: Commit**

```bash
git add frontend/src frontend/wailsjs internal/transport/wails
git commit -m "feat: add adaptive keyword analytics ui"
```

---

### Task 12: gotd Outbound Cutover, Cleanup, and Desktop Acceptance

**Files:**
- Create: `internal/telegram/gotd/outbound.go`
- Create: `internal/telegram/gotd/outbound_test.go`
- Modify: `internal/usecase/selector.go`
- Modify: `internal/usecase/scheduler.go`
- Modify: `main.go`
- Modify: `main_test.go`
- Modify: `Makefile`
- Modify: `README.md`
- Create: `.github/workflows/ci.yml`
- Delete after acceptance: `live_runner.go`
- Delete after acceptance: `live_runner_test.go`
- Delete after acceptance: `scripts/telegram_live_listener.py`
- Delete after acceptance: `scripts/test_telegram_live_listener.py`

**Interfaces:**
- Consumes all previous tasks.
- Produces the final single-process desktop runtime and removes the Python production dependency.

- [ ] **Step 1: Write outbound capability and hot-reply tests**

```go
func TestNextOutboundMessageUsesLatestReplyWithoutRestart(t *testing.T) {
    runtime.Publish(snapshotWithReply("старый"))
    runtime.Publish(snapshotWithReply("  новый\nответ  "))
    require.NoError(t, sender.Send(ctx, queuedTrigger))
    require.Equal(t, "  новый\nответ  ", fakeAPI.LastMessage().Text)
}
```

Test 19/minute maximum, 2-second minimum spacing, round-robin across spammer accounts, FloodWait isolation, and zero scout sends.

- [ ] **Step 2: Verify RED then implement gotd outbound adapter**

Run before implementation: `/usr/local/go/bin/go test ./internal/telegram/gotd ./internal/usecase -run 'Outbound|HotReply|Rate' -v`
Expected: FAIL. Implement sender resolution, public replies, direct messages, queue status, and counters through existing use-case ports.

- [ ] **Step 3: Run shadow mode for all three accounts**

For the approved test group, run gotd update handling without sends while Telethon remains authoritative. Verify account identity, update completeness, reconnect recovery, scout/outbound catalog isolation, and zero duplicate persisted messages. Record acceptance output under `data/acceptance/` without message text or secrets.

- [ ] **Step 4: Switch runtime wiring to gotd**

`main.go` opens SQLite, imports legacy settings once, initializes secrets, runtime config, gotd manager, collectors, analyzers, retention, backup scheduler, export service, and Wails bindings. Startup and shutdown use one root context and bounded graceful shutdown.

- [ ] **Step 5: Remove Python runtime only after live acceptance**

Delete the listed Python/runner files and remove acceptance-venv startup assumptions. Keep migration documentation for previous Telethon sessions. Update Makefile targets for `test`, `test-race`, `build-linux`, `build-macos`, `lint`, and model verification.

- [ ] **Step 6: Run the complete automated verification matrix**

```bash
/usr/local/go/bin/go test ./...
/usr/local/go/bin/go test -tags desktop ./...
/usr/local/go/bin/go test -race ./...
cd frontend && npm test && npm run build
cd .. && golangci-lint run ./...
PATH=/usr/local/go/bin:/home/codex/go/bin:/usr/local/bin:/usr/bin:/bin \
  /home/codex/go/bin/wails build -clean -skipbindings -tags desktop,webkit2_41
```

Expected: all commands pass and `build/bin/telegram-companion` exists.

The CI workflow runs Go tests, desktop-tag tests, race tests, frontend tests, lint, model-manifest verification, and Wails builds on `ubuntu-24.04` and `macos-14`. It caches Go/npm modules but never caches sessions, tdata, databases, imported content, model input, or backup archives. Start `.github/workflows/ci.yml` with this explicit matrix and keep OS-specific Wails prerequisites in named steps:

```yaml
name: ci

on:
  pull_request:
  push:
    branches: [main, "feat/**"]

permissions:
  contents: read

jobs:
  verify:
    strategy:
      fail-fast: false
      matrix:
        os: [ubuntu-24.04, macos-14]
    runs-on: ${{ matrix.os }}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.25.7"
          cache: true
      - uses: actions/setup-node@v4
        with:
          node-version: "22"
          cache: npm
          cache-dependency-path: frontend/package-lock.json
      - name: Install Ubuntu desktop dependencies
        if: runner.os == 'Linux'
        run: sudo apt-get update && sudo apt-get install -y libgtk-3-dev libwebkit2gtk-4.1-dev
      - name: Install tools
        run: |
          go install github.com/wailsapp/wails/v2/cmd/wails@v2.13.0
          go install github.com/golangci/golangci-lint/cmd/golangci-lint@v2.11.4
      - name: Install frontend dependencies
        working-directory: frontend
        run: npm ci
      - name: Verify model manifest
        run: go run ./scripts/fetch_model.go -verify-manifest-only
      - name: Test Go
        run: |
          go test ./...
          go test -tags desktop ./...
          go test -race ./...
      - name: Test frontend
        working-directory: frontend
        run: npm test -- --run && npm run build
      - name: Lint
        run: golangci-lint run ./...
      - name: Build Wails
        shell: bash
        run: |
          if [[ "${RUNNER_OS}" == "Linux" ]]; then
            wails build -clean -skipbindings -tags desktop,webkit2_41
          else
            wails build -clean -skipbindings -tags desktop
          fi
```

- [ ] **Step 7: Perform desktop acceptance**

Verify with screenshots and interaction tests:

- `1920x1080` at every zoom step;
- `2880x1864` at every zoom step;
- no overlap, inaccessible columns, or clipped buttons;
- role changes and immediate scout collection;
- no historical backfill;
- deterministic Telegram analysis;
- rights-confirmed AI import analysis;
- copy, moderation, add all, and file opening;
- import file picker, required rights-confirmation checkbox, and persisted import provenance;
- shared reply hot update;
- STOP/START repetition;
- daily backup creation and one restore drill;
- application restart with persisted roles, topics, keywords, reply, scale, and moderation.

Leave automation running and the latest backup verified.

- [ ] **Step 8: Final review and commit**

Run the `superpowers:requesting-code-review` and `superpowers:verification-before-completion` workflows against the complete feature range. Resolve all Critical and Important findings.

```bash
git add .github/workflows/ci.yml Makefile README.md main.go main_test.go internal/telegram/gotd/outbound.go internal/telegram/gotd/outbound_test.go internal/usecase/selector.go internal/usecase/scheduler.go
git add -u live_runner.go live_runner_test.go scripts/telegram_live_listener.py scripts/test_telegram_live_listener.py
git commit -m "feat: cut over desktop runtime to gotd"
```
