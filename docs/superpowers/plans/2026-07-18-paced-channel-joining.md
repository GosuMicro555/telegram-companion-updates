# Paced Channel Joining Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist and enforce a sequential, restart-safe 10–60 minute randomized join schedule for every eligible account when a channel is activated.

**Architecture:** Store `join_not_before` on each membership and create all queue entries atomically from `CatalogService`. gotd checks the due time before a join RPC; settings and UI only affect future activations.

**Tech Stack:** Go 1.25+, SQLite/Goose, gotd MTProto, Wails, React/TypeScript/Vitest.

## Global Constraints

- All Telegram clients, listener, Tor, Snowflake and desktop app remain stopped during development and tests.
- Every role participates; account order is stable `created_at, id`.
- Default bounds are 10 and 60 minutes; each bound must be 10–60 and `from <= to`.
- The first account is due immediately; later timestamps use cumulative persisted random gaps.
- Existing queues never change on restart, repeated START or settings edits.
- No new persisted membership status is introduced.
- `request_submitted_at` and `joined_at` retain their moderation semantics and immutability.
- No version or release-note change is part of this plan.

---

### Task 1: Persist join scheduling fields and settings

**Files:**
- Modify: `internal/domain/channel.go`
- Modify: `internal/domain/settings.go`
- Create: `internal/repository/sqlite/migrations/000021_membership_join_schedule.sql`
- Modify: `internal/repository/sqlite/production_store.go`
- Modify: `internal/repository/sqlite/db_test.go`
- Modify: `internal/repository/sqlite/production_store_test.go`

**Interfaces:**
- Produces `ChannelMembership.JoinNotBefore *time.Time`.
- Produces `AppSettings.JoinIntervalMinMinutes int` and `JoinIntervalMaxMinutes int`.

- [ ] Write failing migration/round-trip tests for null compatibility, persisted future time, immutable UPSERT time and settings default `10/60`.
- [ ] Run `go test ./internal/repository/sqlite -run 'JoinSchedule|JoinInterval' -count=1`; expect RED for missing fields/column.
- [ ] Add nullable `join_not_before`, include it in all membership SELECT/INSERT/UPSERT paths, preserving existing non-null values. Extend JSON settings normalization so zero legacy values become `10/60`.
- [ ] Run the focused tests; expect PASS.
- [ ] Commit: `feat: persist paced channel joins`.

---

### Task 2: Atomically construct the cumulative queue

**Files:**
- Modify: `internal/usecase/catalogs.go`
- Modify: `internal/usecase/catalogs_test.go`
- Modify: `internal/usecase/random.go` or create `internal/usecase/join_schedule.go`
- Modify: `internal/repository/sqlite/production_store.go`

**Interfaces:**

```go
type JoinDelaySource interface { Minutes(min, max int) (int, error) }
type CatalogActivationStore interface {
    ActivateCatalogWithMemberships(context.Context, domain.SourceCatalog, domain.Channel, []domain.ChannelMembership) error
}
func NewCatalogServiceWithJoinSchedule(CatalogStore, JoinDelaySource, func() time.Time) *CatalogService
```

- [ ] Write failing tests using delay sequence `10,60,15` for four accounts and assert due times `now`, `now+10m`, `now+70m`, `now+85m`; include 100-account monotonicity, all roles, no rewrite of member/pending rows, and rollback error.
- [ ] Run `go test ./internal/usecase -run 'Catalog.*JoinSchedule|ToggleActive' -count=1`; expect RED.
- [ ] Implement validated cumulative scheduling. Prefer the atomic store extension; keep a compatibility path for test stores that returns a clear unsupported error rather than partial activation.
- [ ] Implement SQLite transaction that saves the active channel and all memberships or rolls back all changes.
- [ ] Run focused usecase and SQLite tests; expect PASS.
- [ ] Commit: `feat: schedule channel memberships atomically`.

---

### Task 3: Gate gotd join RPCs by persisted due time

**Files:**
- Modify: `internal/telegram/gotd/membership.go`
- Modify: `internal/telegram/gotd/membership_test.go`
- Modify: `internal/telegram/gotd/updates_test.go`
- Modify: `internal/telegram/gotd/sender_test.go`

**Interfaces:**
- Consumes `ChannelMembership.JoinNotBefore`.
- Preserves `pending_approval` rechecks and all moderation timestamps.

- [ ] Write failing tests proving a future `joining` membership performs zero RPCs, a due membership performs one RPC, `pending_approval` ignores the join delay, and repeated reconcile preserves the timestamp.
- [ ] Run `go test ./internal/telegram/gotd -run 'JoinNotBefore|PendingApproval' -count=1`; expect RED.
- [ ] Add an early return only for `status=joining` and future due time. Prevent missing-intent creation from performing a join unless a persisted activation intent exists.
- [ ] Run focused gotd tests; expect PASS.
- [ ] Commit: `feat: enforce paced Telegram joins`.

---

### Task 4: Expose settings and planned state in Wails

**Files:**
- Modify: `internal/transport/wails/bindings.go`
- Modify: `internal/transport/wails/bindings_test.go`
- Modify: `frontend/wailsjs/go/models.ts`
- Modify: `frontend/wailsjs/go/wails/Bindings.d.ts`

**Interfaces:**
- App settings DTO adds `joinIntervalMinMinutes` and `joinIntervalMaxMinutes`.
- Catalog membership DTO adds `joinNotBefore` and computed `planned` state where appropriate.

- [ ] Write failing binding tests for `10/60` defaults, invalid ranges, save/reload and future planned memberships.
- [ ] Run focused Wails tests; expect RED.
- [ ] Implement DTO mapping/validation and generated contracts without touching secrets.
- [ ] Run focused tests; expect PASS.
- [ ] Commit: `feat: expose channel join pacing`.

---

### Task 5: Add interval controls and planned status to Channels

**Files:**
- Modify: `frontend/src/App.tsx`
- Modify: `frontend/src/settingsHydration.ts`
- Modify: `frontend/src/i18n.ts`
- Modify: `frontend/src/styles.css`
- Modify: `frontend/src/channelActivation.test.ts`
- Create or modify: `frontend/src/channelJoinSchedule.test.ts`

**Interfaces:**
- UI reads/saves the two Wails app-setting fields.
- Existing activation confirmation remains mandatory.

- [ ] Write failing tests for defaults, input bounds, `from <= to`, persistence and computed `Запланирован` display.
- [ ] Run `npm test -- channelActivation.test.ts channelJoinSchedule.test.ts`; expect RED.
- [ ] Add two compact numeric fields to Channels; show inline validation and disable activation while invalid. Do not recalculate already returned rows.
- [ ] Run focused tests and `npm run build`; expect PASS.
- [ ] Commit: `feat: configure paced channel joins`.

---

### Task 6: Offline verification

**Files:** only task-caused fixes.

- [ ] Run `go test ./... -count=1` on Ubuntu or the existing Linux build environment.
- [ ] Run `npm test` and `npm run build` in `frontend`.
- [ ] Run `git diff --check`.
- [ ] Confirm `pgrep -af 'telegram-companion|snowflake|tor' || true` returns no project processes; do not start them.
- [ ] Commit only verified task-caused fixes; never create an empty commit.

