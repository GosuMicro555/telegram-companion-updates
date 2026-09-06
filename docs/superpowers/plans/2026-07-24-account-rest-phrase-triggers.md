# Account Rest, Phrase Triggers, and Live Exclusions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Добавить в Telegram Companion 0.8.0 отлёжку аккаунтов после вступления, связанные keyword из нескольких слов и перенос выделенного текста Live-статистики в негативные keyword.

**Architecture:** Отлёжка хранится на уровне членства аккаунт–канал, а публичная готовность аккаунта вычисляется как максимальное активное окончание отлёжки. Сопоставление keyword расширяется с одного токена до набора канонических токенов внутри одного предложения. UI использует отдельный read-only сервис отлёжек и существующий массовый импорт канонических keyword.

**Tech Stack:** Go 1.25+, SQLite migrations, Wails, React, TypeScript, Vitest.

## Global Constraints

- Целевая версия остаётся `0.8.0`; видимые release notes этой доработки не добавлять.
- Длительность отлёжки по умолчанию `36` часов, допустимо `1..720`.
- Отлёжка блокирует только сообщения в группах; чтение, вступления и разрешённые ЛС продолжают работать.
- Составной trigger совпадает, только если все его слова находятся в одном предложении.
- Русские и английские однословные trigger продолжают работать без регрессий.
- Telegram Companion, trigger-ответы и live Telegram tests не запускать.
- Новые внешние зависимости не добавлять.

---

### Task 1: Persistence and Settings for Account Rest

**Files:**
- Create: `internal/repository/sqlite/migrations/000025_account_group_rest.sql`
- Modify: `internal/domain/channel.go`
- Modify: `internal/domain/settings.go`
- Modify: `internal/domain/settings_test.go`
- Modify: `internal/usecase/catalogs.go`
- Modify: `internal/usecase/catalogs_test.go`
- Modify: `internal/repository/sqlite/production_store.go`
- Modify: `internal/repository/sqlite/production_store_test.go`
- Modify: `internal/repository/sqlite/migrations/embed.go`
- Modify: `internal/transport/wails/bindings.go`
- Modify: `internal/transport/wails/bindings_test.go`

**Interfaces:**
- Produces: `domain.AccountGroupRest`, `domain.GroupRestRepository`, membership rest fields, `AppSettings.GroupRestHours`.
- Produces: `CatalogService.SetGroupRestHours(int)`.
- Produces: `ProductionStore.ListAccountGroupRests(ctx)` and `ProductionStore.ActiveGroupRestUntil(ctx, accountIDs, now)`.

- [ ] **Step 1: Add failing settings tests**

Add assertions that zero-valued settings normalize to 36 hours and values outside `1..720` are rejected or clamped through the existing settings validation convention.

- [ ] **Step 2: Run the settings tests and verify RED**

Run:

```bash
go test ./internal/domain -run 'Settings|GroupRest' -count=1
```

Expected: FAIL because `GroupRestHours` does not exist.

- [ ] **Step 3: Add the migration and domain types**

Migration:

```sql
-- +goose Up
ALTER TABLE account_channel_memberships ADD COLUMN rest_started_at TEXT;
ALTER TABLE account_channel_memberships ADD COLUMN rest_until TEXT;
ALTER TABLE account_channel_memberships ADD COLUMN rest_duration_hours INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE account_channel_memberships DROP COLUMN rest_duration_hours;
ALTER TABLE account_channel_memberships DROP COLUMN rest_until;
ALTER TABLE account_channel_memberships DROP COLUMN rest_started_at;
```

Add nullable rest fields to `domain.ChannelMembership` and:

```go
type AccountGroupRest struct {
    AccountID     ID
    AccountTitle  string
    ChannelID     ID
    ChannelTitle  string
    Catalog       SourceCatalog
    StartedAt     time.Time
    Until         time.Time
    DurationHours int
}
```

- [ ] **Step 4: Implement persistence queries**

`ListAccountGroupRests` returns only initialized rest rows (`rest_started_at` and `rest_until` non-null), including expired rows, with account/channel titles and catalog. `ActiveGroupRestUntil` returns the latest `rest_until` later than `now` for each requested account in one query. `CatalogService.joiningMemberships` snapshots the current `GroupRestHours` into every new membership intent; existing intents keep their original value. Wails settings hydration/save exposes `groupRestHours` and updates `CatalogService` on startup and after a successful save, following the existing join-interval flow.

- [ ] **Step 5: Verify GREEN**

Run:

```bash
go test ./internal/domain ./internal/usecase ./internal/repository/sqlite ./internal/transport/wails -run 'Settings|GroupRest|Membership|Catalog' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/domain internal/usecase/catalogs.go internal/usecase/catalogs_test.go internal/repository/sqlite internal/transport/wails/bindings.go internal/transport/wails/bindings_test.go
git commit -m "feat: persist account group rest periods"
```

### Task 2: Start Rest Only After a Confirmed New Join

**Files:**
- Modify: `internal/telegram/gotd/membership.go`
- Modify: `internal/telegram/gotd/membership_test.go`
- Modify: `internal/repository/sqlite/production_store.go`
- Modify: `internal/repository/sqlite/production_store_test.go`

**Interfaces:**
- Consumes: membership rest fields and `GroupRestHours` from Task 1.
- Produces: exact-once rest initialization for immediate and moderated joins.

- [ ] **Step 1: Add failing transition tests**

Cover:

```go
func TestImmediateJoinSetsJoinedAt(t *testing.T)
func TestPendingApprovalBecomingMemberSetsJoinedAt(t *testing.T)
func TestExistingMembershipCheckDoesNotSetJoinedAt(t *testing.T)
func TestSaveMembershipInitializesRestOnlyOnce(t *testing.T)
func TestLegacyJoinedMembershipIsNotBackfilledWithRest(t *testing.T)
```

Assert that a repeated membership save preserves the original `rest_started_at`, `rest_until`, and duration.

- [ ] **Step 2: Run tests and verify RED**

```bash
go test ./internal/telegram/gotd ./internal/repository/sqlite -run 'Join|Membership|Rest' -count=1
```

Expected: at least the immediate-join and rest initialization tests fail.

- [ ] **Step 3: Implement transition rules**

Set `JoinedAt` when:

- the current application join call succeeds immediately;
- a previously pending approval becomes a member.

Do not set it when a membership check merely discovers an account that already belongs to a group.

In `markMember`, when `JoinedAt` is set for the first time and the queued membership contains a valid `RestDurationHours`, initialize:

```go
restStartedAt := joinedAt.UTC()
restUntil := restStartedAt.Add(time.Duration(membership.RestDurationHours) * time.Hour)
```

Persist all three values only when stored `joined_at IS NULL` becomes non-null, and use conditional update rules so later saves never move an existing rest period. Legacy rows whose stored `joined_at` is already non-null remain rest-null. The repository does not read application settings; duration is already snapshotted on the membership intent.

- [ ] **Step 4: Verify GREEN and commit**

```bash
go test ./internal/telegram/gotd ./internal/repository/sqlite -run 'Join|Membership|Rest' -count=1
git add internal/telegram/gotd internal/repository/sqlite
git commit -m "feat: start rest after confirmed channel joins"
```

### Task 3: Enforce Rest in the Delivery Scheduler

**Files:**
- Modify: `internal/usecase/scheduler.go`
- Modify: `internal/usecase/scheduler_test.go`
- Modify: `main.go`

**Interfaces:**
- Consumes: explicit `domain.GroupRestRepository` constructor dependency exposing `ActiveGroupRestUntil(ctx, accountIDs, now)`.
- Produces: public-delivery filtering and delay until the nearest rest expiry.

- [ ] **Step 1: Add failing scheduler tests**

Required cases:

```go
func TestSchedulerSkipsRestingAccountForPublicReply(t *testing.T)
func TestSchedulerAllowsPrivateMessageDuringRest(t *testing.T)
func TestKeywordPrivateTurnIsAllowedDuringRest(t *testing.T)
func TestKeywordPublicTurnWaitsUntilRestExpires(t *testing.T)
func TestPublicJobUsesAnotherNonRestingAccount(t *testing.T)
func TestAllRestingAccountsDelayUntilEarliestExpiry(t *testing.T)
func TestClaimedKeywordPublicTurnWaitsForItsAccountRest(t *testing.T)
func TestKeywordCandidatesApplyRestPerDeliveryCursor(t *testing.T)
```

- [ ] **Step 2: Run and verify RED**

```bash
go test ./internal/usecase -run 'Scheduler.*Rest|RestingAccount' -count=1
```

Expected: FAIL because scheduler does not consult rest state.

- [ ] **Step 3: Implement public eligibility**

Define the repository contract:

```go
type GroupRestRepository interface {
    ActiveGroupRestUntil(context.Context, []domain.ID, time.Time) (map[domain.ID]time.Time, error)
}
```

Inject it explicitly into `NewScheduler` and wire the production store in `main.go`. Before `Claim`, determine the actual next delivery separately for every candidate: an eligible private turn stays available, while a public turn requires no active rest. For an already claimed public keyword job, keep the claim and call `DelayTransientUntil` with that account's exact rest expiry without increasing attempts. For all-resting unclaimed jobs use `DelayTransientUntil`/`DelayedJobRepository.DelayUntil` with the earliest relevant future `rest_until`.

- [ ] **Step 4: Verify GREEN and commit**

```bash
go test ./internal/usecase -run 'Scheduler|KeywordResponse' -count=1
git add main.go internal/usecase/scheduler.go internal/usecase/scheduler_test.go
git commit -m "feat: pause group replies during account rest"
```

### Task 4: Store and Match Multi-Word Canonical Triggers

**Files:**
- Modify: `internal/usecase/analytics/canonical_service.go`
- Modify: `internal/usecase/analytics/canonical_service_test.go`
- Modify: `internal/telegram/gotd/live_sink.go`
- Modify: `internal/telegram/gotd/live_sink_test.go`
- Modify: `internal/analytics/canonical.go`
- Modify: `internal/analytics/canonical_test.go`

**Interfaces:**
- Produces: normalized canonical phrases with 1–8 words and existing one-word additional forms.
- Produces: sentence-local unordered conjunction matching.

- [ ] **Step 1: Add failing import tests**

Assert:

```go
normalizeCanonicalImportValue("машина едет") == "машина едет"
normalizeCanonicalImportValue("  CAR   moves ") == "car moves"
```

Reject mixed empty tokens, more than eight words, and values over 120 characters according to existing validation error style.

- [ ] **Step 2: Verify import RED**

```bash
go test ./internal/usecase/analytics ./internal/analytics -run 'Canonical.*Phrase|Import.*Phrase' -count=1
```

- [ ] **Step 3: Add failing matcher tests**

Required assertions:

```go
match("Сейчас машина очень тихо едет", "машина едет") == true
match("Машина стоит. Поезд едет.", "машина едет") == false
match("Машина стоит", "машина едет") == false
match("The car quietly moves", "car moves") == true
```

Also retain representative existing one-word Russian and English cases.

- [ ] **Step 4: Implement sentence-local phrase matching**

For each message sentence:

1. normalize and canonicalize sentence tokens;
2. normalize the configured canonical into a unique token set;
3. match when every canonical token exists in the sentence token set;
4. retain existing exact-token matching for each one-word additional form.

Return the configured canonical trigger name so Live-statistics and active trigger deletion keep their existing identity.

- [ ] **Step 5: Verify GREEN and commit**

```bash
go test ./internal/usecase/analytics ./internal/analytics ./internal/telegram/gotd -run 'Canonical|Keyword|Phrase|LiveSink' -count=1
git add internal/usecase/analytics internal/analytics internal/telegram/gotd
git commit -m "feat: support sentence-local phrase triggers"
```

### Task 5: Expose Account Rest Through Wails

**Files:**
- Create: `internal/usecase/account_rest.go`
- Create: `internal/usecase/account_rest_test.go`
- Create: `internal/transport/wails/account_rest_bindings.go`
- Create: `internal/transport/wails/account_rest_bindings_test.go`
- Modify: `main.go`
- Modify: `frontend/wailsjs/go/wails/Bindings.d.ts`
- Modify: `frontend/wailsjs/go/wails/Bindings.js`
- Modify: `frontend/wailsjs/go/models.ts`

**Interfaces:**
- Produces: `ListAccountRests(ctx) ([]AccountRestDTO, error)`.
- DTO fields: account ID/title, channel ID/title, catalog, status, startedAt, until, durationHours.

- [ ] **Step 1: Add failing use-case and binding tests**

Test UTC/RFC3339 serialization, `resting` versus `ready`, deterministic sort by `until DESC`, and sanitized errors.

- [ ] **Step 2: Verify RED**

```bash
go test ./internal/usecase ./internal/transport/wails -run 'AccountRest' -count=1
```

- [ ] **Step 3: Implement the read-only service and binding**

The backend lists only initialized rest rows, returns timestamps and stable status, and exposes the catalog. Remaining time is calculated by the frontend from `until`. Regenerate the tracked Wails bindings after adding the method.

- [ ] **Step 4: Register the binding, verify, and commit**

```bash
go test ./internal/usecase ./internal/transport/wails -run 'AccountRest' -count=1
git add main.go internal/usecase/account_rest.go internal/usecase/account_rest_test.go internal/transport/wails/account_rest_bindings.go internal/transport/wails/account_rest_bindings_test.go frontend/wailsjs/go
git commit -m "feat: expose account rest status"
```

### Task 6: Add the Account Rest Screen and Setting

**Files:**
- Create: `frontend/src/AccountRestView.tsx`
- Create: `frontend/src/AccountRestView.test.tsx`
- Create: `frontend/src/accountRest.ts`
- Create: `frontend/src/accountRest.test.ts`
- Modify: `frontend/src/App.tsx`
- Modify: `frontend/src/i18n.ts`
- Modify: `frontend/src/i18n.test.ts`
- Modify: `frontend/src/settingsHydration.ts`
- Modify: `frontend/src/settingsHydration.test.ts`
- Modify: `frontend/src/styles.css`

**Interfaces:**
- Consumes: `ListAccountRests`.
- Consumes and saves `groupRestHours`.

- [ ] **Step 1: Add failing pure-function tests**

Test Russian countdown output, ready/resting state, expired values clamped to zero, and settings default `36`.

- [ ] **Step 2: Add failing component tests**

Assert:

- navigation contains `Отлёжка аккаунтов`;
- table contains account, group, status, remaining, total hours;
- backend refresh timer is 30 seconds;
- local countdown changes without another backend call;
- settings input accepts `1..720`.

- [ ] **Step 3: Verify RED**

```bash
cd frontend
npm test -- AccountRestView.test.tsx accountRest.test.ts settingsHydration.test.ts i18n.test.ts
```

- [ ] **Step 4: Implement the screen**

Use a compact full-height table consistent with Channel Moderation. Keep server refresh and one-second local countdown in separate effects.

- [ ] **Step 5: Verify GREEN and commit**

```bash
npm test -- AccountRestView.test.tsx accountRest.test.ts settingsHydration.test.ts i18n.test.ts
npm run build
git add frontend/src
git commit -m "feat: add account rest dashboard"
```

### Task 7: Preserve Phrase Imports and Add Live Selection Action

**Files:**
- Create: `frontend/src/SelectionActionMenu.tsx`
- Create: `frontend/src/SelectionActionMenu.test.tsx`
- Modify: `frontend/src/AnalyticsView.tsx`
- Modify: `frontend/src/AnalyticsView.test.tsx`
- Modify: `frontend/src/LiveStatisticsView.tsx`
- Modify: `frontend/src/LiveStatisticsView.test.tsx`
- Modify: `frontend/src/styles.css`

**Interfaces:**
- Consumes: `BulkImportCanonicalKeywords(entries, "negative")`.
- Produces: one-line phrase parsing and source-message selection popover.

- [ ] **Step 1: Add failing phrase import tests**

Assert that:

```text
машина едет
бабки (бабосы, бабосики, баблишко)
```

creates two canonical entries, preserves the space inside `машина едет`, and keeps the three parenthesized one-word forms attached to `бабки`.

- [ ] **Step 2: Add failing Live-selection tests**

Simulate a selection inside the source-message cell, assert `В негативные` appears, click it, and assert:

```ts
BulkImportCanonicalKeywords(
  [{ canonical: "машина едет", forms: [] }],
  "negative",
)
```

Also assert no action for selections outside that cell, selections whose range endpoints belong to different `data-message-id` cells, or selections over 120 characters. Component tests for `SelectionActionMenu` cover Escape, focus restoration, viewport clamping, disabled state while importing, and retained selection after an import failure.

- [ ] **Step 3: Verify RED**

```bash
cd frontend
npm test -- AnalyticsView.test.tsx LiveStatisticsView.test.tsx
```

- [ ] **Step 4: Implement parsing and selection UI**

Parse one canonical entry per non-empty line while retaining whitespace inside each canonical. Continue splitting parenthesized one-word additional forms by whitespace and punctuation. Use delegated mouse-up only when both range endpoints resolve to the same source cell marked with `data-message-id`, freeze the normalized selection and pointer/selection-rect position in parent state, and render it through `SelectionActionMenu`. The menu clamps to an 8px viewport margin, autofocuses its action, closes on Escape, restores prior focus, disables while importing, clears after success, retains the snapshot after failure, and never navigates tabs.

- [ ] **Step 5: Verify GREEN and commit**

```bash
npm test -- AnalyticsView.test.tsx LiveStatisticsView.test.tsx SelectionActionMenu.test.tsx
npm run build
git add frontend/src/SelectionActionMenu.tsx frontend/src/SelectionActionMenu.test.tsx frontend/src/AnalyticsView.tsx frontend/src/AnalyticsView.test.tsx frontend/src/LiveStatisticsView.tsx frontend/src/LiveStatisticsView.test.tsx frontend/src/styles.css
git commit -m "feat: add phrase imports and live exclusions"
```

### Task 8: Integration Verification and macOS Artifact

**Files:**
- Modify only if generated version metadata requires it; do not add visible release notes.

**Interfaces:**
- Verifies all prior tasks together.

- [ ] **Step 1: Confirm the application is stopped**

```bash
pgrep -fl 'Telegram Companion|telegram-companion|snowflake|tor'
```

Expected: no Telegram Companion, Tor, or Snowflake process started by this application. Repeat this exact process check after Steps 2, 3, and 4.

- [ ] **Step 2: Run backend verification**

```bash
go test ./... -count=1
go test -race ./internal/usecase ./internal/telegram/gotd ./internal/repository/sqlite -count=1
```

Expected: PASS.

Run the process check from Step 1 again before continuing.

- [ ] **Step 3: Run frontend verification**

```bash
cd frontend
npm test -- --run
npm run build
```

Expected: all tests and TypeScript/Vite build pass.

Run the process check from Step 1 again before continuing.

- [ ] **Step 4: Build without launching**

```bash
wails build -platform darwin/arm64 -clean
```

Expected: arm64 macOS application builds successfully and no application process starts.

Run the process check from Step 1 again and require the same empty result.

- [ ] **Step 5: Verify version and artifact checksum**

Assert the configured UI and build version strings equal `0.8.0`, calculate SHA-256 for the resulting DMG/application archive, and record the absolute artifact path. Do not publish or deploy without a separate release step.
