import { describe, expect, test } from "vitest";
import { arrayFromBridge } from "./bridgeCollections";

describe("arrayFromBridge", () => {
  test("preserves arrays returned by Wails", () => {
    const rows = [{ id: "one" }];
    expect(arrayFromBridge(rows)).toBe(rows);
  });

  test.each([null, undefined, {}, "invalid", 0])(
    "normalizes a non-array bridge value (%p) to an empty array",
    (value) => {
      expect(arrayFromBridge(value)).toEqual([]);
    }
  );
});
