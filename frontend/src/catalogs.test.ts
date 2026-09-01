import { describe, expect, it, vi } from "vitest";
import * as catalogs from "./catalogs";
import { applyBulkTopic, existingTopics, toggleSelected, uniqueTelegramLinks } from "./catalogs";

type DeleteCatalogEntries = (catalog: "outbound" | "scout", channelIDs: string[]) => Promise<unknown[]>;
type RequestCatalogEntriesDeletion = (input: {
  catalog: "outbound" | "scout";
  channelIDs: string[];
  confirmationMessage: string;
  confirm: (message: string) => boolean | Promise<boolean>;
  deleteCatalogEntries: DeleteCatalogEntries;
}) => Promise<unknown[] | null>;
type CreateCatalogRemovalPolling = (input: {
  rows: Array<{ status: string }>;
  load: () => Promise<unknown[]>;
  onRows: (rows: unknown[]) => void;
  onError: (message: string) => void;
}) => () => void;

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((resolvePromise) => { resolve = resolvePromise; });
  return { promise, resolve };
}

function deletionRequestHelper(): RequestCatalogEntriesDeletion | undefined {
  return (catalogs as unknown as { requestCatalogEntriesDeletion?: RequestCatalogEntriesDeletion }).requestCatalogEntriesDeletion;
}

function removalPollingHelper(): CreateCatalogRemovalPolling | undefined {
  return (catalogs as unknown as { createCatalogRemovalPolling?: CreateCatalogRemovalPolling }).createCatalogRemovalPolling;
}

describe("catalog reducers", () => {
  it("cancels channel deletion without calling the backend", async () => {
    const requestDeletion = deletionRequestHelper();
    expect(requestDeletion).toBeTypeOf("function");
    if (!requestDeletion) return;

    const deleteCatalogEntries = vi.fn<DeleteCatalogEntries>();
    const result = await requestDeletion({
      catalog: "outbound",
      channelIDs: ["one", "two"],
      confirmationMessage: "Delete selected channels?",
      confirm: () => false,
      deleteCatalogEntries
    });

    expect(result).toBeNull();
    expect(deleteCatalogEntries).not.toHaveBeenCalled();
  });

  it("deletes the selected catalog rows only after confirmation", async () => {
    const requestDeletion = deletionRequestHelper();
    expect(requestDeletion).toBeTypeOf("function");
    if (!requestDeletion) return;

    const rows = [{ id: "remaining" }];
    const deleteCatalogEntries = vi.fn<DeleteCatalogEntries>().mockResolvedValue(rows);
    await expect(requestDeletion({
      catalog: "scout",
      channelIDs: ["one", "two"],
      confirmationMessage: "Delete selected channels?",
      confirm: async () => true,
      deleteCatalogEntries
    })).resolves.toEqual(rows);
    expect(deleteCatalogEntries).toHaveBeenCalledWith("scout", ["one", "two"]);
  });

  it("polls only while a catalog still has rows being removed", async () => {
    vi.useFakeTimers();
    const createPolling = removalPollingHelper();
    expect(createPolling).toBeTypeOf("function");
    if (!createPolling) return;

    const load = vi.fn().mockResolvedValue([{ id: "remaining" }]);
    const onRows = vi.fn();
    const dispose = createPolling({
      rows: [{ status: "removing" }],
      load,
      onRows,
      onError: () => undefined
    });

    await vi.advanceTimersByTimeAsync(1_999);
    expect(load).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    expect(load).toHaveBeenCalledTimes(1);
    expect(onRows).toHaveBeenCalledWith([{ id: "remaining" }]);
    dispose();

    const noPolling = createPolling({
      rows: [{ status: "ready" }],
      load,
      onRows,
      onError: () => undefined
    });
    await vi.advanceTimersByTimeAsync(2_000);
    expect(load).toHaveBeenCalledTimes(1);
    noPolling();
    vi.useRealTimers();
  });

  it("does not start a second load while the previous request is pending", async () => {
    vi.useFakeTimers();
    const createPolling = removalPollingHelper();
    expect(createPolling).toBeTypeOf("function");
    if (!createPolling) return;

    const requests = [deferred<unknown[]>(), deferred<unknown[]>()];
    let requestIndex = 0;
    const load = vi.fn(() => requests[requestIndex++].promise);
    const onRows = vi.fn();
    const dispose = createPolling({
      rows: [{ status: "removing" }],
      load,
      onRows,
      onError: () => undefined
    });

    await vi.advanceTimersByTimeAsync(2_000);
    expect(load).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(10_000);
    expect(load).toHaveBeenCalledTimes(1);

    requests[0].resolve([{ id: "fresh" }]);
    await vi.advanceTimersByTimeAsync(0);
    expect(onRows).toHaveBeenCalledWith([{ id: "fresh" }]);

    await vi.advanceTimersByTimeAsync(2_000);
    expect(load).toHaveBeenCalledTimes(2);
    dispose();
    requests[1].resolve([{ id: "stale" }]);
    await vi.advanceTimersByTimeAsync(0);
    expect(onRows).toHaveBeenCalledTimes(1);
    vi.useRealTimers();
  });

  it("normalizes and deduplicates Telegram links", () => {
    expect(uniqueTelegramLinks([" @One ", "https://t.me/one/", "t.me/two", "invalid.example/three"])).toEqual([
      "https://t.me/One",
      "https://t.me/two"
    ]);
  });

  it("preserves an explicit discussion target but discards unrelated fragments", () => {
    expect(uniqueTelegramLinks([
      "https://t.me/+mgppYi2XAIMxYjFi#tc-discussion=4291488698",
      "https://t.me/+mgppYi2XAIMxYjFi#tracking"
    ])).toEqual([
      "https://t.me/+mgppYi2XAIMxYjFi#tc-discussion=4291488698",
      "https://t.me/+mgppYi2XAIMxYjFi"
    ]);
  });

  it("rejects a blank bulk topic", () => {
    expect(applyBulkTopic(["https://t.me/one"], "  ")).toEqual({
      links: [],
      topic: "",
      error: "topic_required"
    });
  });

  it("applies one trimmed topic to all unique bulk links", () => {
    expect(applyBulkTopic(["@one", "https://t.me/two", "@one"], "  Личные финансы  ")).toEqual({
      links: ["https://t.me/one", "https://t.me/two"],
      topic: "Личные финансы"
    });
  });

  it("builds case-insensitive existing-topic autocomplete", () => {
    expect(existingTopics(["Маркетинг", " личные финансы ", "маркетинг", ""])).toEqual([
      "Маркетинг",
      "личные финансы"
    ]);
  });

  it("toggles checked rows without changing the input set", () => {
    const selected = new Set(["one"]);
    expect([...toggleSelected(selected, "two")]).toEqual(["one", "two"]);
    expect([...selected]).toEqual(["one"]);
    expect([...toggleSelected(selected, "one")]).toEqual([]);
  });
});
