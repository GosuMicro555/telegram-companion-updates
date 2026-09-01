import { renderToStaticMarkup } from "react-dom/server";
import { readFileSync } from "node:fs";
import { expect, test } from "vitest";
import { replyStatisticsDateTimeRange, replyStatisticsRequestRange, StatsView, StatsViewContent } from "./StatsView";

const statistics = {
  replies: 24,
  publicReplies: 15,
  privateMessages: 9,
  privateMessagesClosed: 4,
  channels: 2,
  accounts: 1,
  averageRepliesPerMinute: 0.8,
  timeSeries: [{ date: "2026-07-14", replies: 8 }, { date: "2026-07-15", replies: 16 }],
  accountRows: [{ id: "account-a", title: "Alpha", replies: 14, publicReplies: 8, privateMessages: 6, privateMessagesClosed: 3, lastActivityAt: "2026-07-15T10:00:00Z" }],
  channelRows: [{ id: "channel-a", title: "General", replies: 10, publicReplies: 0, privateMessages: 0, privateMessagesClosed: 0, lastActivityAt: "2026-07-15T09:00:00Z" }]
};

test("renders compact persisted reply metrics, chart, and table", () => {
  const markup = renderToStaticMarkup(<StatsViewContent locale="en" statistics={statistics} />);

  expect(markup).toContain("Replies");
  expect(markup).toContain("Replies / min");
  expect(markup).toContain("Public replies");
  expect(markup).toContain("Successful DMs");
  expect(markup).toContain("Closed DMs");
  expect(markup).toContain('aria-label="Reply activity"');
  expect(markup).toContain("Alpha");
  expect(markup).toContain("General");
  expect(markup).toContain('aria-label="Account and channel replies"');
  expect(markup).toContain('aria-label="Choose columns"');
  expect(markup).toContain('aria-sort="descending"');
  expect(markup).toContain('<strong title="Alpha">Alpha</strong>');
  expect(markup).toContain('<strong title="General">General</strong>');
  expect(markup).toContain("14");
  expect(markup).toContain("8");
  expect(markup).toContain("6");
  expect(markup).toContain("3");
  expect(markup).toContain("Previous page");
  expect(markup).toContain("Next page");
  expect(markup).toContain("0,112 240,0");
});

test("renders an honest empty state without an invented chart", () => {
  const markup = renderToStaticMarkup(<StatsViewContent locale="en" statistics={{ ...statistics, replies: 0, timeSeries: [], accountRows: [], channelRows: [] }} />);

  expect(markup).toContain("No reply activity exists for this date range.");
  expect(markup).not.toContain("<polyline");
});

test("uses the same explicit date-range labels as live statistics", () => {
  const markup = renderToStaticMarkup(<StatsView locale="ru" />);

  expect(markup).toContain("Выбор даты начала");
  expect(markup).toContain("Выбор даты окончания");
});

test("keeps full local datetimes for the statistics range and converts selected times for the API", () => {
  expect(replyStatisticsDateTimeRange(new Date(2026, 6, 15, 14, 30))).toEqual({ from: "2026-06-16T00:00", to: "2026-07-15T23:59" });

  const selected = { from: "2026-07-14T10:15", to: "2026-07-15T18:45" };
  expect(replyStatisticsRequestRange(selected.from, selected.to)).toEqual({
    from: new Date(selected.from).toISOString(),
    to: new Date(selected.to).toISOString()
  });
});

test("renders initial statistics fields with the full-day time bounds", () => {
  const markup = renderToStaticMarkup(<StatsView locale="en" />);

  expect(markup).toContain("00:00");
  expect(markup).toContain("23:59");
});

test("keeps the stats table in an internal scrolling workspace", () => {
  const css = readFileSync(new URL("./styles.css", import.meta.url), "utf8");

  expect(css).toMatch(/\.content:has\(\.statsWorkspace\)\s*\{[^}]*overflow:\s*hidden/s);
  expect(css).toMatch(/\.statsWorkspace\s*\{[^}]*min-height:\s*0[^}]*overflow:\s*hidden/s);
  expect(css).toMatch(/\.statsWorkspace:has\(> \.errorBanner\)\s*\{[^}]*grid-template-rows:\s*auto auto auto auto minmax\(0,\s*1fr\)/s);
  expect(css).toMatch(/\.configurableStatsTable__body\s*\{[^}]*min-height:\s*0[^}]*overflow:\s*auto/s);
  expect(css).toMatch(/\.configurableStatsTable__header\s*\{[^}]*position:\s*sticky/s);
});

test("keeps release history typography on the Friday inherited style", () => {
  const css = readFileSync(new URL("./styles.css", import.meta.url), "utf8");

  expect(css).not.toMatch(/\.releaseNotesPage h2\s*\{[^}]*font-(?:size|weight|family)\s*:/s);
  expect(css).not.toMatch(/\.releaseNoteEntry ul\s*\{[^}]*font-(?:size|weight|family)\s*:/s);
});
