import { renderToStaticMarkup } from "react-dom/server";
import { readFileSync } from "node:fs";
import type { ReactNode } from "react";
import { expect, test, vi } from "vitest";
import * as configurableStatsTableComponent from "./ConfigurableStatsTableComponent";
import { ConfigurableStatsTable, createPointerResizeSession } from "./ConfigurableStatsTableComponent";
import {
  cycleConfigurableTableSort,
  tablePreferencesFromDefinitions,
  toggleTableColumnVisibility,
  type ConfigurableStatsTablePreferences,
  type ConfigurableTableSort,
  type StorageLike
} from "./configurableStatsTable";

type Row = { id: string; name: string; replies: number };

const definitions = [
  { id: "name", label: "Name", width: 220, sortable: true, initialSortDirection: "asc" as const, cell: (row: Row) => <strong>{row.name}</strong> },
  { id: "replies", label: "Replies", width: 96, sortable: true, cell: (row: Row) => row.replies.toLocaleString("en-US") }
] as const;

test("renders accessible configurable column controls with one shared grid template", () => {
  const markup = renderToStaticMarkup(
    <ConfigurableStatsTable
      definitions={definitions}
      columnMenuLabel="Column choices"
      emptyState="No rows"
      onSortChange={() => undefined}
      pagination={{ label: "Account reply pages", pageLabel: "Page 1 of 1", previousLabel: "Previous page", nextLabel: "Next page", onPrevious: () => undefined, onNext: () => undefined, previousDisabled: true, nextDisabled: true }}
      rowKey={(row) => row.id}
      rows={[{ id: "a", name: "Alpha", replies: 14 }]}
      settingsKey="stats.accounts.table.v1"
      sort={{ column: "name", direction: "asc" }}
      tableLabel="Account replies"
    />
  );
  const templates = [...markup.matchAll(/grid-template-columns:([^";]+)/g)].map((match) => match[1]);
  const nameResizeHandle = markup.match(/<span\b[^>]*aria-label="Resize Name"[^>]*>/)?.[0] ?? "";

  expect(markup).toContain('role="grid"');
  expect(markup).toContain('aria-label="Account replies"');
  expect(markup).toContain('aria-label="Column choices"');
  expect(markup).toContain('role="menu"');
  expect(markup).toContain('aria-label="Reorder Name"');
  expect(markup).toContain('draggable="true"');
  expect(markup).toContain('aria-label="Resize Name"');
  expect(markup).toContain('role="separator"');
  expect(nameResizeHandle).toContain('aria-valuenow="220"');
  expect(nameResizeHandle).toContain('aria-valuemin="48"');
  expect(nameResizeHandle).toContain('aria-valuemax="900"');
  expect(markup).toContain('aria-sort="ascending"');
  expect(markup).toContain('aria-sort="none"');
  expect(markup).toContain('aria-label="Previous page"');
  expect(markup).toContain('aria-label="Next page"');
  expect(templates).toEqual(["220px 96px", "220px 96px"]);
});

test("applies a caller-provided class only to matching rows", () => {
  const markup = renderToStaticMarkup(
    <ConfigurableStatsTable
      definitions={definitions}
      columnMenuLabel="Column choices"
      emptyState="No rows"
      onSortChange={() => undefined}
      rowClassName={(row) => row.id === "b" ? "configurableStatsTable__row--new" : undefined}
      rowKey={(row) => row.id}
      rows={[{ id: "a", name: "Alpha", replies: 14 }, { id: "b", name: "Beta", replies: 21 }]}
      settingsKey="stats.accounts.table.v1"
      sort={{ column: "name", direction: "asc" }}
      tableLabel="Account replies"
    />
  );

  expect(markup.match(/configurableStatsTable__row--new/g)).toHaveLength(1);
  expect(markup).toContain('class="configurableStatsTable__grid configurableStatsTable__row configurableStatsTable__row--new"');
});

test("renders an accessible expansion at the effective visible grid width", () => {
  const preferences = { order: ["replies", "name"], visible: ["replies"], widths: { name: 220, replies: 144 } };
  const values = new Map([["stats.accounts.table.v1", JSON.stringify(preferences)]]);
  const storage = {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value)
  };
  const markup = renderToStaticMarkup(
    <ConfigurableStatsTable
      definitions={definitions}
      columnMenuLabel="Column choices"
      emptyState="No rows"
      onSortChange={() => undefined}
      renderAfterRow={(row) => row.id === "a" ? <span>Alpha detail</span> : null}
      rowKey={(row) => row.id}
      rows={[{ id: "a", name: "Alpha", replies: 14 }, { id: "b", name: "Beta", replies: 21 }]}
      settingsKey="stats.accounts.table.v1"
      sort={{ column: "replies", direction: "desc" }}
      storage={storage}
      tableLabel="Account replies"
    />
  );
  const templates = [...markup.matchAll(/grid-template-columns:([^";]+)/g)].map((match) => match[1]);

  expect(templates).toEqual(["144px", "144px", "144px"]);
  expect(markup).toContain('aria-label="Resize Replies"');
  expect(markup).toContain('aria-valuenow="144"');
  expect(markup).toContain('aria-sort="descending"');
  expect(markup.match(/role="columnheader"/g)).toHaveLength(1);
  expect(markup).toContain('class="configurableStatsTable__expandedRow" role="row" style="width:144px"');
  expect(markup).toContain('<div role="gridcell" aria-colspan="1"><span>Alpha detail</span></div>');
  expect(markup.indexOf("Alpha detail")).toBeGreaterThan(markup.indexOf('<div role="gridcell">14</div>'));
  expect(markup.indexOf("Alpha detail")).toBeLessThan(markup.indexOf('<div role="gridcell">21</div>'));
});

type InteractionControllerFactory = <Row, ColumnID extends string>(options: {
  defaults: ConfigurableStatsTablePreferences<ColumnID>;
  preferences: ConfigurableStatsTablePreferences<ColumnID>;
  sort: ConfigurableTableSort<ColumnID>;
  settingsKey: string;
  storage?: StorageLike;
  onPreferencesChange(preferences: ConfigurableStatsTablePreferences<ColumnID>): void;
  onSortChange(sort: ConfigurableTableSort<ColumnID>): void;
  renderAfterRow?: (row: Row) => ReactNode;
}) => {
  commitResize(): void;
  getPreferences(): ConfigurableStatsTablePreferences<ColumnID>;
  renderAfterRow(row: Row): ReactNode;
  reorder(source: ColumnID, target: ColumnID): void;
  resize(column: ColumnID, width: number): void;
  sort(column: ColumnID, initialDirection?: "asc" | "desc"): void;
  toggleVisibility(column: ColumnID): void;
};

test("keeps expanded-table interactions and persistence on one controller state", () => {
  const createController = (configurableStatsTableComponent as {
    createConfigurableStatsTableInteractionController?: InteractionControllerFactory;
  }).createConfigurableStatsTableInteractionController;
  expect(createController).toBeTypeOf("function");
  if (!createController) return;

  type ColumnID = "name" | "replies";
  const defaults: ConfigurableStatsTablePreferences<ColumnID> = {
    order: ["name", "replies"],
    visible: ["name", "replies"],
    widths: { name: 220, replies: 96 }
  };
  let currentPreferences = defaults;
  let currentSort: ConfigurableTableSort<ColumnID> = { column: "name", direction: "asc" };
  const values = new Map<string, string>();
  const controller = createController<Row, ColumnID>({
    defaults,
    preferences: currentPreferences,
    sort: currentSort,
    settingsKey: "stats.accounts.table.v1",
    storage: {
      getItem: (key) => values.get(key) ?? null,
      setItem: (key, value) => values.set(key, value)
    },
    onPreferencesChange: (preferences) => { currentPreferences = preferences; },
    onSortChange: (sort) => { currentSort = sort; },
    renderAfterRow: (row) => row.id === "a" ? <span>{row.name} detail</span> : null
  });

  expect(renderToStaticMarkup(<>{controller.renderAfterRow({ id: "a", name: "Alpha", replies: 14 })}</>)).toContain("Alpha detail");
  controller.reorder("replies", "name");
  expect(currentPreferences.order).toEqual(["replies", "name"]);
  expect(JSON.parse(values.get("stats.accounts.table.v1") ?? "null").order).toEqual(["replies", "name"]);
  controller.toggleVisibility("name");
  expect(currentPreferences.visible).toEqual(["replies"]);
  expect(JSON.parse(values.get("stats.accounts.table.v1") ?? "null")).toEqual(currentPreferences);

  controller.resize("replies", 143.6);
  expect(currentPreferences.widths.replies).toBe(144);
  expect(JSON.parse(values.get("stats.accounts.table.v1") ?? "null").widths.replies).toBe(96);
  controller.commitResize();
  expect(JSON.parse(values.get("stats.accounts.table.v1") ?? "null").widths.replies).toBe(144);

  controller.sort("replies", "desc");
  expect(currentSort).toEqual({ column: "replies", direction: "desc" });
  expect(controller.getPreferences()).toEqual(currentPreferences);
});

test("keeps an expanded row at least as wide as the table body", () => {
  const css = readFileSync(new URL("./styles.css", import.meta.url), "utf8");

  expect(css).toMatch(/\.configurableStatsTable__expandedRow\s*\{[^}]*min-width:\s*100%;/s);
});

test("hides the closed column menu while keeping its dismissal contract", () => {
  const markup = renderToStaticMarkup(
    <ConfigurableStatsTable
      definitions={definitions}
      columnMenuLabel="Column choices"
      emptyState="No rows"
      onSortChange={() => undefined}
      rowKey={(row) => row.id}
      rows={[]}
      settingsKey="stats.accounts.table.v1"
      sort={{ column: "name", direction: "asc" }}
      tableLabel="Account replies"
    />
  );
  const css = readFileSync(new URL("./styles.css", import.meta.url), "utf8");

  expect(markup).toContain('class="configurableStatsTable__columnMenu" hidden=""');
  expect(css).toMatch(/\.configurableStatsTable__columnMenu\[hidden\]\s*\{\s*display:\s*none;\s*\}/s);
});

test("cycles generic sort state and keeps one visible column", () => {
  expect(cycleConfigurableTableSort({ column: "replies", direction: "desc" }, "name", "asc")).toEqual({ column: "name", direction: "asc" });
  expect(cycleConfigurableTableSort({ column: "name", direction: "asc" }, "name", "asc")).toEqual({ column: "name", direction: "desc" });

  const preferences = tablePreferencesFromDefinitions(definitions);
  expect(toggleTableColumnVisibility(preferences, "replies").visible).toEqual(["name"]);
  expect(toggleTableColumnVisibility({ ...preferences, visible: ["name"] }, "name").visible).toEqual(["name"]);
});

test("cleans up an active pointer resize when the table unmounts", () => {
  let moveListener: ((event: PointerEvent) => void) | undefined;
  let upListener: ((event: PointerEvent) => void) | undefined;
  const target = {
    addEventListener: vi.fn((type: string, listener: (event: PointerEvent) => void) => {
      if (type === "pointermove") moveListener = listener;
      if (type === "pointerup") upListener = listener;
    }),
    removeEventListener: vi.fn()
  };
  const update = vi.fn();
  const commit = vi.fn();
  const unmount = createPointerResizeSession(target, update, commit);

  moveListener?.({ clientX: 140 } as PointerEvent);
  expect(update).toHaveBeenCalledOnce();

  unmount();
  moveListener?.({ clientX: 180 } as PointerEvent);
  upListener?.({ clientX: 180 } as PointerEvent);

  expect(update).toHaveBeenCalledOnce();
  expect(commit).not.toHaveBeenCalled();
  expect(target.removeEventListener).toHaveBeenCalledWith("pointermove", moveListener);
  expect(target.removeEventListener).toHaveBeenCalledWith("pointerup", upListener);
});

test("dismisses the shared column menu on an outside pointer press or Escape", () => {
  type ColumnMenuDismissal = (
    target: {
      addEventListener(type: string, listener: EventListener): void;
      removeEventListener(type: string, listener: EventListener): void;
    },
    root: { contains(target: Node | null): boolean },
    dismiss: () => void
  ) => () => void;
  const createColumnMenuDismissal = (configurableStatsTableComponent as {
    createColumnMenuDismissal?: ColumnMenuDismissal;
  }).createColumnMenuDismissal;
  expect(createColumnMenuDismissal).toBeTypeOf("function");
  if (!createColumnMenuDismissal) return;

  const listeners = new Map<string, EventListener>();
  const target = {
    addEventListener: vi.fn((type: string, listener: EventListener) => listeners.set(type, listener)),
    removeEventListener: vi.fn((type: string) => listeners.delete(type))
  };
  const inside = {} as Node;
  const outside = {} as Node;
  const dismiss = vi.fn();
  const dispose = createColumnMenuDismissal(target, { contains: (node) => node === inside }, dismiss);

  listeners.get("pointerdown")?.({ target: inside } as unknown as Event);
  expect(dismiss).not.toHaveBeenCalled();
  listeners.get("pointerdown")?.({ target: outside } as unknown as Event);
  expect(dismiss).toHaveBeenCalledOnce();
  listeners.get("keydown")?.({ key: "Escape" } as KeyboardEvent);
  expect(dismiss).toHaveBeenCalledTimes(2);
  listeners.get("keydown")?.({ key: "Enter" } as KeyboardEvent);
  expect(dismiss).toHaveBeenCalledTimes(2);

  dispose();
  expect(target.removeEventListener).toHaveBeenCalledWith("pointerdown", expect.any(Function));
  expect(target.removeEventListener).toHaveBeenCalledWith("keydown", expect.any(Function));
});
