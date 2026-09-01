import { describe, expect, test, vi } from "vitest";
import {
  createAnalyticsTablePreferenceSaveQueue,
  defaultAnalyticsTablePreferences,
  normalizeAnalyticsTablePreferences,
  reorderAnalyticsColumns,
  resolveDraggedAnalyticsColumn,
  toAnalyticsTablePreference,
  withAnalyticsTableSort,
  type AnalyticsTablePreferences
} from "./analyticsTablePreferences";

describe("analytics table preferences", () => {
  test("creates a complete independent default for every tab", () => {
    const all = defaultAnalyticsTablePreferences("neutral");
    const service = defaultAnalyticsTablePreferences("service");

    expect(all.order).toEqual(["canonical", "class", "language", "frequency", "messages", "trend", "lastSeen", "forms", "actions"]);
    expect(all).not.toBe(service);
    expect(service.visible).toContain("canonical");
    expect(all).toMatchObject({ sortBy: "frequency", sortDirection: "desc", pageSize: 20 });
  });

  test("keeps only known persisted columns and restores missing defaults", () => {
    const persisted = {
      order: ["messages", "canonical", "unknown"],
      visible: ["canonical", "messages", "unknown"],
      widths: { canonical: 320, messages: 104, unknown: 99 }
    } as unknown as Partial<AnalyticsTablePreferences>;

    expect(normalizeAnalyticsTablePreferences(persisted).order).toEqual([
      "messages", "canonical", "class", "language", "frequency", "trend", "lastSeen", "forms", "actions"
    ]);
    expect(normalizeAnalyticsTablePreferences(persisted).widths).toMatchObject({ canonical: 320, messages: 104 });
  });

  test("upgrades the previous default width so the Russian mentions header remains readable", () => {
    expect(normalizeAnalyticsTablePreferences({
      Columns: [
        { Key: "canonical", Width: 260, Visible: true },
        { Key: "frequency", Width: 86, Visible: true }
      ]
    }).widths.frequency).toBe(128);
  });

  test("normalizes both Wails casing styles for the backend preference domain", () => {
    const pascalCase = {
      Tab: "positive",
      Columns: [{ Key: "messages", Width: 104, Visible: true }, { Key: "canonical", Width: 320, Visible: true }],
      SortBy: "messages",
      SortDirection: "ascending",
      PageSize: 75
    };
    const camelCase = {
      tab: "negative",
      columns: [{ key: "forms", width: 92, visible: true }, { key: "actions", width: 144, visible: false }],
      sortBy: "forms",
      sortDirection: "descending",
      pageSize: 25
    };

    expect(normalizeAnalyticsTablePreferences(pascalCase)).toMatchObject({
      order: ["messages", "canonical"], visible: ["messages", "canonical"], widths: { messages: 104, canonical: 320 }, sortBy: "messages", sortDirection: "asc", pageSize: 75
    });
    expect(normalizeAnalyticsTablePreferences(camelCase)).toMatchObject({
      order: ["forms", "actions"], visible: ["forms"], widths: { forms: 92, actions: 144 }, sortBy: "forms", sortDirection: "desc", pageSize: 25
    });
  });

  test("serializes per-tab columns and sort in the backend domain shape", () => {
    const preferences = withAnalyticsTableSort(defaultAnalyticsTablePreferences("positive"), { column: "messages", direction: "asc" });

    expect(toAnalyticsTablePreference("positive", preferences)).toEqual({
      Tab: "positive",
      Columns: [
        { Key: "canonical", Width: 260, Visible: true },
        { Key: "class", Width: 116, Visible: true },
        { Key: "language", Width: 72, Visible: true },
        { Key: "frequency", Width: 128, Visible: true },
        { Key: "messages", Width: 94, Visible: true },
        { Key: "trend", Width: 128, Visible: true },
        { Key: "lastSeen", Width: 164, Visible: true },
        { Key: "forms", Width: 76, Visible: true },
        { Key: "actions", Width: 144, Visible: true }
      ],
      SortBy: "messages",
      SortDirection: "ascending",
      PageSize: 20
    });
  });

  test("moves a table column without duplicating or losing saved columns", () => {
    expect(reorderAnalyticsColumns(
      ["canonical", "class", "language", "frequency", "messages", "trend", "lastSeen", "forms", "actions"],
      "trend",
      "canonical"
    )).toEqual(["trend", "canonical", "class", "language", "frequency", "messages", "lastSeen", "forms", "actions"]);
  });

  test("resolves a drag source synchronously before React state is rendered", () => {
    expect(resolveDraggedAnalyticsColumn("trend", "canonical")).toBe("trend");
    expect(resolveDraggedAnalyticsColumn(null, "trend")).toBe("trend");
    expect(resolveDraggedAnalyticsColumn(null, "unknown")).toBeUndefined();
  });

  test("serializes preference writes so the final resize state cannot be overwritten", async () => {
    let releaseFirst: (() => void) | undefined;
    let calls = 0;
    const save = vi.fn(() => ++calls === 1 ? new Promise<void>((resolve) => { releaseFirst = resolve; }) : Promise.resolve());
    const queue = createAnalyticsTablePreferenceSaveQueue({ load: vi.fn(), save }, () => undefined);
    const initial = defaultAnalyticsTablePreferences("neutral");
    const resized = { ...initial, widths: { ...initial.widths, canonical: 420 } };

    const first = queue.save("neutral", toAnalyticsTablePreference("neutral", initial));
    const second = queue.save("neutral", toAnalyticsTablePreference("neutral", resized));

    await Promise.resolve();
    expect(save).toHaveBeenCalledTimes(1);
    releaseFirst?.();
    await first;
    expect(save).toHaveBeenLastCalledWith("neutral", toAnalyticsTablePreference("neutral", resized));
    await second;
  });
});
