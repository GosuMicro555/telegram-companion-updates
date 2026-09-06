import { describe, expect, test } from "vitest";
import {
  filterChannelModeration,
  formatModerationDate,
  formatModerationDuration,
  normalizeChannelModeration,
  sortChannelModeration,
  type ChannelModerationRow
} from "./channelModeration";

const source = [
  {
    catalog: "outbound", channelID: "alpha-id", title: "Alpha channel", link: "https://t.me/alpha", topic: "Loans",
    applications: 3, joined: 1, pending: 2, status: "partial", firstRequestAt: "2026-07-18T08:00:00Z", durationSeconds: 5400,
    accounts: [{ title: "Anna", role: "outbound", requestSubmittedAt: "2026-07-18T08:00:00Z", joinedAt: "", durationSeconds: 5400, status: "pending_approval" }]
  },
  {
    Catalog: "scout", ChannelID: "beta-id", Title: "Beta channel", Link: "https://t.me/beta", Topic: "News",
    Applications: 2, Joined: 2, Pending: 0, Status: "member", FirstRequestAt: "2026-07-17T08:00:00Z", DurationSeconds: 120,
    Accounts: [{ Title: "Boris", Role: "scout", RequestSubmittedAt: "2026-07-17T08:00:00Z", JoinedAt: "2026-07-17T08:02:00Z", DurationSeconds: 120, Status: "member" }]
  },
  {
    catalog: "outbound", channelID: "gamma-id", title: "Gamma channel", link: "https://t.me/gamma", topic: "Finance",
    applications: 1, joined: 0, pending: 1, status: "pending_approval", firstRequestAt: "2026-07-19T08:00:00Z", durationSeconds: 0, accounts: []
  }
];

describe("channel moderation model", () => {
  test("normalizes lower- and upper-case Wails DTO fields", () => {
    const rows = normalizeChannelModeration(source);

    expect(rows).toHaveLength(3);
    expect(rows[1]).toMatchObject({ catalog: "scout", channelID: "beta-id", key: "beta-id", title: "Beta channel", applications: 2, status: "member" });
    expect(rows[1].accounts[0]).toEqual({ title: "Boris", role: "scout", requestSubmittedAt: "2026-07-17T08:00:00Z", joinedAt: "2026-07-17T08:02:00Z", durationSeconds: 120, status: "member" });
  });

  test("filters aggregate statuses and searches title, link, and topic", () => {
    const rows = normalizeChannelModeration(source);

    expect(filterChannelModeration(rows, "all", "")).toHaveLength(3);
    expect(filterChannelModeration(rows, "pending_approval", "").map((row) => row.title)).toEqual(["Gamma channel"]);
    expect(filterChannelModeration(rows, "partial", "").map((row) => row.title)).toEqual(["Alpha channel"]);
    expect(filterChannelModeration(rows, "member", "").map((row) => row.title)).toEqual(["Beta channel"]);
    expect(filterChannelModeration(rows, "all", "T.ME/BETA").map((row) => row.title)).toEqual(["Beta channel"]);
    expect(filterChannelModeration(rows, "all", "finance").map((row) => row.title)).toEqual(["Gamma channel"]);
  });

  test("filters the joining queue aggregate status", () => {
    const joining = normalizeChannelModeration([{
      catalog: "outbound", title: "Queued channel", link: "https://t.me/queued", topic: "",
      applications: 0, joined: 0, pending: 3, status: "joining", firstRequestAt: "", durationSeconds: 0, accounts: []
    }]);

    expect(filterChannelModeration(joining, "joining", "").map((row) => row.title)).toEqual(["Queued channel"]);
  });

  test.each([
    ["title", ["Alpha channel", "Beta channel", "Gamma channel"]],
    ["topic", ["Gamma channel", "Alpha channel", "Beta channel"]],
    ["applications", ["Gamma channel", "Beta channel", "Alpha channel"]],
    ["joined", ["Gamma channel", "Alpha channel", "Beta channel"]],
    ["pending", ["Beta channel", "Gamma channel", "Alpha channel"]],
    ["status", ["Beta channel", "Alpha channel", "Gamma channel"]],
    ["firstRequestAt", ["Beta channel", "Alpha channel", "Gamma channel"]],
    ["durationSeconds", ["Gamma channel", "Beta channel", "Alpha channel"]]
  ] as const)("sorts %s deterministically", (column, expected) => {
    const rows = normalizeChannelModeration(source);

    expect(sortChannelModeration(rows, { column, direction: "asc" }).map((row) => row.title)).toEqual(expected);
    expect(sortChannelModeration(rows, { column, direction: "desc" }).map((row) => row.title)).toEqual([...expected].reverse());
  });

  test("uses stable title tie-breaks while sorting", () => {
    const rows: ChannelModerationRow[] = [
      { ...normalizeChannelModeration(source)[0], title: "Zulu", applications: 1 },
      { ...normalizeChannelModeration(source)[1], title: "Alpha", applications: 1 }
    ];

    expect(sortChannelModeration(rows, { column: "applications", direction: "asc" }).map((row) => row.title)).toEqual(["Alpha", "Zulu"]);
  });

  test("keeps row keys stable when a refresh returns a different row order", () => {
    const before = normalizeChannelModeration(source).find((row) => row.title === "Alpha channel");
    const after = normalizeChannelModeration([...source].reverse()).find((row) => row.title === "Alpha channel");

    expect(after?.key).toBe(before?.key);
  });

  test("formats zero and negative durations safely", () => {
    expect(formatModerationDuration(undefined, "en")).toBe("-");
    expect(formatModerationDuration(0, "ru")).toBe("0 мин.");
    expect(formatModerationDuration(-5, "ru")).toBe("0 мин.");
    expect(formatModerationDuration(3 * 3600 + 18 * 60, "en")).toBe("3 h 18 min");
    expect(formatModerationDuration(2 * 86400 + 4 * 3600, "ru")).toBe("2 д. 4 ч.");
  });

  test("formats Russian dates as DD.MM.YYYY | HH:MM", () => {
    expect(formatModerationDate("2026-07-18T11:17:00", "ru")).toBe("18.07.2026 | 11:17");
    expect(formatModerationDate("not-a-date", "ru")).toBe("-");
  });
});
