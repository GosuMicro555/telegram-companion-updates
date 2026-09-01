# Scheduled Direct Messages Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a disabled-by-default section that schedules one shared private message to 1–3 consented Telegram usernames using 1–3 explicitly selected spammer accounts.

**Architecture:** Use dedicated schedule/run/delivery tables so task text, finite recurrence and durable account assignment remain independent from keyword jobs. A separate runner reuses gotd account leases, rate limits and privacy-error classification without starting automatically.

**Tech Stack:** Go 1.25+, SQLite/Goose, gotd MTProto, Wails, React/TypeScript/Vitest.

## Global Constraints

- Maximum three contacts, three selected spammer accounts, twelve runs and one delivery per contact per run.
- Recurrence is finite and one of: once, 5/10/30 minutes, 1–12 hours, daily, weekly.
- Recipients are entered only as username, `@username` or `https://t.me/username`.
- Account assignment is persisted round-robin and never changes after privacy/invalid-peer failure.
- Resolving a username may show `resolved`; `open`/`closed` is only authoritative after a send attempt.
- New and restored tasks are paused by default for this release; no background runner starts during development.
- Keyword automation, Telegram listener, Tor, Snowflake and desktop app remain stopped for all tests.
- No version or release-note change is part of this plan.

---

### Task 1: Add scheduled-DM domain and parser

**Files:**
- Create: `internal/domain/scheduled_dm.go`
- Create: `internal/domain/scheduled_dm_test.go`
- Create: `internal/usecase/scheduled_dm_parser.go`
- Create: `internal/usecase/scheduled_dm_parser_test.go`

**Interfaces:**

```go
type ScheduledDMStatus string
type ScheduledDMRecurrence string
type ScheduledDMTask struct { /* persisted task, recipients, account IDs */ }
type ScheduledDMDelivery struct { /* stable account/recipient/run assignment */ }
func ParseScheduledDMRecipients(string) ([]string, error)
func RecurrenceDuration(ScheduledDMRecurrence) (time.Duration, bool)
```

- [ ] Write failing table tests for all accepted username forms, deduplication, invalid domains/paths, 1–3 limit and all interval values.
- [ ] Run focused domain/usecase tests; expect RED.
- [ ] Implement strict normalization and typed statuses/recurrences with no Telegram calls.
- [ ] Run focused tests; expect PASS.
- [ ] Commit: `feat: define scheduled direct messages`.

---

### Task 2: Persist tasks, runs and deliveries

**Files:**
- Create: `internal/repository/sqlite/migrations/000022_scheduled_direct_messages.sql`
- Create: `internal/repository/sqlite/scheduled_dm_store.go`
- Create: `internal/repository/sqlite/scheduled_dm_store_test.go`
- Modify: `internal/repository/sqlite/db_test.go`

**Interfaces:**

```go
type ScheduledDMRepository interface {
    ListTasks(context.Context) ([]domain.ScheduledDMTask, error)
    SaveTask(context.Context, domain.ScheduledDMTask) error
    SetTaskStatus(context.Context, domain.ID, domain.ScheduledDMStatus, time.Time) error
    ClaimDueDelivery(context.Context, time.Time, time.Duration) (*domain.ScheduledDMDelivery, error)
    CompleteDelivery(context.Context, domain.ScheduledDMDeliveryResult) error
    DelayDelivery(context.Context, domain.ID, string, time.Time) error
}
```

- [ ] Write failing migration/round-trip tests, atomic run creation tests, lease recovery, stable sender/random ID and restart-safe next-run tests.
- [ ] Run focused SQLite tests; expect RED.
- [ ] Implement normalized tables with foreign keys and a transactional due-run materializer. Migration Down removes only these tables.
- [ ] Run focused tests; expect PASS.
- [ ] Commit: `feat: persist scheduled direct messages`.

---

### Task 3: Validate and manage schedules

**Files:**
- Create: `internal/usecase/scheduled_dm.go`
- Create: `internal/usecase/scheduled_dm_test.go`

**Interfaces:**

```go
func NewScheduledDMService(ScheduledDMRepository, usecase.AccountStore, func() time.Time) *ScheduledDMService
func (s *ScheduledDMService) Save(context.Context, ScheduledDMDraft) (domain.ScheduledDMTask, error)
func (s *ScheduledDMService) Start(context.Context, domain.ID) error
func (s *ScheduledDMService) Stop(context.Context, domain.ID) error
func (s *ScheduledDMService) Cancel(context.Context, domain.ID) error
```

- [ ] Write failing tests for all limits, non-empty text, future start, selected active spammer accounts, durable round-robin assignment and paused defaults.
- [ ] Run focused usecase tests; expect RED.
- [ ] Implement validation and lifecycle without invoking Telegram.
- [ ] Run focused tests; expect PASS.
- [ ] Commit: `feat: manage scheduled DM tasks`.

---

### Task 4: Resolve and send scheduled usernames

**Files:**
- Modify: `internal/telegram/gotd/sender.go`
- Modify: `internal/telegram/gotd/sender_test.go`
- Modify: `internal/telegram/gotd/outbound.go`
- Modify: `internal/telegram/gotd/outbound_test.go`
- Create: `internal/usecase/scheduled_dm_runner.go`
- Create: `internal/usecase/scheduled_dm_runner_test.go`

**Interfaces:**

```go
type ScheduledDMSender interface {
    ResolveUsername(context.Context, domain.Account, string) error
    SendScheduledPrivateMessage(context.Context, domain.Account, domain.ScheduledDMDelivery) error
}
```

- [ ] Write failing tests that resolve per assigned account, preserve task text, classify closed/invalid as terminal, delay FloodWait, avoid account reassignment and calculate the next finite run.
- [ ] Run focused gotd/usecase tests; expect RED.
- [ ] Implement a dedicated send path that does not substitute global replies. Reuse account lease/rate/flood handling and stable Telegram random ID.
- [ ] Run focused tests; expect PASS.
- [ ] Commit: `feat: deliver scheduled direct messages`.

---

### Task 5: Add Wails API and disabled lifecycle composition

**Files:**
- Create: `internal/transport/wails/scheduled_dm_bindings.go`
- Modify: `internal/transport/wails/bindings.go`
- Modify: `internal/transport/wails/bindings_test.go`
- Modify: `main.go`
- Modify: `frontend/wailsjs/go/wails/Bindings.js`
- Modify: `frontend/wailsjs/go/wails/Bindings.d.ts`
- Modify: `frontend/wailsjs/go/models.ts`

**Interfaces:** `ListScheduledDMTasks`, `SaveScheduledDMTask`, `StartScheduledDMTask`, `StopScheduledDMTask`, `CancelScheduledDMTask`, `ResolveScheduledDMRecipients`.

- [ ] Write failing binding tests for safe DTOs, pause defaults, lifecycle calls and recipient resolution states.
- [ ] Run focused Wails tests; expect RED.
- [ ] Wire the service and runner so composition creates it stopped; only explicit section START starts its loop. Global automation START must not start it.
- [ ] Run focused tests; expect PASS.
- [ ] Commit: `feat: expose scheduled DM controls`.

---

### Task 6: Build the «Сообщения в ЛС» UI

**Files:**
- Create: `frontend/src/scheduledDM.ts`
- Create: `frontend/src/scheduledDM.test.ts`
- Create: `frontend/src/ScheduledDMView.tsx`
- Create: `frontend/src/ScheduledDMView.test.tsx`
- Modify: `frontend/src/App.tsx`
- Modify: `frontend/src/i18n.ts`
- Modify: `frontend/src/styles.css`

**Interfaces:** Consumes Task 5 Wails methods; adds section key `scheduledDM`.

- [ ] Write failing pure tests for normalization, interval options, finite counts, status mapping and account assignment display.
- [ ] Write failing view tests for 1–3 contacts, account selection, common text, date/time, interval/count, save, resolve, START/STOP/cancel and error/empty states.
- [ ] Implement a compact desktop workspace using `DateTimeField`, existing theme tokens and internal table scrolling. Show `Контакт найден` before send and open/closed only from delivery outcome.
- [ ] Run focused tests and `npm run build`; expect PASS.
- [ ] Commit: `feat: add scheduled DM workspace`.

---

### Task 7: Offline verification

**Files:** only task-caused fixes.

- [ ] Run `go test ./... -count=1` in the Linux test environment.
- [ ] Run `npm test` and `npm run build` in `frontend`.
- [ ] Run `git diff --check`.
- [ ] Confirm no `telegram-companion`, Snowflake or project Tor process is running; do not launch them.
- [ ] Confirm newly migrated tasks are paused and no trigger/delivery runner auto-started.
- [ ] Commit only verified task-caused fixes; no empty commit.

