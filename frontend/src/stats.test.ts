import { describe, expect, test } from "vitest";
import {
  lineChartPoints,
  normalizeReplyStatistics,
  replyStatisticRows,
  sortReplyStatisticRows
} from "./stats";

describe("reply statistics data", () => {
  test("normalizes the persisted reply statistics DTO", () => {
    expect(normalizeReplyStatistics({
      replies: 24,
      publicReplies: 15,
      privateMessages: 9,
      privateMessagesClosed: 4,
      channels: 3,
      accounts: 2,
      averageRepliesPerMinute: 0.8,
      timeSeries: [{ date: "2026-07-14", replies: 8 }],
      accountRows: [{ id: "account-a", title: "Alpha", replies: 14, publicReplies: 8, privateMessages: 6, privateMessagesClosed: 3, lastActivityAt: "2026-07-14T10:00:00Z" }],
      channelRows: [{ id: "channel-a", title: "General", replies: 10, lastActivityAt: "2026-07-14T09:00:00Z" }]
    })).toEqual({
      replies: 24,
      publicReplies: 15,
      privateMessages: 9,
      privateMessagesClosed: 4,
      channels: 3,
      accounts: 2,
      averageRepliesPerMinute: 0.8,
      timeSeries: [{ date: "2026-07-14", replies: 8 }],
      accountRows: [{ id: "account-a", title: "Alpha", replies: 14, publicReplies: 8, privateMessages: 6, privateMessagesClosed: 3, lastActivityAt: "2026-07-14T10:00:00Z" }],
      channelRows: [{ id: "channel-a", title: "General", replies: 10, publicReplies: 0, privateMessages: 0, privateMessagesClosed: 0, lastActivityAt: "2026-07-14T09:00:00Z" }]
    });
  });

  test("normalizes missing and malformed split metrics to zero", () => {
    expect(normalizeReplyStatistics({
      publicReplies: "invalid",
      privateMessages: null,
      privateMessagesClosed: {},
      accountRows: [{ id: "account-a", title: "Alpha", publicReplies: "bad" }],
      channelRows: [{ id: "channel-a", title: "General" }]
    })).toMatchObject({
      publicReplies: 0,
      privateMessages: 0,
      privateMessagesClosed: 0,
      accountRows: [{ publicReplies: 0, privateMessages: 0, privateMessagesClosed: 0 }],
      channelRows: [{ publicReplies: 0, privateMessages: 0, privateMessagesClosed: 0 }]
    });
  });

  test("uses only actual series points when constructing the chart", () => {
    expect(lineChartPoints([{ date: "2026-07-14", replies: 4 }, { date: "2026-07-15", replies: 12 }], 100, 40)).toEqual("0,40 100,0");
    expect(lineChartPoints([], 100, 40)).toBe("");
  });

  test("combines and sorts account and channel rows by replies", () => {
    const rows = replyStatisticRows({
      replies: 18,
      publicReplies: 7,
      privateMessages: 11,
      privateMessagesClosed: 2,
      channels: 1,
      accounts: 1,
      averageRepliesPerMinute: 0.4,
      timeSeries: [],
      accountRows: [{ id: "account-a", title: "Alpha", replies: 7, publicReplies: 2, privateMessages: 5, privateMessagesClosed: 2, lastActivityAt: "2026-07-14T10:00:00Z" }],
      channelRows: [{ id: "channel-a", title: "General", replies: 11, publicReplies: 0, privateMessages: 0, privateMessagesClosed: 0, lastActivityAt: "2026-07-15T10:00:00Z" }]
    });

    expect(sortReplyStatisticRows(rows, "replies", "desc").map((row) => row.title)).toEqual(["General", "Alpha"]);
    expect(sortReplyStatisticRows(rows, "publicReplies", "desc").map((row) => row.title)).toEqual(["Alpha", "General"]);
    expect(sortReplyStatisticRows(rows, "privateMessages", "desc").map((row) => row.title)).toEqual(["Alpha", "General"]);
    expect(sortReplyStatisticRows(rows, "privateMessagesClosed", "desc").map((row) => row.title)).toEqual(["Alpha", "General"]);
  });

  test.each(["publicReplies", "privateMessages", "privateMessagesClosed"] as const)(
    "keeps account values ahead of channel N/A rows when sorting %s",
    (field) => {
      const rows = replyStatisticRows({
        ...emptyStatistics(),
        accountRows: [
          { id: "account-alpha", title: "Alpha", replies: 0, publicReplies: 0, privateMessages: 0, privateMessagesClosed: 0, lastActivityAt: "" },
          { id: "account-able", title: "Able", replies: 0, publicReplies: 0, privateMessages: 0, privateMessagesClosed: 0, lastActivityAt: "" },
          { id: "account-beta", title: "Beta", replies: 6, publicReplies: 2, privateMessages: 2, privateMessagesClosed: 2, lastActivityAt: "" }
        ],
        channelRows: [{ id: "channel-general", title: "General", replies: 6, publicReplies: 0, privateMessages: 0, privateMessagesClosed: 0, lastActivityAt: "" }]
      });

      expect(sortReplyStatisticRows(rows, field, "asc").map((row) => row.title)).toEqual(["Able", "Alpha", "Beta", "General"]);
      expect(sortReplyStatisticRows(rows, field, "desc").map((row) => row.title)).toEqual(["Beta", "Able", "Alpha", "General"]);
    }
  );
});

function emptyStatistics() {
  return {
    replies: 0,
    publicReplies: 0,
    privateMessages: 0,
    privateMessagesClosed: 0,
    channels: 0,
    accounts: 0,
    averageRepliesPerMinute: 0,
    timeSeries: [],
    accountRows: [],
    channelRows: []
  };
}
