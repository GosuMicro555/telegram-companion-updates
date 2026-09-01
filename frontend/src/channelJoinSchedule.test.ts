import { readFileSync } from "node:fs";
import { describe, expect, test } from "vitest";
import { channelJoinSchedule } from "./App";
import {
  isValidJoinIntervalRange,
  safeInitialAppSettings
} from "./settingsHydration";

describe("paced channel joins", () => {
  test("hydrates the backend's 10 to 60 minute defaults", () => {
    expect(safeInitialAppSettings).toMatchObject({
      joinIntervalMinMinutes: 10,
      joinIntervalMaxMinutes: 60
    });
  });

  test.each([
    [-1, 60, false],
    [0, 0, true],
    [0, 3000, true],
    [3000, 3000, true],
    [10, 3001, false],
    [45, 30, false]
  ])("validates the %i to %i minute range", (minimum, maximum, expected) => {
    expect(isValidJoinIntervalRange(minimum, maximum)).toBe(expected);
  });

  test("uses the returned planned status and timestamp without recalculation", () => {
    const joinNotBefore = "2026-07-18T09:30:00Z";

    expect(channelJoinSchedule({ planned: true, joinNotBefore })).toEqual({
      status: "planned",
      joinNotBefore
    });
    expect(channelJoinSchedule({ planned: false, joinNotBefore })).toBeNull();
  });

  test("persists both pacing fields through the Wails app settings payload", () => {
    const app = readFileSync(new URL("./App.tsx", import.meta.url), "utf8");

    expect(app).toContain("joinIntervalMinMinutes,");
    expect(app).toContain("joinIntervalMaxMinutes,");
    expect(app).toContain("joinIntervalEnabled:");
    expect(app).toContain('max={3000} min={0}');
    expect(app).toContain("toggleJoinInterval");
    expect(app).toContain("SaveAppSettings({");
  });
});
