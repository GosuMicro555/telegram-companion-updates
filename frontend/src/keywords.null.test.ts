import { describe, expect, test } from "vitest";
import { keywordRowsFromSettings } from "./keywords";

describe("keyword bridge boundary", () => {
  test("treats null keywords from a fresh public profile as empty", () => {
    expect(keywordRowsFromSettings(null as unknown as string[], null)).toEqual([]);
  });
});
