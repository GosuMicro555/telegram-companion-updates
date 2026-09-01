# Channel Moderation Dashboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a local, persistent "Channel moderation" section that shows every channel where at least one Telegram account submitted a join request, including aggregate and per-account wait/join status and elapsed time.

**Architecture:** Extend the existing account-channel membership record with immutable request/join timestamps, populate them only from the existing gotd membership state machine, and aggregate both outbound and scout catalogs in a focused use case. Expose safe DTOs through Wails and render them with the existing configurable table, extended by one reusable expanded-row hook.

**Tech Stack:** Go 1.25+, SQLite/Goose, gotd MTProto, Wails, React 19, TypeScript, Vitest.

## Global Constraints

- Telegram automation, listeners, Tor and Snowflake remain stopped for the entire implementation and verification.
- Do not launch or install the desktop application and do not perform live Telegram tests.
- Verification is limited to offline Go tests, frontend tests, TypeScript/Vite builds and process inspection.
- Include both `outbound` and `scout` catalogs; roles do not filter moderation history.
- Include only memberships that have entered `pending_approval`; direct joins do not appear.
- Preserve completed moderation history indefinitely.
- Display dates as `ДД.ММ.ГГГГ | ЧЧ:ММ` in Russian and equivalent localized formatting in English.
- Refresh the view from local SQLite every 15 seconds and when the section opens; refreshing must preserve expanded rows.
- The page itself must not vertically scroll; only the table workspace may scroll.
- Reuse configurable table behavior for sorting, column order, width and visibility under a dedicated storage key.
- Do not change the application version or release notes in this feature.

---

### Task 1: Persist moderation lifecycle timestamps

**Files:**
- Modify: `internal/domain/channel.go`
- Create: `internal/repository/sqlite/migrations/000020_channel_moderation_timestamps.sql`
- Modify: `internal/repository/sqlite/production_store.go`
- Modify: `internal/repository/sqlite/db_test.go`
- Modify: `internal/repository/sqlite/delivery_migration_test.go`

**Interfaces:**
- Produces: `domain.ChannelMembership.RequestSubmittedAt *time.Time`
- Produces: `domain.ChannelMembership.JoinedAt *time.Time`
- Preserves: `ProductionStore.ListMemberships`, `ProductionStore.SaveMembership`, `CatalogRepository.LoadMembership`, `CatalogRepository.SaveMembership` signatures.

- [ ] **Step 1: Write failing SQLite round-trip and migration tests**

Add a test that saves and reloads:

```go
membership := domain.ChannelMembership{
    AccountID: "account-1", ChannelID: "channel-1",
    Status: "member", IsMember: true,
    RequestSubmittedAt: ptrTime(time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)),
    JoinedAt: ptrTime(time.Date(2026, 7, 18, 8, 17, 0, 0, time.UTC)),
}
```

Assert both timestamps survive `SaveMembership` and `ListMemberships`. Add a migration assertion that an existing `pending_approval` row receives `request_submitted_at = last_check_at`, while an existing `member` row has both new columns null.

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/repository/sqlite -run 'MembershipModerationTimestamps|Migration20' -count=1`

Expected: compilation or SQL failure because the fields/columns do not exist.

- [ ] **Step 3: Add fields and migration**

Extend the domain type:

```go
type ChannelMembership struct {
    AccountID          ID
    ChannelID          ID
    IsMember           bool
    Status             string
    LastCheckAt        *time.Time
    RequestSubmittedAt *time.Time
    JoinedAt           *time.Time
    LastError          string
}
```

Migration `000020` must add nullable `request_submitted_at` and `joined_at`, then backfill only `status='pending_approval' AND last_check_at IS NOT NULL`. Its down section must recreate the `000019` table shape and copy all original fields so rollback works on supported SQLite versions.

Update every membership SELECT/Scan and INSERT/UPSERT to include both fields. Use the existing `parseTime` and `formatTimePtr` helpers.

- [ ] **Step 4: Verify GREEN and regression coverage**

Run: `go test ./internal/repository/sqlite -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/channel.go internal/repository/sqlite/migrations/000020_channel_moderation_timestamps.sql internal/repository/sqlite/production_store.go internal/repository/sqlite/db_test.go internal/repository/sqlite/delivery_migration_test.go
git commit -m "feat: persist channel moderation timestamps"
```

---

### Task 2: Record request and approval transitions

**Files:**
- Modify: `internal/telegram/gotd/membership.go`
- Modify: `internal/telegram/gotd/membership_test.go`

**Interfaces:**
- Consumes: `ChannelMembership.RequestSubmittedAt`, `ChannelMembership.JoinedAt` from Task 1.
- Produces: deterministic timestamp transitions in `reconcileMembershipIntent` and `membershipTransition.markMember`.

- [ ] **Step 1: Write failing state-transition tests**

Cover these exact cases:

```go
// INVITE_REQUEST_SENT: set request time once, leave joined time nil.
// Rechecking pending request: preserve the original request time.
// pending_approval -> member: preserve request time and set joined time.
// joining -> member direct join: leave both moderation timestamps nil.
```

Use fixed UTC times and assert equality, not approximate duration.

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/telegram/gotd -run 'Membership.*Moderation|PendingApproval' -count=1`

Expected: FAIL because transitions do not populate the new timestamps.

- [ ] **Step 3: Implement minimal transition logic**

When Telegram returns `INVITE_REQUEST_SENT`, set `RequestSubmittedAt` only when it is nil. Pending rechecks update `LastCheckAt` but never overwrite the request timestamp. Change member completion to retain the previous status before mutation:

```go
wasPendingApproval := r.membership.Status == membershipPendingApproval || r.membership.RequestSubmittedAt != nil
r.membership.IsMember = true
r.membership.Status = membershipMember
r.membership.LastCheckAt = &checkedAt
if wasPendingApproval && r.membership.JoinedAt == nil {
    r.membership.JoinedAt = &checkedAt
}
```

Direct joins must not synthesize request history.

- [ ] **Step 4: Verify GREEN**

Run: `go test ./internal/telegram/gotd -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/telegram/gotd/membership.go internal/telegram/gotd/membership_test.go
git commit -m "feat: track moderation request lifecycle"
```

---

### Task 3: Aggregate channel moderation history locally

**Files:**
- Create: `internal/usecase/channel_moderation.go`
- Create: `internal/usecase/channel_moderation_test.go`

**Interfaces:**
- Produces:

```go
type ChannelModerationStore interface {
    ListAccounts(context.Context) ([]domain.Account, error)
    ListCatalog(context.Context, domain.SourceCatalog) ([]domain.Channel, error)
    ListMemberships(context.Context, domain.SourceCatalog, domain.ID) ([]domain.ChannelMembership, error)
}

type ChannelModerationService struct { /* store */ }
func NewChannelModerationService(ChannelModerationStore) *ChannelModerationService
func (s *ChannelModerationService) List(context.Context, time.Time) ([]ChannelModerationChannel, error)
```

`ChannelModerationChannel` contains catalog, channel ID/title/link/topic, applications/joined/pending counts, aggregate status, first request, duration, and nested `[]ChannelModerationAccount`. Nested rows contain account key/title/role, request time, optional join time, duration and raw membership status.

- [ ] **Step 1: Write failing aggregation tests**

Test one outbound channel with two requests (one member, one pending) and one scout channel with all requests approved. Assert:

```go
require.Equal(t, "partial", rows[0].Status)
require.Equal(t, 2, rows[0].Applications)
require.Equal(t, 1, rows[0].Joined)
require.Equal(t, 1, rows[0].Pending)
require.Equal(t, 45*time.Minute, rows[0].Duration) // now - first request while pending
```

Also assert: direct-member rows are excluded; error/flood-wait rows with a request timestamp count as pending; all-approved duration ends at the latest `JoinedAt`; account label prefers `DisplayName`, then `PhoneMasked`, then localized-neutral `Telegram account`; store errors are wrapped with catalog/channel context.

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/usecase -run ChannelModeration -count=1`

Expected: compilation failure because the service does not exist.

- [ ] **Step 3: Implement aggregation**

Read accounts once, then both catalogs. For each channel read memberships and retain only rows with `RequestSubmittedAt != nil`. Derive:

```go
switch {
case joined == applications:
    status = "member"
case joined > 0:
    status = "partial"
default:
    status = "pending_approval"
}
```

Sort account details by request time then stable account key; sort channels by first request descending then title. Clamp negative durations to zero.

- [ ] **Step 4: Verify GREEN**

Run: `go test ./internal/usecase -run ChannelModeration -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/usecase/channel_moderation.go internal/usecase/channel_moderation_test.go
git commit -m "feat: aggregate channel moderation history"
```

---

### Task 4: Expose safe Wails moderation DTOs

**Files:**
- Modify: `internal/transport/wails/bindings.go`
- Modify: `internal/transport/wails/bindings_test.go`
- Modify: `frontend/wailsjs/go/wails/Bindings.js`
- Modify: `frontend/wailsjs/go/wails/Bindings.d.ts`

**Interfaces:**
- Consumes: `ChannelModerationService.List` from Task 3.
- Produces: `func (b *Bindings) GetChannelModeration() ([]ChannelModerationDTO, error)`.
- Produces JS binding: `GetChannelModeration(): Promise<Array<ChannelModerationDTO>>`.

- [ ] **Step 1: Write failing binding test**

Construct `settingsStoreStub` with outbound and scout moderation rows, call `GetChannelModeration`, and assert JSON-facing values:

```go
require.Equal(t, "partial", result[0].Status)
require.Equal(t, "2026-07-18T08:00:00Z", result[0].FirstRequestAt)
require.Equal(t, int64(2700), result[0].DurationSeconds)
require.NotContains(t, marshaled, "telegramId")
```

The DTO account label must be display name or masked phone and must not contain a Telegram numeric ID.

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/transport/wails -run ChannelModeration -count=1`

Expected: compilation failure because the binding is absent.

- [ ] **Step 3: Implement DTO mapping and binding**

Add DTOs with lower-camel JSON fields and RFC3339 timestamps. Initialize `channelModeration` in both successful and fallback binding constructors when `settings` implements `usecase.ChannelModerationStore`. `GetChannelModeration` uses a short read-only operation timeout and returns an empty non-nil slice when no history exists.

Add the generated-style wrapper:

```js
export function GetChannelModeration() {
  return window['go']['wails']['Bindings']['GetChannelModeration']();
}
```

and its TypeScript declaration without hand-editing unrelated generated entries.

- [ ] **Step 4: Verify GREEN**

Run: `go test ./internal/transport/wails -run ChannelModeration -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/transport/wails/bindings.go internal/transport/wails/bindings_test.go frontend/wailsjs/go/wails/Bindings.js frontend/wailsjs/go/wails/Bindings.d.ts
git commit -m "feat: expose channel moderation history"
```

---

### Task 5: Support expandable configurable table rows

**Files:**
- Modify: `frontend/src/ConfigurableStatsTableComponent.tsx`
- Modify: `frontend/src/ConfigurableStatsTable.test.tsx`
- Modify: `frontend/src/styles.css`

**Interfaces:**
- Produces optional prop:

```ts
renderAfterRow?: (row: Row) => ReactNode;
```

The expanded block renders immediately after its parent row and spans the current visible grid width without changing sort/order/resize/visibility behavior.

- [ ] **Step 1: Write failing component test**

Render two rows with `renderAfterRow` returning detail only for the first. Assert the detail follows the first row in DOM order, uses the computed table width, and column reordering/visibility persistence remains unchanged.

- [ ] **Step 2: Verify RED**

Run: `npm test -- ConfigurableStatsTable.test.tsx`

Working directory: `frontend`

Expected: TypeScript/test failure because `renderAfterRow` is unsupported.

- [ ] **Step 3: Implement the shared hook**

Wrap each main row and optional detail in a keyed `Fragment`. Render detail in `configurableStatsTable__expandedRow` with `style={{ width: gridPixelWidth }}`; do not duplicate table preference logic.

- [ ] **Step 4: Verify GREEN**

Run: `npm test -- ConfigurableStatsTable.test.tsx configurableStatsTable.test.ts`

Working directory: `frontend`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/ConfigurableStatsTableComponent.tsx frontend/src/ConfigurableStatsTable.test.tsx frontend/src/styles.css
git commit -m "feat: support expandable configurable rows"
```

---

### Task 6: Build and integrate the moderation dashboard

**Files:**
- Create: `frontend/src/channelModeration.ts`
- Create: `frontend/src/channelModeration.test.ts`
- Create: `frontend/src/ChannelModerationView.tsx`
- Create: `frontend/src/ChannelModerationView.test.tsx`
- Modify: `frontend/src/App.tsx`
- Modify: `frontend/src/i18n.ts`
- Modify: `frontend/src/styles.css`

**Interfaces:**
- Consumes: `GetChannelModeration` from Task 4.
- Consumes: `ConfigurableStatsTable.renderAfterRow` from Task 5.
- Produces section key: `channelModeration`.
- Produces storage key: `channel-moderation.table.v1`.

- [ ] **Step 1: Write failing pure-model tests**

Test normalization of lower/upper-case Wails fields, filtering (`all`, `pending_approval`, `partial`, `member`), title/link/topic search, deterministic sorting for all eight columns, zero-safe duration formatting, and Russian date format `18.07.2026 | 11:17`.

- [ ] **Step 2: Verify pure-model RED**

Run: `npm test -- channelModeration.test.ts`

Working directory: `frontend`

Expected: module-not-found failure.

- [ ] **Step 3: Implement the pure model**

Define normalized row/account types and pure helpers:

```ts
normalizeChannelModeration(value: unknown): ChannelModerationRow[]
filterChannelModeration(rows, filter, search): ChannelModerationRow[]
sortChannelModeration(rows, sort): ChannelModerationRow[]
formatModerationDuration(seconds: number, locale: Locale): string
formatModerationDate(value: string, locale: Locale): string
```

- [ ] **Step 4: Verify pure-model GREEN**

Run: `npm test -- channelModeration.test.ts`

Working directory: `frontend`

Expected: PASS.

- [ ] **Step 5: Write failing view tests**

Mock only the Wails boundary. Verify initial load, 15-second local refresh, unmount cancellation, retained expanded channel across refresh, four filter controls, counters, search, error banner, empty state, and expanded account columns. Confirm no START/STOP or Telegram command appears in the view.

- [ ] **Step 6: Verify view RED**

Run: `npm test -- ChannelModerationView.test.tsx`

Working directory: `frontend`

Expected: module-not-found failure.

- [ ] **Step 7: Implement the view and shell integration**

Add `ChannelModerationView`, poll `GetChannelModeration` every 15 seconds, keep expansion in `Set<string>`, and render columns:

```text
Канал | Тематика | Заявок | Вступили | Ожидают | Статус | Первая заявка | Время ожидания
```

Expanded rows render:

```text
Telegram-аккаунт | Роль | Заявка подана | Вступил | Через сколько | Статус
```

Add the navigation item immediately after Channels, localized title/lead/copy, and `content--channelModeration`. CSS must use the existing light/dark tokens, thin borders, compact rows, stable table dimensions and internal overflow only.

- [ ] **Step 8: Verify frontend GREEN**

Run: `npm test -- channelModeration.test.ts ChannelModerationView.test.tsx ConfigurableStatsTable.test.tsx`

Working directory: `frontend`

Expected: PASS.

Run: `npm run build`

Working directory: `frontend`

Expected: TypeScript and Vite build PASS.

- [ ] **Step 9: Commit**

```bash
git add frontend/src/channelModeration.ts frontend/src/channelModeration.test.ts frontend/src/ChannelModerationView.tsx frontend/src/ChannelModerationView.test.tsx frontend/src/App.tsx frontend/src/i18n.ts frontend/src/styles.css
git commit -m "feat: add channel moderation dashboard"
```

---

### Task 7: Offline integration verification

**Files:**
- Modify only files required by failures directly caused by Tasks 1-6.

**Interfaces:**
- Verifies the complete persistent-membership -> aggregation -> Wails -> React path.

- [ ] **Step 1: Run complete Go verification**

Run: `go test ./... -count=1`

Expected: PASS with no package failures.

- [ ] **Step 2: Run complete frontend verification**

Run: `npm test`

Working directory: `frontend`

Expected: all tests PASS.

Run: `npm run build`

Working directory: `frontend`

Expected: PASS.

- [ ] **Step 3: Confirm Telegram processes remain stopped**

Run on Ubuntu only if the SSH host is already reachable, without starting anything:

```bash
pgrep -af 'telegram-companion|snowflake|tor' || true
```

Expected: no application, listener, Snowflake or Tor process owned by this project.

- [ ] **Step 4: Inspect final diff and commit verification-only fixes**

Run: `git status --short` and `git diff --check`.

Expected: no whitespace errors and no unrelated files. If verification required a code fix, preserve TDD evidence and commit it as `fix: complete channel moderation integration`; otherwise do not create an empty commit.

