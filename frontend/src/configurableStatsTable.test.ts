import { describe, expect, test } from "vitest";
import {
  clampTableColumnWidth,
  loadTablePreferences,
  normalizeTablePreferences,
  reorderTableColumns,
  saveTablePreferences,
  tablePreferencesFromDefinitions,
  type StorageLike
} from "./configurableStatsTable";

class MemoryStorage implements StorageLike {
  private readonly values = new Map<string, string>();

  getItem(key: string): string | null { return this.values.get(key) ?? null; }
  setItem(key: string, value: string): void { this.values.set(key, value); }
}

const accountDefaults = tablePreferencesFromDefinitions([
  { id: "kind", label: "Type", width: 86 },
  { id: "name", label: "Name", width: 260 },
  { id: "replies", label: "Replies", width: 120 }
]);

const liveDefaults = tablePreferencesFromDefinitions([
  { id: "account", label: "Account", width: 260 },
  { id: "sent", label: "Sent", width: 120 }
]);

describe("configurable statistics table preferences", () => {
  test("keeps independent order, visibility and width per settings key", () => {
    const storage = new MemoryStorage();
    saveTablePreferences(storage, "stats.accounts.table.v1", { order: ["name"], visible: ["name"], widths: { name: 320 } });

    expect(loadTablePreferences(storage, "stats.live.table.v1", liveDefaults)).toEqual(liveDefaults);
    expect(loadTablePreferences(storage, "stats.accounts.table.v1", accountDefaults)).toEqual({
      order: ["name", "kind", "replies"],
      visible: ["name"],
      widths: { kind: 86, name: 320, replies: 120 }
    });
  });

  test("rejects unknown column ids while restoring missing defaults", () => {
    expect(normalizeTablePreferences({
      order: ["replies", "unknown"],
      visible: ["unknown", "replies"],
      widths: { replies: 200, unknown: 700 }
    }, accountDefaults)).toEqual({
      order: ["replies", "kind", "name"],
      visible: ["replies"],
      widths: { kind: 86, name: 260, replies: 200 }
    });
  });

  test("reorders a known column without losing any definitions", () => {
    expect(reorderTableColumns(accountDefaults.order, "replies", "kind")).toEqual(["replies", "kind", "name"]);
  });

  test("clamps all saved widths to the exact supported range", () => {
    expect(clampTableColumnWidth(4)).toBe(48);
    expect(clampTableColumnWidth(960)).toBe(900);
  });

  test("restores defaults when persisted preferences contain corrupt JSON", () => {
    const storage = new MemoryStorage();
    storage.setItem("stats.accounts.table.v1", "{not json");

    expect(loadTablePreferences(storage, "stats.accounts.table.v1", accountDefaults)).toEqual(accountDefaults);
  });
});
