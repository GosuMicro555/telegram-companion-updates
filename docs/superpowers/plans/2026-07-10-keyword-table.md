# Compact Keyword Table Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add page scrolling, a dense editable keyword table with deletion, and a restrained running-state shimmer on the START/STOP control.

**Architecture:** Keep persistence in the existing `KeywordsView` debounced save flow. Put deterministic rename/delete rules in `keywords.ts`, render row-level interaction in a focused `KeywordTable.tsx` component, and keep layout/animation in the existing stylesheet.

**Tech Stack:** React 19, TypeScript, Vitest, Lucide React, CSS, Wails v2, Go 1.25+

## Global Constraints

- Russian remains the primary UI language, with matching English translations.
- Support up to 1000 shared keywords.
- Preserve the full label `Личные сообщения`.
- Persist every successful edit, delete, and toggle through the existing local database flow.
- Disable decorative motion under `prefers-reduced-motion: reduce`.

---

### Task 1: Keyword Mutation Rules

**Files:**
- Modify: `frontend/src/keywords.ts`
- Modify: `frontend/src/keywords.test.ts`

**Interfaces:**
- Produces: `renameKeyword(rows: KeywordRow[], currentKeyword: string, nextKeyword: string): KeywordMutationResult`
- Produces: `removeKeyword(rows: KeywordRow[], keyword: string): KeywordRow[]`
- Produces: `KeywordMutationResult = { rows: KeywordRow[]; error: "empty" | "duplicate" | null }`

- [ ] **Step 1: Write failing rename and delete tests**

```ts
test("renameKeyword trims and renames one keyword", () => {
  const rows = [{ keyword: "тест1", dm: true }];
  expect(renameKeyword(rows, "тест1", "  новый ключ  ")).toEqual({
    rows: [{ keyword: "новый ключ", dm: true }],
    error: null
  });
});

test("renameKeyword rejects empty and duplicate values", () => {
  const rows = [
    { keyword: "тест1", dm: true },
    { keyword: "тест2", dm: false }
  ];
  expect(renameKeyword(rows, "тест1", " ").error).toBe("empty");
  expect(renameKeyword(rows, "тест1", "ТЕСТ2").error).toBe("duplicate");
});

test("removeKeyword removes only the selected row", () => {
  const rows = [
    { keyword: "тест1", dm: true },
    { keyword: "тест2", dm: false }
  ];
  expect(removeKeyword(rows, "тест1")).toEqual([{ keyword: "тест2", dm: false }]);
});
```

- [ ] **Step 2: Run the focused test and verify RED**

Run: `cd frontend && npm test -- --run src/keywords.test.ts`

Expected: FAIL because `renameKeyword` and `removeKeyword` are not exported.

- [ ] **Step 3: Implement minimal immutable mutation helpers**

```ts
export type KeywordMutationResult = {
  rows: KeywordRow[];
  error: "empty" | "duplicate" | null;
};

export function renameKeyword(rows: KeywordRow[], currentKeyword: string, nextKeyword: string): KeywordMutationResult {
  const keyword = nextKeyword.trim();
  if (!keyword) return { rows, error: "empty" };
  const normalized = keyword.toLocaleLowerCase("ru-RU");
  const duplicate = rows.some((row) =>
    row.keyword !== currentKeyword && row.keyword.toLocaleLowerCase("ru-RU") === normalized
  );
  if (duplicate) return { rows, error: "duplicate" };
  return {
    rows: rows.map((row) => row.keyword === currentKeyword ? { ...row, keyword } : row),
    error: null
  };
}

export function removeKeyword(rows: KeywordRow[], keyword: string): KeywordRow[] {
  return rows.filter((row) => row.keyword !== keyword);
}
```

- [ ] **Step 4: Run the focused test and verify GREEN**

Run: `cd frontend && npm test -- --run src/keywords.test.ts`

Expected: all keyword helper tests pass.

### Task 2: Compact Inline Keyword Table

**Files:**
- Create: `frontend/src/KeywordTable.tsx`
- Modify: `frontend/src/App.tsx`
- Modify: `frontend/src/i18n.ts`

**Interfaces:**
- Consumes: `renameKeyword`, `removeKeyword`, `KeywordRow`, `Locale`
- Produces: `KeywordTable({ rows, visibleRows, locale, onRowsChange, onToggleDm })`

- [ ] **Step 1: Add Russian and English copy**

Add message keys for `actions`, `edit`, `delete`, `save`, `confirmDelete`, `emptyKeyword`, and `duplicateKeyword`. Russian action labels use `Действия`, `Изменить`, `Удалить`, `Сохранить`, and `Подтвердить удаление`.

- [ ] **Step 2: Implement the focused table component**

Create a component with `editingKeyword`, `editDraft`, and `deletingKeyword` state. Use Lucide `Pencil`, `Trash2`, `Check`, and `X` icons. The edit action swaps only the keyword cell for an input; save calls `renameKeyword`; delete requires a second inline confirmation click; successful mutations call `onRowsChange` so the existing database autosave runs.

- [ ] **Step 3: Replace the existing keyword rows in `KeywordsView`**

```tsx
<KeywordTable
  rows={keywordRows}
  visibleRows={filteredKeywords}
  locale={locale}
  onRowsChange={setKeywordRows}
  onToggleDm={toggleDm}
/>
```

Pass the complete source rows to mutation operations so edits made while search is active do not discard hidden keywords. Remove the repeated shared reply from every row.

- [ ] **Step 4: Compile TypeScript**

Run: `cd frontend && npm run build`

Expected: TypeScript and Vite complete with exit code 0.

### Task 3: Page Scroll, Dense Rows, And Running Shimmer

**Files:**
- Modify: `frontend/src/styles.css`

**Interfaces:**
- Consumes: existing `.shell`, `.content`, `.keywordPanel`, `.keywordsGrid`, `.startButton.stop`
- Produces: viewport-owned content scroll and motion-safe running animation

- [ ] **Step 1: Make the right content area scroll**

Set `.shell` to `height: 100vh`, `.sidebar` to `min-height: 0`, and `.content` to `min-width: 0; min-height: 0; overflow-y: auto`. Remove the nested `max-height` and `overflow: auto` from `.keywordPanel`.

- [ ] **Step 2: Compact the keyword grid**

Use `grid-template-columns: minmax(320px, 1fr) 190px 128px`, set keyword rows to about `42px`, reduce vertical padding, and add stable 32px icon buttons. Keep the complete `Личные сообщения` header on one line.

- [ ] **Step 3: Add the running shimmer**

Use `.startButton.stop::after` with a translucent diagonal highlight animated across the existing red/orange background. Keep `overflow: hidden` and stable button dimensions. Under `@media (prefers-reduced-motion: reduce)`, set animation to `none`.

- [ ] **Step 4: Run all frontend tests and build**

Run: `cd frontend && npm test -- --run`

Expected: all Vitest suites pass.

Run: `cd frontend && npm run build`

Expected: Vite production build succeeds.

### Task 4: Desktop Acceptance

**Files:**
- Verify: `build/bin/telegram-companion`

**Interfaces:**
- Consumes: Tasks 1-3
- Produces: rebuilt Ubuntu desktop application

- [ ] **Step 1: Run Go regression tests**

Run: `/usr/local/go/bin/go test -tags desktop ./...`

Expected: all Go packages pass.

- [ ] **Step 2: Build Wails**

Run: `PATH=/usr/local/go/bin:/home/codex/go/bin:/usr/local/bin:/usr/bin:/bin /home/codex/go/bin/wails build -clean -skipbindings -tags "desktop webkit2_41"`

Expected: `build/bin/telegram-companion` is produced.

- [ ] **Step 3: Restart and visually verify**

Restart the desktop app, open `Ключевые слова`, verify full-page scrolling, compact table rows, edit/save/cancel, inline delete confirmation, persistence after reopening the tab, and the animated running button. Leave automation running after verification.
