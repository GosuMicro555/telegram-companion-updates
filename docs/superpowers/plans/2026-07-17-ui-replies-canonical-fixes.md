# UI, Replies, and Canonical Trigger Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Исправить общие UI-дефекты статистики, добавить отдельные ответы для комментариев и ЛС и восстановить срабатывание активных канонов по всем связанным формам.

**Architecture:** Общие элементы статистики остаются переиспользуемыми React-компонентами. Настройки ответов проходят через существующий JSON store, Wails DTO и горячий `runtimeconfig.Snapshot`; выбор текста выполняется непосредственно перед Telegram-отправкой. Канонический триггер проверяется сквозным тестом от сохранённого канона и форм до постановки исходящего задания.

**Tech Stack:** Go 1.25+, Wails, React 19, TypeScript 7, Vitest, SQLite, gotd/MTProto.

## Global Constraints

- Версия приложения остаётся `0.5.0`; release notes не добавляются.
- Не добавлять frontend-зависимости.
- Сохранить текущие аккаунты, tdata-сессии, роли, каналы, прокси, keyword и статистику.
- Публичный ответ и ответ в ЛС обновляются без `STOP/START`.
- Активный позитивный канон срабатывает на канон и каждую привязанную форму как на отдельное нормализованное слово.
- Светлая тема остаётся темой по умолчанию; календарь поддерживает обе существующие темы.

---

### Task 1: Общие UI-исправления таблиц и аналитики

**Files:**
- Modify: `frontend/src/ConfigurableStatsTable.test.tsx`
- Modify: `frontend/src/AnalyticsView.test.tsx`
- Modify: `frontend/src/StatsView.test.tsx`
- Modify: `frontend/src/styles.css`
- Modify: `frontend/src/AnalyticsView.tsx`

**Interfaces:**
- Consumes: `createColumnMenuDismissal`, существующие CSS-классы `configurableStatsTable__columnMenu` и `releaseNotesPage`.
- Produces: скрываемое меню столбцов и `formatNextRun(value, locale): string`, возвращающий `—` для пустого значения.

- [ ] **Step 1: Write failing regression tests**

Добавить проверки CSS-правила скрытия, отсутствия локальных release-note font overrides и пустого следующего запуска:

```ts
expect(css).toMatch(/\.configurableStatsTable__columnMenu\[hidden\]\s*\{[^}]*display:\s*none/);
expect(css).not.toContain(".releaseNotesPage h2 {");
expect(css).not.toContain(".releaseNoteEntry ul {");
expect(formatNextRun("", "ru")).toBe("—");
```

- [ ] **Step 2: Verify RED**

Run: `npm test -- ConfigurableStatsTable.test.tsx StatsView.test.tsx AnalyticsView.test.tsx` from `frontend`.
Expected: FAIL because hidden is overridden, typography overrides remain, and empty next run renders `никогда`.

- [ ] **Step 3: Implement minimal fixes**

Add the explicit hidden selector, remove only the two recent typography override blocks, and change only `formatNextRun` empty output:

```css
.configurableStatsTable__columnMenu[hidden] { display: none; }
```

```ts
export function formatNextRun(value: string, locale: Locale): string {
  return value ? formatDateTime(value, locale) : "—";
}
```

- [ ] **Step 4: Verify GREEN**

Run the same focused Vitest command. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/ConfigurableStatsTable.test.tsx frontend/src/AnalyticsView.test.tsx frontend/src/StatsView.test.tsx frontend/src/styles.css frontend/src/AnalyticsView.tsx
git commit -m "fix: restore shared statistics UI behavior"
```

### Task 2: Общий календарь приложения

**Files:**
- Create: `frontend/src/DateTimeField.tsx`
- Create: `frontend/src/DateTimeField.test.tsx`
- Modify: `frontend/src/LiveStatisticsView.tsx`
- Modify: `frontend/src/StatsView.tsx`
- Modify: `frontend/src/i18n.ts`
- Modify: `frontend/src/styles.css`

**Interfaces:**
- Consumes: controlled value `YYYY-MM-DDTHH:mm`, `Locale` and existing theme variables.
- Produces: `DateTimeField({ value, onChange, label, locale })` without native `datetime-local` popup.

- [ ] **Step 1: Write failing component tests**

```tsx
const markup = renderToStaticMarkup(<DateTimeField value="2026-07-17T12:30" onChange={() => {}} label="Дата" locale="ru" />);
expect(markup).not.toContain('type="datetime-local"');
expect(markup).toContain("17.07.2026, 12:30");
```

Add DOM tests for opening, selecting a day, changing hours/minutes, applying, clearing, outside click and `Escape`.

- [ ] **Step 2: Verify RED**

Run: `npm test -- DateTimeField.test.tsx` from `frontend`.
Expected: FAIL because `DateTimeField` does not exist.

- [ ] **Step 3: Implement the controlled component**

Use local draft date, a month grid derived from `new Date(year, month, 1)`, numeric hour/minute controls, and document listeners active only while open. Emit only valid `YYYY-MM-DDTHH:mm` values from Apply and `""` from Clear.

- [ ] **Step 4: Replace both native ranges and style the popover**

Replace the four `datetime-local` inputs in `LiveStatisticsView.tsx` and `StatsView.tsx`. Add compact thin-border styles based on existing `--surface`, `--border`, `--text`, and accent variables.

- [ ] **Step 5: Verify GREEN and integration**

Run: `npm test -- DateTimeField.test.tsx LiveStatisticsView.test.tsx StatsView.test.tsx`.
Expected: PASS and no rendered `type="datetime-local"`.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/DateTimeField.tsx frontend/src/DateTimeField.test.tsx frontend/src/LiveStatisticsView.tsx frontend/src/StatsView.tsx frontend/src/i18n.ts frontend/src/styles.css
git commit -m "feat: add shared themed date time picker"
```

### Task 3: Два независимых текста ответа

**Files:**
- Modify: `internal/domain/settings.go`
- Modify: `internal/usecase/runtimeconfig/store.go`
- Modify: `internal/repository/localdb/settings_store.go`
- Modify: `internal/repository/localdb/settings_store_test.go`
- Modify: `internal/repository/sqlite/catalog_store.go`
- Modify: `internal/repository/sqlite/catalog_store_test.go`
- Modify: `internal/transport/wails/bindings.go`
- Modify: `internal/transport/wails/bindings_test.go`
- Modify: `internal/telegram/gotd/outbound.go`
- Modify: `internal/telegram/gotd/outbound_test.go`
- Modify: `frontend/src/keywordSettingsSaveQueue.ts`
- Modify: `frontend/src/App.tsx`
- Modify: `frontend/src/App.test.tsx`
- Modify: `frontend/src/i18n.ts`

**Interfaces:**
- Consumes: existing `SharedReply` / `sharedReply` as public reply.
- Produces: `PrivateReply string` / `privateReply`, defaulted from public reply only when the field is absent in legacy JSON.

- [ ] **Step 1: Write failing persistence and runtime tests**

Create test settings with `SharedReply: "comment"` and `PrivateReply: "private"`; assert Bolt and production SQLite save/load, DTO round trip, hot runtime publication and snapshot cloning preserve both independently.

- [ ] **Step 2: Verify backend RED**

Run: `go test ./internal/repository/localdb ./internal/repository/sqlite ./internal/transport/wails ./internal/usecase/runtimeconfig`.
Expected: FAIL because `PrivateReply` does not exist.

- [ ] **Step 3: Add the field through all settings layers**

Add `PrivateReply string json:"privateReply"` to domain, DTO and runtime snapshot. Detect absence of the JSON key during legacy load and copy `SharedReply`; do not overwrite an explicitly saved empty string.

- [ ] **Step 4: Write failing outbound selection tests**

```go
snapshot := runtimeconfig.Snapshot{SharedReply: "comment", PrivateReply: "private", RateLimits: validLimits}
require.NoError(t, sender.SendPublicReply(ctx, account, job))
require.Equal(t, "comment", api.publicJobs[0].Text)
require.NoError(t, sender.SendPrivateMessage(ctx, account, job))
require.Equal(t, "private", api.privateJobs[0].Text)
```

- [ ] **Step 5: Implement send-time selection**

In `Outbound.send`, assign `snapshot.PrivateReply` when `private == true`, otherwise `snapshot.SharedReply`. Return a permanent no-op delivery error for an empty selected reply so Telegram never receives an empty message; scheduler fallback from a closed DM calls `SendPublicReply` and therefore selects the public reply.

- [ ] **Step 6: Add failing frontend settings tests**

Assert `KeywordSettingsSnapshot` carries both values and KeywordsView renders labels «Единый ответ в комментариях» and «Единый ответ в ЛС».

- [ ] **Step 7: Implement two compact controlled inputs**

Keep public and private reply states independent, enqueue both in the existing latest-save queue, and hydrate both from authoritative settings after save or reload.

- [ ] **Step 8: Verify GREEN**

Run backend packages above plus `go test ./internal/telegram/gotd`; run `npm test -- App.test.tsx`.
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal frontend/src
git commit -m "feat: separate comment and private replies"
```

### Task 4: Канонические триггеры и синхронизация Live-статистики

**Files:**
- Modify: `internal/telegram/gotd/live_sink_test.go`
- Modify: `internal/telegram/gotd/live_sink.go`
- Modify: `internal/transport/wails/bindings_test.go`
- Modify: `internal/transport/wails/bindings.go`
- Modify: `frontend/src/LiveStatisticsView.test.tsx`
- Modify: `frontend/src/LiveStatisticsView.tsx`
- Modify: `frontend/src/App.test.tsx`
- Modify: `frontend/src/App.tsx`

**Interfaces:**
- Consumes: active positive canonical row `{ canonical: "тест", forms: ["тест", "тестик"] }`.
- Produces: one queued keyword-response job for either form and authoritative frontend keyword refresh after deletion.

- [ ] **Step 1: Add a failing stored-canonical integration test**

Build bindings with an active positive `тест` canonical and `тестик` form, call `SyncCanonicalTriggers`, then feed separate incoming updates containing `тест` and `тестик` through `LiveSink.Trigger`. Assert both enqueue jobs with the canonical ID and snapshot `тест`.

- [ ] **Step 2: Verify RED or expose the operational boundary**

Run: `go test ./internal/transport/wails ./internal/telegram/gotd -run 'Canonical|Trigger' -count=1`.
Expected: the new test must fail at the first broken boundary. If it passes, extend the same test through active outbound catalog resolution and queue deduplication until it reproduces the missing job; do not alter production code before a failing assertion exists.

- [ ] **Step 3: Fix the first broken boundary minimally**

Preserve exact-token matching. Ensure trigger synchronization includes canonical plus every stored form and is invoked after canonical activation, settings save and startup runtime hydration. Ensure an active discussion catalog entry resolves both raw and namespaced Telegram chat IDs consistently.

- [ ] **Step 4: Add failing frontend deletion refresh test**

Assert successful `DeleteLiveTrigger` calls an `onTriggerDeleted` callback; in App, that callback reloads `GetKeywordSettings()` and replaces keyword rows from authoritative data.

- [ ] **Step 5: Implement authoritative refresh**

Pass `onTriggerDeleted` from App to LiveStatisticsView. After backend success, reload settings before allowing another local save, update public/private replies and keyword rows, and advance hydration revision.

- [ ] **Step 6: Verify GREEN**

Run focused Go tests and `npm test -- LiveStatisticsView.test.tsx App.test.tsx`.
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/telegram/gotd internal/transport/wails frontend/src/LiveStatisticsView.tsx frontend/src/LiveStatisticsView.test.tsx frontend/src/App.tsx frontend/src/App.test.tsx
git commit -m "fix: restore canonical trigger delivery"
```

### Task 5: Полная проверка, сборка и установка

**Files:**
- Verify only: entire repository
- Update only if generated by normal build: Wails bindings/assets already tracked by project conventions

**Interfaces:**
- Consumes: Tasks 1–4.
- Produces: verified Ubuntu desktop binary at the existing installed launcher path.

- [ ] **Step 1: Run all frontend checks**

Run: `npm test` and `npm run build` from `frontend`.
Expected: all tests PASS and TypeScript/Vite build succeeds.

- [ ] **Step 2: Run all Go checks**

Run: `go test ./...`.
Expected: all packages PASS.

- [ ] **Step 3: Build Wails binary**

Run the repository's existing Wails build command used by the Ubuntu installer.
Expected: `build/bin/telegram-companion` exists and is executable.

- [ ] **Step 4: Install and restart in Ubuntu**

Copy the committed source to `/home/codex/projects`, build there, install to `/home/codex/.local/opt/telegram-companion/telegram-companion`, and launch through the existing desktop helper. Confirm exactly one installed process is running and desktop/dock launchers point to that path.

- [ ] **Step 5: Verify persisted runtime state**

Restart once and confirm logs show loaded accounts, active canonical triggers and no settings decode errors. Do not send Telegram test traffic unless necessary to reproduce a remaining trigger failure.

- [ ] **Step 6: Commit only necessary final corrections**

```bash
git status --short
git diff --check
```

Expected: clean worktree after any final correction commit.
