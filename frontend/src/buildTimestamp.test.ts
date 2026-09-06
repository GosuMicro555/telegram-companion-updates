import { describe, expect, test } from "vitest";
import { formatBuildTimestamp } from "./buildTimestamp";

describe("build timestamp", () => {
  test("formats the immutable UTC build time in Moscow time", () => {
    expect(formatBuildTimestamp("2026-08-11T17:45:00Z")).toBe("11.08.2026 | 20:45");
  });

  test("uses a neutral placeholder when build metadata is unavailable", () => {
    expect(formatBuildTimestamp("")).toBe("-");
    expect(formatBuildTimestamp("not-a-date")).toBe("-");
  });
});
