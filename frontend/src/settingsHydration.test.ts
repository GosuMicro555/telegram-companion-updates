import { readFileSync } from "node:fs";
import { describe, expect, it, vi } from "vitest";
import {
  completeHydration,
  failedHydration,
  initialHydration,
  markHydratedEdit,
  nextJoinIntervalRange,
  normalizeJoinIntervalRange,
  safeInitialAppSettings,
  safeInitialKeywordSettings,
  isValidGroupRestHours,
  isValidJoinIntervalRange,
  shouldPersistHydratedSettings,
  successfulHydration
} from "./settingsHydration";

describe("settings hydration safety", () => {
  it("starts with empty non-DM production values", () => {
    expect(safeInitialKeywordSettings).toEqual({ keywords: [], minusKeywords: [], sharedReply: "", deliveryMode: "comments", directMessageKeywords: [] });
    expect(safeInitialAppSettings.directMessages).toBe(false);
    expect(safeInitialAppSettings.groupRestHours).toBe(36);
    expect(safeInitialAppSettings.joinIntervalEnabled).toBe(true);
  });

  it("accepts join intervals from zero through 3000 minutes", () => {
    expect(isValidJoinIntervalRange(0, 0)).toBe(true);
    expect(isValidJoinIntervalRange(0, 3000)).toBe(true);
    expect(isValidJoinIntervalRange(-1, 30)).toBe(false);
    expect(isValidJoinIntervalRange(30, 3001)).toBe(false);
    expect(isValidJoinIntervalRange(31, 30)).toBe(false);
  });

  it("keeps join interval edits inside a valid zero through 3000 range", () => {
    expect(nextJoinIntervalRange(10, 60, "minimum", 4000)).toEqual({ minimum: 3000, maximum: 3000 });
    expect(nextJoinIntervalRange(10, 60, "maximum", -1)).toEqual({ minimum: 0, maximum: 0 });
    expect(nextJoinIntervalRange(10, 60, "minimum", 70.9)).toEqual({ minimum: 70, maximum: 70 });
    expect(normalizeJoinIntervalRange(4000, -10)).toEqual({ minimum: 0, maximum: 3000 });
  });

  it("accepts only whole account rest durations from one through 720 hours", () => {
    expect(isValidGroupRestHours(1)).toBe(true);
    expect(isValidGroupRestHours(720)).toBe(true);
    expect(isValidGroupRestHours(36.5)).toBe(false);
    expect(isValidGroupRestHours(0)).toBe(false);
    expect(isValidGroupRestHours(721)).toBe(false);
  });

  it("does not embed acceptance test literals in the production app", () => {
    const app = readFileSync(new URL("./App.tsx", import.meta.url), "utf8");
    expect(app).not.toMatch(/тест1|тест2|тест3|Единый тестовый ответ/);
  });

  it("never autosaves fallback state after read failure even when writes later work", async () => {
    let state = failedHydration(initialHydration);
    state = markHydratedEdit(state);
    const save = vi.fn(async () => undefined);

    if (shouldPersistHydratedSettings(state)) {
      await save();
    }

    expect(save).not.toHaveBeenCalled();
    const loaded = markHydratedEdit(successfulHydration(initialHydration));
    expect(shouldPersistHydratedSettings(loaded)).toBe(true);
  });

  it("finishes hydration without marking pre-load edits as saved", () => {
    const editedWhileLoading = markHydratedEdit(initialHydration);
    const completed = completeHydration(editedWhileLoading, true);

    expect(completed.phase).toBe("loaded");
    expect(completed.savedVersion).toBe(0);
    expect(shouldPersistHydratedSettings(completed)).toBe(true);
    expect(shouldPersistHydratedSettings(completeHydration(initialHydration, false))).toBe(false);
  });
});
