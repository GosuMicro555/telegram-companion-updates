import { describe, expect, test } from "vitest";
import { formatAccountRestCountdown, normalizeAccountRests } from "./accountRest";

const until = "2026-07-24T12:00:00Z";

describe("account rest helpers", () => {
  test("derives resting only while until is strictly in the future", () => {
    const rows = normalizeAccountRests([
      { accountID: "a", accountTitle: "Anna", channelID: "c", channelTitle: "Alpha", catalog: "outbound", startedAt: "2026-07-23T00:00:00Z", until, durationHours: 36 },
      { accountID: "b", accountTitle: "Boris", channelID: "d", channelTitle: "Beta", catalog: "scout", startedAt: "2026-07-23T00:00:00Z", until: "2026-07-24T10:00:00Z", durationHours: 36 }
    ], new Date("2026-07-24T10:00:00Z"));

    expect(rows.map((row) => row.status)).toEqual(["resting", "ready"]);
  });

  test("formats Russian remaining time and clamps expired values to zero", () => {
    expect(formatAccountRestCountdown(until, new Date("2026-07-24T10:29:30Z"), "ru")).toBe("1 ч 30 мин");
    expect(formatAccountRestCountdown("2026-07-24T10:00:00Z", new Date("2026-07-24T10:00:00Z"), "ru")).toBe("0 мин");
  });
});
