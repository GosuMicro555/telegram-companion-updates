import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, expect, test, vi } from "vitest";
import { GetChannelModeration, RetryCatalogJoin } from "../wailsjs/go/wails/Bindings";
import { normalizeChannelModeration } from "./channelModeration";
import { ChannelModerationViewContent, createChannelModerationPolling, retryChannelModerationRow } from "./ChannelModerationView";

vi.mock("../wailsjs/go/wails/Bindings", () => ({ GetChannelModeration: vi.fn(), RetryCatalogJoin: vi.fn() }));

const dtoRows = [
  { catalog: "outbound", channelID: "alpha-id", title: "Alpha", link: "https://t.me/alpha", topic: "Loans", applications: 2, joined: 1, pending: 1, status: "partial", firstRequestAt: "2026-07-18T08:00:00", durationSeconds: 5400, accounts: [{ title: "Anna", role: "outbound", requestSubmittedAt: "2026-07-18T08:00:00", joinedAt: "", durationSeconds: 5400, status: "pending_approval" }] },
  { catalog: "scout", channelID: "beta-id", title: "Beta", link: "https://t.me/beta", topic: "News", applications: 1, joined: 1, pending: 0, status: "member", firstRequestAt: "2026-07-17T08:00:00", durationSeconds: 120, accounts: [] },
  { catalog: "outbound", channelID: "gamma-id", title: "Gamma", link: "https://t.me/gamma", topic: "Finance", applications: 1, joined: 0, pending: 1, status: "pending_approval", firstRequestAt: "2026-07-19T08:00:00", durationSeconds: 0, accounts: [] }
];
const rows = normalizeChannelModeration(dtoRows);

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((resolvePromise) => { resolve = resolvePromise; });
  return { promise, resolve };
}

afterEach(() => vi.useRealTimers());

test("loads immediately, refreshes locally every 15 seconds, and cancels on unmount", async () => {
  vi.useFakeTimers();
  const getChannelModeration = vi.mocked(GetChannelModeration);
  getChannelModeration.mockResolvedValueOnce(dtoRows as Awaited<ReturnType<typeof GetChannelModeration>>).mockResolvedValueOnce([]);
  const applied: number[] = [];
  const dispose = createChannelModerationPolling(GetChannelModeration, (next) => applied.push(next.length), () => undefined);

  await Promise.resolve();
  expect(getChannelModeration).toHaveBeenCalledTimes(1);
  expect(applied).toEqual([3]);

  await vi.advanceTimersByTimeAsync(15_000);
  expect(getChannelModeration).toHaveBeenCalledTimes(2);
  expect(applied).toEqual([3, 0]);
  dispose();

  const request = deferred<unknown>();
  getChannelModeration.mockReset().mockReturnValueOnce(request.promise as ReturnType<typeof GetChannelModeration>);
  const ignored: number[] = [];
  const canceled = createChannelModerationPolling(GetChannelModeration, (next) => ignored.push(next.length), () => undefined);
  canceled();
  request.resolve(rows);
  await Promise.resolve();
  expect(ignored).toEqual([]);
});

test("renders filters, counters, search results, errors, empty state, and expanded account columns", () => {
  const expanded = new Set([rows[0].key]);
  const markup = renderToStaticMarkup(<ChannelModerationViewContent
    error="local database unavailable"
    expanded={expanded}
    filter="all"
    locale="en"
    onFilter={() => undefined}
    onSearch={() => undefined}
    onSort={() => undefined}
    onToggle={() => undefined}
    rows={rows}
    search=""
    sort={{ column: "firstRequestAt", direction: "desc" }}
  />);

  expect(markup).toContain("All (3)");
  expect(markup).toContain("Awaiting approval (1)");
  expect(markup).toContain("Partial (1)");
  expect(markup).toContain("Joined (1)");
  expect(markup).toContain('role="search"');
  expect(markup).toContain("Search channels, links, and topics");
  expect(markup).toContain('role="alert"');
  expect(markup).toContain("local database unavailable");
  expect(markup).toContain("Telegram account");
  expect(markup).toContain("Role");
  expect(markup).toContain("Request submitted");
  expect(markup).toContain("Joined at");
  expect(markup).toContain("Waiting time");
  expect(markup).toContain("Anna");
  expect(markup).not.toMatch(/START|STOP|Telegram command/i);

  const searched = renderToStaticMarkup(<ChannelModerationViewContent
    error=""
    expanded={expanded}
    filter="all"
    locale="en"
    onFilter={() => undefined}
    onSearch={() => undefined}
    onSort={() => undefined}
    onToggle={() => undefined}
    rows={rows}
    search="finance"
    sort={{ column: "firstRequestAt", direction: "desc" }}
  />);
  expect(searched).toContain("Gamma");
  expect(searched).not.toContain("Beta");

  const empty = renderToStaticMarkup(<ChannelModerationViewContent
    error=""
    expanded={new Set()}
    filter="all"
    locale="en"
    onFilter={() => undefined}
    onSearch={() => undefined}
    onSort={() => undefined}
    onToggle={() => undefined}
    rows={[]}
    search=""
    sort={{ column: "firstRequestAt", direction: "desc" }}
  />);
  expect(empty).toContain("No moderation requests yet.");
});

test("keeps an expanded channel selected when refreshed rows retain its stable key", () => {
  const refreshed = normalizeChannelModeration([{ ...rows[0], applications: 3 }]);
  const markup = renderToStaticMarkup(<ChannelModerationViewContent
    error=""
    expanded={new Set([rows[0].key])}
    filter="all"
    locale="en"
    onFilter={() => undefined}
    onSearch={() => undefined}
    onSort={() => undefined}
    onToggle={() => undefined}
    rows={refreshed}
    search=""
    sort={{ column: "firstRequestAt", direction: "desc" }}
  />);

  expect(markup).toContain("Anna");
});

test("offers join again only for pending approval rows and calls the retry binding with the real channel ID", async () => {
  const markup = renderToStaticMarkup(<ChannelModerationViewContent
    error=""
    expanded={new Set()}
    filter="all"
    locale="en"
    onFilter={() => undefined}
    onRetry={() => undefined}
    onSearch={() => undefined}
    onSort={() => undefined}
    onToggle={() => undefined}
    retrying={new Set()}
    rows={rows}
    search=""
    sort={{ column: "firstRequestAt", direction: "desc" }}
  />);

  expect(markup.match(/Join again/g)).toHaveLength(1);

  const retry = vi.mocked(RetryCatalogJoin);
  retry.mockResolvedValueOnce([dtoRows[2]] as Awaited<ReturnType<typeof RetryCatalogJoin>>);
  const refreshed = await retryChannelModerationRow(rows[2], retry);
  expect(retry).toHaveBeenCalledWith("outbound", "gamma-id");
  expect(refreshed).toHaveLength(1);
  expect(refreshed[0].channelID).toBe("gamma-id");
});

test("renders joining queue rows without pretending a request was submitted", () => {
  const joiningRows = normalizeChannelModeration([{
    catalog: "outbound",
    title: "Queued channel",
    link: "https://t.me/queued",
    topic: "",
    applications: 0,
    joined: 0,
    pending: 3,
    status: "joining",
    firstRequestAt: "",
    durationSeconds: 0,
    accounts: [{ title: "Queued account", role: "spammer", requestSubmittedAt: "", joinedAt: "", durationSeconds: 0, status: "joining" }]
  }]);

  const markup = renderToStaticMarkup(<ChannelModerationViewContent
    error=""
    expanded={new Set([joiningRows[0].key])}
    filter="all"
    locale="en"
    onFilter={() => undefined}
    onSearch={() => undefined}
    onSort={() => undefined}
    onToggle={() => undefined}
    rows={joiningRows}
    search=""
    sort={{ column: "firstRequestAt", direction: "desc" }}
  />);

  expect(markup).toContain("Joining queue (1)");
  expect(markup).toContain("Queued account");
  expect(markup).not.toContain('dateTime=""');
  expect(markup).not.toContain("0 min");
});
