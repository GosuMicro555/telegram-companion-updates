# Keyword Delivery Alternation And Statistics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make every spammer account alternate DM and public reply per successful keyword delivery, fall back to a public reply when Telegram privacy blocks the DM, and expose accurate public/DM/closed-DM statistics.

**Architecture:** `LiveSink` will enqueue one idempotent `keyword_response` job per triggering Telegram message. `Scheduler` will claim the next round-robin account, read that account's persisted delivery cursor, and choose DM or public delivery. SQLite completion methods will atomically finish the job, update counters, and advance the cursor. Privacy-blocked DMs are a typed outcome that triggers one public fallback with the same account; transport failures remain retryable and do not advance state.

**Tech Stack:** Go 1.26, gotd/td, SQLite/goose, Wails v2, React 19, TypeScript, Vitest.

## Global Constraints

- Preserve existing keyword matching, shared reply hot updates, round-robin account selection, 19/minute cap, and two-second minimum interval.
- Do not add random DMs, new allowlists, new rate limits, version changes, or release notes.
- Only Telegram privacy/access errors count as a closed DM. FloodWait, timeout, proxy, and network failures do not.
- A successful send advances `next_delivery`; a closed DM consumes the DM turn even if its public fallback later fails.
- When global DM or the matched keyword's DM toggle is disabled, enqueue a public-only response and leave `next_delivery` unchanged.

---

### Task 1: Add the persistent delivery state and typed outcomes

**Files:**
- Modify: `internal/domain/types.go`
- Modify: `internal/domain/account.go`
- Modify: `internal/domain/job.go`
- Modify: `internal/domain/statistics.go`
- Modify: `internal/domain/delivery_errors.go`
- Test: `internal/domain/account_test.go`
- Create: `internal/domain/delivery_errors_test.go`

- [ ] Write failing domain tests for cursor defaults, cursor toggling, and `PrivateMessageClosed` error detection.
- [ ] Add `JobKeywordResponse JobType = "keyword_response"`.
- [ ] Add `DeliveryTarget` with `private` and `public`, `Account.NextDelivery`, and `Account.PrivateMessagesClosed`.
- [ ] Add `OutgoingMessageJob.AllowPrivate bool` so the matched keyword toggle is frozen into the durable job.
- [ ] Add `PrivateMessageClosed(err)` and `IsPrivateMessageClosed(err)` wrappers without stringifying credentials or peer data.
- [ ] Extend reply statistics totals/account rows with `PublicReplies`, `PrivateMessages`, and `PrivateMessagesClosed`.
- [ ] Run `go test ./internal/domain` on Ubuntu.
- [ ] Commit: `feat: define alternating keyword delivery outcomes`

Use these domain values:

```go
type DeliveryTarget string

const (
	DeliveryTargetPrivate DeliveryTarget = "private"
	DeliveryTargetPublic  DeliveryTarget = "public"
)

func (a Account) EffectiveNextDelivery() DeliveryTarget {
	if a.NextDelivery == DeliveryTargetPublic {
		return DeliveryTargetPublic
	}
	return DeliveryTargetPrivate
}
```

### Task 2: Migrate SQLite without losing existing queues or account settings

**Files:**
- Create: `internal/repository/sqlite/migrations/000015_delivery_alternation.sql`
- Modify: `internal/repository/sqlite/production_store.go`
- Modify: `internal/repository/sqlite/job_store.go`
- Modify: `internal/repository/sqlite/production_store_test.go`
- Modify: `internal/repository/sqlite/job_store_test.go`

- [ ] Write a migration test proving existing accounts and pending public/private jobs survive.
- [ ] Add `accounts.next_delivery TEXT NOT NULL DEFAULT 'private'` and `private_messages_closed INTEGER NOT NULL DEFAULT 0`.
- [ ] Add `outgoing_message_jobs.allow_private INTEGER NOT NULL DEFAULT 0` and rebuild its type check to accept `keyword_response` while preserving lease columns and indexes.
- [ ] Extend account scans/saves with both fields.
- [ ] Extend job enqueue/load with `allow_private`.
- [ ] Add transactional APIs `RecordPrivateClosed` and `CompleteKeywordResponse`.
- [ ] `RecordPrivateClosed` must insert one unsuccessful private event with error code `private_message_closed`, increment `private_messages_closed`, advance the account cursor to `public`, and set the claimed job to `allow_private=0` without completing it.
- [ ] `CompleteKeywordResponse` must mark the job done once, insert the actual successful send event, increment the correct account counter, and update `next_delivery` only for an alternating success.
- [ ] Prove duplicate completion is idempotent and never double increments counters.
- [ ] Run `go test ./internal/repository/sqlite` on Ubuntu.
- [ ] Commit: `feat: persist alternating delivery state atomically`

The repository boundary should be explicit:

```go
type KeywordDeliveryOutcome struct {
	JobID              domain.ID
	AccountID          domain.ID
	ChannelID          domain.ID
	PublicSent         bool
	PrivateSent        bool
	PrivateClosed      bool
	AdvanceTo          domain.DeliveryTarget
	CompletedAt        time.Time
}

type KeywordDeliveryRepository interface {
	RecordPrivateClosed(context.Context, domain.ID, domain.ID, time.Time) error
	CompleteKeywordResponse(context.Context, KeywordDeliveryOutcome) (bool, error)
}
```

### Task 3: Enqueue one response job per trigger

**Files:**
- Modify: `internal/telegram/gotd/live_sink.go`
- Modify: `internal/telegram/gotd/live_sink_test.go`

- [ ] Replace the two-job expectation with one `keyword_response` job test.
- [ ] Test public-only behavior when global DM is off, sender ID is absent, or the matched canonical keyword has DM disabled.
- [ ] Test that a DM-eligible trigger stores both `TargetTelegramID` and `AllowPrivate=true`.
- [ ] Keep the existing deterministic job ID, but derive it only from chat ID, message ID, and `keyword_response`.
- [ ] Confirm edited messages and historical collection still never enqueue sends.
- [ ] Run `go test ./internal/telegram/gotd -run LiveSink` on Ubuntu.
- [ ] Commit: `refactor: enqueue one durable keyword response`

### Task 4: Classify closed DMs at the Telegram boundary

**Files:**
- Modify: `internal/telegram/gotd/sender.go`
- Modify: `internal/telegram/gotd/sender_test.go`
- Modify: `internal/telegram/gotd/catalog_gateway.go`

- [ ] Add table-driven closed-DM tests for Telegram RPC types `USER_PRIVACY_RESTRICTED`, `USER_IS_BLOCKED`, and `YOU_BLOCKED_USER`.
- [ ] Add negative tests proving `PEER_ID_INVALID`, missing access hash/reference, `FLOOD_WAIT`, timeout, and proxy errors are not classified as closed DMs.
- [ ] Return `domain.PrivateMessageClosed(err)` for those DM-specific outcomes.
- [ ] Preserve `FloodWaitError` and transient wrapping for network/proxy/timeouts.
- [ ] Ensure logs and UI errors expose only a stable code such as `private_message_closed`, not Telegram peer details.
- [ ] Run `go test ./internal/telegram/gotd -run 'Sender|Private'` on Ubuntu.
- [ ] Commit: `fix: distinguish closed private messages from transport errors`

### Task 5: Execute alternating delivery in the scheduler

**Files:**
- Modify: `internal/usecase/scheduler.go`
- Modify: `internal/usecase/scheduler_test.go`
- Modify: `internal/telegram/gotd/outbound.go`
- Modify: `internal/telegram/gotd/outbound_test.go`

- [ ] Add failing tests for the sequence `DM -> public -> DM` independently for two round-robin accounts.
- [ ] Add restart coverage by reloading account state from SQLite between sends.
- [ ] Add closed-DM coverage: same account attempts DM, increments closed counter, sends public fallback, and next turn is public.
- [ ] Add failure coverage: FloodWait and transport failures delay the same claimed job and leave the cursor unchanged.
- [ ] Add public-only coverage: send public and leave the cursor unchanged.
- [ ] Add concurrent completion coverage proving two claimed jobs cannot consume the same account cursor or double-count one outcome.
- [ ] Make `Scheduler` choose delivery only after durable account claim:

```go
privateTurn := job.AllowPrivate && job.TargetTelegramID != "" &&
	account.EffectiveNextDelivery() == domain.DeliveryTargetPrivate
```

- [ ] On typed closed DM, atomically record/consume the DM turn, retarget the same claimed job to public-only, and delay it until the normal limiter allows its fallback. The persisted `account_id` guarantees the same account.
- [ ] Ensure one global rate-limit reservation applies per actual Telegram send; the delayed fallback must reserve separately and never bypass FloodWait.
- [ ] Run `go test ./internal/usecase ./internal/telegram/gotd` on Ubuntu.
- [ ] Commit: `feat: alternate keyword replies and private messages`

### Task 6: Extend statistics and account UI

**Files:**
- Modify: `internal/repository/sqlite/statistics_store.go`
- Modify: `internal/repository/sqlite/statistics_store_test.go`
- Modify: `internal/transport/wails/statistics_bindings.go`
- Modify: `internal/transport/wails/statistics_bindings_test.go`
- Modify: `internal/transport/wails/bindings.go`
- Modify: `internal/transport/wails/bindings_test.go`
- Modify: `frontend/src/accounts.ts`
- Modify: `frontend/src/stats.ts`
- Modify: `frontend/src/stats.test.ts`
- Modify: `frontend/src/StatsView.tsx`
- Modify: `frontend/src/App.tsx`
- Modify: `frontend/src/i18n.ts`
- Modify: `frontend/src/styles.css`

- [ ] Query successful public and private events separately; query closed-DM count from unsuccessful private events whose error code is exactly `private_message_closed` in the selected range.
- [ ] Do not count a failed DM or a closed-DM fallback as a successful private message.
- [ ] Add aggregate metrics and per-account columns for public replies, successful DMs, and closed DMs.
- [ ] Add `nextDelivery` and `privateMessagesClosed` to `AccountDTO` and `AccountRow`.
- [ ] Show each account's next target as `ЛС` or `Ответ` in `AccountsView`.
- [ ] Keep the current light/dark styling and make the new metrics use the existing statistics typography.
- [ ] Run `npm test -- --run stats accounts` and `npm run build` from `frontend`.
- [ ] Run `go test ./internal/repository/sqlite ./internal/transport/wails` on Ubuntu.
- [ ] Commit: `feat: report alternating delivery statistics`

### Task 7: Integration verification

**Files:**
- Modify only if verification exposes a defect.

- [ ] Run `go test ./...` and `go test -tags desktop ./...` on Ubuntu.
- [ ] Run `npm test` and `npm run build` from `frontend`.
- [ ] Build the Linux Wails app without changing version metadata.
- [ ] Use the existing test group with at most four trigger messages and verify one spammer account follows `DM/public/DM`; verify another account owns its own cursor.
- [ ] Verify a user with closed DMs increments `ЛС закрыто` and receives only the public fallback.
- [ ] Restart the app and verify the next target persists.
- [ ] Change an account role away from and back to spammer and verify its next target persists.
- [ ] Confirm shared reply changes are used without STOP/START.
- [ ] Commit any verification-only fix separately; otherwise leave the verified commits unchanged.
