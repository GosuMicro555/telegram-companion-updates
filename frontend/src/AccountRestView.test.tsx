import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, describe, expect, test, vi } from "vitest";
import { AccountRestViewContent, createAccountRestPolling, shouldPollAccountRests } from "./AccountRestView";
import { normalizeAccountRests } from "./accountRest";

afterEach(() => vi.useRealTimers());

const dtoRows = [{
  accountID: "account-1", accountTitle: "Anna", channelID: "channel-1", channelTitle: "Alpha group",
  catalog: "outbound", startedAt: "2026-07-23T00:00:00Z", until: "2026-07-24T12:00:00Z", durationHours: 36
}];

test("refreshes backend rows every thirty seconds while local ticks never reload", async () => {
  vi.useFakeTimers();
  const load = vi.fn(async () => dtoRows);
  const rows: number[] = [];
  const ticks: number[] = [];
  const dispose = createAccountRestPolling(load, (next) => rows.push(next.length), () => undefined, () => ticks.push(1));

  await Promise.resolve();
  expect(load).toHaveBeenCalledTimes(1);
  await vi.advanceTimersByTimeAsync(1_000);
  expect(ticks).toEqual([1]);
  expect(load).toHaveBeenCalledTimes(1);
  await vi.advanceTimersByTimeAsync(29_000);
  expect(load).toHaveBeenCalledTimes(2);
  expect(rows).toEqual([1, 1]);
  dispose();
});

test("renders a compact rest table with all dashboard columns", () => {
  const markup = renderToStaticMarkup(<AccountRestViewContent
    enabled
    error=""
    loaded
    locale="en"
    now={new Date("2026-07-24T10:00:00Z")}
    onToggle={() => undefined}
    rows={normalizeAccountRests(dtoRows, new Date("2026-07-24T10:00:00Z"))}
    saving={false}
  />);

  expect(markup).toContain("Account");
  expect(markup).toContain("Channel / group");
  expect(markup).toContain("Catalog");
  expect(markup).toContain("Status");
  expect(markup).toContain("Started");
  expect(markup).toContain("Ends");
  expect(markup).toContain("Remaining");
  expect(markup).toContain("Total hours");
  expect(markup).toContain("Anna");
  expect(markup).toContain("Alpha group");
  expect(markup).toContain("Resting");
  expect(markup).toContain("Account rest");
  expect(markup).toContain("aria-pressed=\"true\"");
});

test("does not poll or expose rest rows before enabled settings are loaded", () => {
  expect(shouldPollAccountRests(false, true)).toBe(false);
  expect(shouldPollAccountRests(true, false)).toBe(false);
  expect(shouldPollAccountRests(true, true)).toBe(true);

  const markup = renderToStaticMarkup(<AccountRestViewContent
    enabled
    error=""
    loaded={false}
    locale="en"
    now={new Date("2026-07-24T10:00:00Z")}
    onToggle={() => undefined}
    rows={normalizeAccountRests(dtoRows, new Date("2026-07-24T10:00:00Z"))}
    saving={false}
  />);

  expect(markup).not.toContain("Anna");
  expect(markup).not.toContain("Alpha group");
  expect(markup).toContain("disabled=\"\"");
});
