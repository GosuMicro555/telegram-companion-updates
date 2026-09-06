import { readFileSync } from "node:fs";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, describe, expect, test, vi } from "vitest";
import {
  createLiveSelectionImportController,
  createLiveSelectionGestureController,
  createLiveStatisticsPolling,
  createLiveStatisticsRequestController,
  isLiveSelectionPhraseEligible,
  liveStatisticsExportColumns,
  LiveStatisticsViewContent,
  normalizeLiveSelectionPhrase,
  resolveLiveStatisticsSelection,
  setupLiveStatisticsRequests
} from "./LiveStatisticsView";
import * as liveStatisticsViewModule from "./LiveStatisticsView";
import { messages } from "./i18n";
import type { LiveDeliveryPage } from "./liveStatistics";

class VisibilityDocument {
  visibilityState: "visible" | "hidden" = "visible";
  private listeners = new Set<EventListener>();

  addEventListener(type: "visibilitychange", listener: EventListener) {
    if (type === "visibilitychange") this.listeners.add(listener);
  }

  removeEventListener(type: "visibilitychange", listener: EventListener) {
    if (type === "visibilitychange") this.listeners.delete(listener);
  }

  changeVisibility(next: "visible" | "hidden") {
    this.visibilityState = next;
    this.listeners.forEach((listener) => listener(new Event("visibilitychange")));
  }
}

const page: LiveDeliveryPage = {
  rows: [{
    id: "audit-1", sourceMessage: "Need help", triggerCanonicalID: null, triggerSnapshot: "credit",
    triggeredAt: "2026-07-15T07:00:00Z", deliveryType: "private_message", accountTitleSnapshot: "Operator",
    finalStatus: "successful", errorCode: "", finalizedAt: "2026-07-15T07:00:04Z"
  }],
  total: 1,
  databaseBytes: 2_621_440,
  refreshedAt: "2026-07-15T07:01:00Z"
};
const addedImportResult = { added: 1, skipped: 0, updated: 0 };
const importFeedback = {
  error: "The selected phrase was not added.",
  success: "Added to negative keywords."
};

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, reject, resolve };
}

afterEach(() => vi.useRealTimers());

test("normalizes selected phrase whitespace without Unicode composition or letter folding", () => {
  expect(normalizeLiveSelectionPhrase("\u00a0CREDIT\u00a0\u00a0card\t")).toBe("credit card");
  expect(normalizeLiveSelectionPhrase("\u0415\u0308\u0436 \u0451\u043b\u043a\u0430")).toBe("\u0435\u0308\u0436 \u0451\u043b\u043a\u0430");
});

test("accepts selected phrases at the 8-word and 120-code-point boundaries", () => {
  const eightWordsAnd120CodePoints = `${"a ".repeat(7)}${"x".repeat(106)}`;

  expect(Array.from(eightWordsAnd120CodePoints)).toHaveLength(120);
  expect(isLiveSelectionPhraseEligible(eightWordsAnd120CodePoints)).toBe(true);
  expect(isLiveSelectionPhraseEligible("\ud83d\ude00".repeat(120))).toBe(true);
});

test("rejects selected phrases over 8 words or 120 Unicode code points", () => {
  const nineWords = `${"a ".repeat(8)}a`;
  const eightWordsAnd121CodePoints = `${"a ".repeat(7)}${"x".repeat(107)}`;

  expect(isLiveSelectionPhraseEligible(nineWords)).toBe(false);
  expect(isLiveSelectionPhraseEligible(eightWordsAnd121CodePoints)).toBe(false);
  expect(isLiveSelectionPhraseEligible("\ud83d\ude00".repeat(121))).toBe(false);
});

function selectionCell(messageId: string) {
  const cell = {
    nodeType: 1,
    parentElement: null,
    closest: (selector: string) => selector === "[data-message-id]" ? cell : null,
    getAttribute: (name: string) => name === "data-message-id" ? messageId : null
  };
  return cell;
}

function nestedTextNode(cell: ReturnType<typeof selectionCell>) {
  const nested = {
    nodeType: 1,
    parentElement: cell,
    closest: (selector: string) => selector === "[data-message-id]" ? cell : null
  };
  return { nodeType: 3, parentElement: nested };
}

function selectionFor(
  text: string,
  startContainer: object,
  endContainer: object,
  options: { collapsed?: boolean; rect?: Partial<DOMRect> } = {}
) {
  const rect = {
    bottom: 60, height: 20, left: 20, right: 90, top: 40, width: 70,
    x: 20, y: 40, toJSON: () => ({}),
    ...options.rect
  };
  const range = {
    collapsed: options.collapsed ?? false,
    endContainer,
    getBoundingClientRect: () => rect,
    startContainer
  };
  return {
    getRangeAt: () => range,
    isCollapsed: options.collapsed ?? false,
    rangeCount: 1,
    toString: () => text
  } as unknown as Selection;
}

test("resolves nested text-node endpoints inside one source-message cell", () => {
  const cell = selectionCell("audit-1");
  const selection = selectionFor("  CREDIT\u00a0 card ", nestedTextNode(cell), nestedTextNode(cell));

  expect(resolveLiveStatisticsSelection(selection, { clientX: 30, clientY: 40 })).toEqual({
    messageId: "audit-1",
    position: { x: 38, y: 48 },
    text: "credit card"
  });
});

test("ignores collapsed and whitespace-only ranges", () => {
  const cell = selectionCell("audit-1");

  expect(resolveLiveStatisticsSelection(
    selectionFor("credit", nestedTextNode(cell), nestedTextNode(cell), { collapsed: true }),
    { clientX: 0, clientY: 0 }
  )).toBeUndefined();
  expect(resolveLiveStatisticsSelection(
    selectionFor("\u00a0 \t", nestedTextNode(cell), nestedTextNode(cell)),
    { clientX: 0, clientY: 0 }
  )).toBeUndefined();
});

test("ignores selections that cross source-message cells", () => {
  const startCell = selectionCell("audit-1");
  const endCell = selectionCell("audit-2");

  expect(resolveLiveStatisticsSelection(
    selectionFor("credit card", nestedTextNode(startCell), nestedTextNode(endCell)),
    { clientX: 0, clientY: 0 }
  )).toBeUndefined();
});

test("uses stable fallback coordinates for an empty range rectangle", () => {
  const cell = selectionCell("audit-1");
  const emptyRect = { bottom: 0, height: 0, left: 0, right: 0, top: 0, width: 0, x: 0, y: 0 };

  expect(resolveLiveStatisticsSelection(
    selectionFor("credit", nestedTextNode(cell), nestedTextNode(cell), { rect: emptyRect }),
    { clientX: 0, clientY: 0 }
  )?.position).toEqual({ x: 8, y: 8 });
});

test("imports the frozen phrase as one negative canonical and clears only on success", async () => {
  const imported = vi.fn(async () => addedImportResult);
  const busy: boolean[] = [];
  const errors: string[] = [];
  const onSuccess = vi.fn();
  const controller = createLiveSelectionImportController({
    importKeywords: imported,
    onBusy: (value) => busy.push(value),
    onError: (value) => errors.push(value),
    onSuccess
  });
  const snapshot = { messageId: "audit-1", position: { x: 20, y: 30 }, text: "credit card" };

  await expect(controller.submit(snapshot, importFeedback)).resolves.toBe(true);
  expect(imported).toHaveBeenCalledWith([{ canonical: "credit card", forms: [] }], "negative");
  expect(busy).toEqual([true, false]);
  expect(errors).toEqual([""]);
  expect(onSuccess).toHaveBeenCalledWith(snapshot, importFeedback.success);
});

test("keeps the selection open when a resolved import only reports skipped entries", async () => {
  const onSuccess = vi.fn();
  const errors: string[] = [];
  const snapshot = { messageId: "audit-1", position: { x: 20, y: 30 }, text: "credit card" };
  const controller = createLiveSelectionImportController({
    importKeywords: async () => ({ added: 0, skipped: 1, updated: 0 }),
    onBusy: () => undefined,
    onError: (value) => errors.push(value),
    onSuccess
  });

  await expect(controller.submit(snapshot, importFeedback)).resolves.toBe(false);
  expect(onSuccess).not.toHaveBeenCalled();
  expect(errors).toEqual(["", importFeedback.error]);
  expect(snapshot.text).toBe("credit card");
});

test("accepts a partial selection import when an existing canonical was updated", async () => {
  const onSuccess = vi.fn();
  const errors: string[] = [];
  const snapshot = { messageId: "audit-1", position: { x: 20, y: 30 }, text: "credit card" };
  const controller = createLiveSelectionImportController({
    importKeywords: async () => ({ added: 0, skipped: 1, updated: 1 }),
    onBusy: () => undefined,
    onError: (value) => errors.push(value),
    onSuccess
  });

  await expect(controller.submit(snapshot, importFeedback)).resolves.toBe(true);
  expect(onSuccess).toHaveBeenCalledWith(snapshot, importFeedback.success);
  expect(errors).toEqual([""]);
});

test("keeps one selection import in flight at a time", async () => {
  const request = deferred<typeof addedImportResult>();
  const imported = vi.fn(() => request.promise);
  const controller = createLiveSelectionImportController({
    importKeywords: imported,
    onBusy: () => undefined,
    onError: () => undefined,
    onSuccess: () => undefined
  });
  const snapshot = { messageId: "audit-1", position: { x: 20, y: 30 }, text: "credit" };

  const first = controller.submit(snapshot, importFeedback);
  await expect(controller.submit(snapshot, importFeedback)).resolves.toBe(false);
  expect(imported).toHaveBeenCalledTimes(1);
  request.resolve(addedImportResult);
  await expect(first).resolves.toBe(true);
});

test("preserves the frozen selection and reports a local import failure", async () => {
  const onSuccess = vi.fn();
  const errors: string[] = [];
  const snapshot = { messageId: "audit-1", position: { x: 20, y: 30 }, text: "credit card" };
  const controller = createLiveSelectionImportController({
    importKeywords: async () => { throw new Error("Import failed"); },
    onBusy: () => undefined,
    onError: (value) => errors.push(value),
    onSuccess
  });

  await expect(controller.submit(snapshot, importFeedback)).resolves.toBe(false);
  expect(onSuccess).not.toHaveBeenCalled();
  expect(errors).toEqual(["", "Import failed"]);
  expect(snapshot.text).toBe("credit card");
});

test("does not publish a late import completion after disposal", async () => {
  const request = deferred<typeof addedImportResult>();
  const busy: boolean[] = [];
  const errors: string[] = [];
  const onSuccess = vi.fn();
  const controller = createLiveSelectionImportController({
    importKeywords: () => request.promise,
    onBusy: (value) => busy.push(value),
    onError: (value) => errors.push(value),
    onSuccess
  });
  const snapshot = { messageId: "audit-1", position: { x: 20, y: 30 }, text: "credit card" };

  const submission = controller.submit(snapshot, importFeedback);
  controller.dispose();
  request.resolve(addedImportResult);

  await expect(submission).resolves.toBe(false);
  expect(onSuccess).not.toHaveBeenCalled();
  expect(errors).toEqual([""]);
  expect(busy).toEqual([true]);
});

test("distinguishes one source selection gesture from ordinary clicks", () => {
  const cell = selectionCell("audit-1");
  const snapshots: unknown[] = [];
  const onClear = vi.fn();
  const controller = createLiveSelectionGestureController({
    onClear,
    onSnapshot: (snapshot) => snapshots.push(snapshot)
  });
  const preventDefault = vi.fn();
  const stopPropagation = vi.fn();

  expect(controller.handleMouseUp(
    selectionFor("credit card", nestedTextNode(cell), nestedTextNode(cell)),
    { clientX: 30, clientY: 40 }
  )).toBe(true);
  expect(snapshots).toHaveLength(1);
  expect(controller.handleClick(nestedTextNode(cell) as unknown as Node, { preventDefault, stopPropagation })).toBe(true);
  expect(preventDefault).toHaveBeenCalledTimes(1);
  expect(stopPropagation).toHaveBeenCalledTimes(1);
  expect(controller.handleClick(nestedTextNode(cell) as unknown as Node, { preventDefault, stopPropagation })).toBe(false);

  const ordinaryController = createLiveSelectionGestureController({ onClear, onSnapshot: () => undefined });
  expect(ordinaryController.handleClick(nestedTextNode(cell) as unknown as Node, { preventDefault, stopPropagation })).toBe(false);
});

describe("live statistics polling", () => {
  test("fetches immediately, pauses while hidden, resumes on visibility, and cleans up", () => {
    vi.useFakeTimers();
    const document = new VisibilityDocument();
    const refresh = vi.fn();
    const dispose = createLiveStatisticsPolling(document, refresh);

    expect(refresh).toHaveBeenCalledTimes(1);
    vi.advanceTimersByTime(5000);
    expect(refresh).toHaveBeenCalledTimes(2);

    document.changeVisibility("hidden");
    vi.advanceTimersByTime(10000);
    expect(refresh).toHaveBeenCalledTimes(2);

    document.changeVisibility("visible");
    expect(refresh).toHaveBeenCalledTimes(3);
    vi.advanceTimersByTime(5000);
    expect(refresh).toHaveBeenCalledTimes(4);

    dispose();
    vi.advanceTimersByTime(10000);
    expect(refresh).toHaveBeenCalledTimes(4);
  });

  test("does not supersede a page request that remains pending beyond five seconds", async () => {
    vi.useFakeTimers();
    const document = new VisibilityDocument();
    const requests = [deferred<string>(), deferred<string>()];
    let requestIndex = 0;
    const fetchPage = vi.fn(() => requests[requestIndex++].promise);
    const controller = createLiveStatisticsRequestController({
      fetchPage,
      deleteTrigger: async () => undefined,
      onPage: () => undefined,
      onError: () => undefined,
      onLoading: () => undefined
    });
    const dispose = createLiveStatisticsPolling(document, () => { void controller.refresh({ page: 1 }); });

    expect(fetchPage).toHaveBeenCalledTimes(1);
    vi.advanceTimersByTime(10_000);
    expect(fetchPage).toHaveBeenCalledTimes(1);

    const first = controller.refresh({ page: 1 });
    requests[0].resolve("page-1");
    await first;
    vi.advanceTimersByTime(5000);
    expect(fetchPage).toHaveBeenCalledTimes(2);

    requests[1].resolve("page-2");
    await controller.refresh({ page: 1 });
    dispose();
  });

  test("effect replay creates a fresh request controller after cleanup", async () => {
    vi.useFakeTimers();
    const document = new VisibilityDocument();
    const requests = [deferred<string>(), deferred<string>()];
    const pages: string[] = [];
    let requestIndex = 0;
    const dependencies = {
      fetchPage: () => requests[requestIndex++].promise,
      deleteTrigger: async () => undefined,
      onPage: (page: string) => pages.push(page),
      onError: () => undefined,
      onLoading: () => undefined
    };

    const firstSetup = setupLiveStatisticsRequests(document, { page: 1 }, dependencies);
    const firstRequest = firstSetup.controller.refresh({ page: 1 });
    firstSetup.dispose();
    requests[0].resolve("disposed-page");
    await firstRequest;

    const secondSetup = setupLiveStatisticsRequests(document, { page: 1 }, dependencies);
    const secondRequest = secondSetup.controller.refresh({ page: 1 });
    requests[1].resolve("current-page");
    await secondRequest;

    expect(pages).toEqual(["current-page"]);
    secondSetup.dispose();
  });
});

describe("live statistics request controller", () => {
  test("applies only the newest page when requests complete out of order", async () => {
    const requests = [deferred<string>(), deferred<string>()];
    const state = { page: "existing", error: "previous", loading: false };
    let requestIndex = 0;
    const controller = createLiveStatisticsRequestController({
      fetchPage: () => requests[requestIndex++].promise,
      deleteTrigger: async () => undefined,
      onPage: (next) => { state.page = next; },
      onError: (error) => { state.error = error; },
      onLoading: (loading) => { state.loading = loading; }
    });

    const first = controller.refresh({ page: 1 });
    controller.invalidate();
    const second = controller.refresh({ page: 2 });
    requests[1].resolve("page-2");
    await second;
    requests[0].resolve("stale-page-1");
    await first;

    expect(state).toEqual({ page: "page-2", error: "", loading: false });
  });

  test("ignores a stale error after a newer request succeeds", async () => {
    const requests = [deferred<string>(), deferred<string>()];
    const state = { page: "existing", error: "", loading: false };
    let requestIndex = 0;
    const controller = createLiveStatisticsRequestController({
      fetchPage: () => requests[requestIndex++].promise,
      deleteTrigger: async () => undefined,
      onPage: (next) => { state.page = next; },
      onError: (error) => { state.error = error; },
      onLoading: (loading) => { state.loading = loading; }
    });

    const first = controller.refresh({ page: 1 });
    controller.invalidate();
    const second = controller.refresh({ page: 2 });
    requests[1].resolve("page-2");
    await second;
    requests[0].reject(new Error("stale failure"));
    await first;

    expect(state).toEqual({ page: "page-2", error: "", loading: false });
  });

  test("invalidates an older poll and refreshes the same page after deletion", async () => {
    const requests = [deferred<string>(), deferred<string>()];
    const queries: Array<{ page: number }> = [];
    const deleted: string[] = [];
    const state = { page: "existing", error: "", loading: false };
    const controller = createLiveStatisticsRequestController({
      fetchPage: (query: { page: number }) => {
        queries.push(query);
        return requests[queries.length - 1].promise;
      },
      deleteTrigger: async (id) => { deleted.push(id); },
      onPage: (next) => { state.page = next; },
      onError: (error) => { state.error = error; },
      onLoading: (loading) => { state.loading = loading; }
    });

    const poll = controller.refresh({ page: 3 });
    const deletion = controller.deleteAndRefresh("trigger-7", { page: 3 });
    await Promise.resolve();
    expect(deleted).toEqual(["trigger-7"]);
    expect(queries).toEqual([{ page: 3 }, { page: 3 }]);

    requests[1].resolve("post-delete-page-3");
    await deletion;
    requests[0].resolve("stale-pre-delete-page-3");
    await poll;

    expect(state).toEqual({ page: "post-delete-page-3", error: "", loading: false });
  });

  test("awaits authoritative keyword refresh before refreshing the live page", async () => {
    const keywordRefresh = deferred<void>();
    const events: string[] = [];
    const controller = createLiveStatisticsRequestController({
      fetchPage: async () => { events.push("page"); return "post-delete"; },
      deleteTrigger: async () => { events.push("delete"); },
      onTriggerDeleted: async () => { events.push("keywords:start"); await keywordRefresh.promise; events.push("keywords:done"); },
      runTriggerDeletion: async (operation) => { events.push("exclusive:start"); await operation(); events.push("exclusive:done"); },
      onPage: () => undefined,
      onError: () => undefined,
      onLoading: () => undefined
    });

    const deletion = controller.deleteAndRefresh("trigger-7", { page: 1 });
    await Promise.resolve();

    expect(events).toEqual(["exclusive:start", "delete", "keywords:start"]);
    keywordRefresh.resolve();
    await deletion;

    expect(events).toEqual(["exclusive:start", "delete", "keywords:start", "keywords:done", "exclusive:done", "page"]);
  });

  test("does not refresh keyword settings when trigger deletion fails", async () => {
    const onTriggerDeleted = vi.fn();
    const fetchPage = vi.fn(async () => "page");
    const controller = createLiveStatisticsRequestController({
      fetchPage,
      deleteTrigger: async () => { throw new Error("delete failed"); },
      onTriggerDeleted,
      onPage: () => undefined,
      onError: () => undefined,
      onLoading: () => undefined
    });

    await controller.deleteAndRefresh("trigger-7", { page: 1 });

    expect(onTriggerDeleted).not.toHaveBeenCalled();
    expect(fetchPage).not.toHaveBeenCalled();
  });

  test.each(["invalidate", "dispose"] as const)("refreshes authoritative keywords after successful deletion despite %s", async (mode) => {
    const deletionRequest = deferred<void>();
    const onTriggerDeleted = vi.fn(async () => undefined);
    const fetchPage = vi.fn(async () => "page");
    const controller = createLiveStatisticsRequestController({
      fetchPage,
      deleteTrigger: () => deletionRequest.promise,
      onTriggerDeleted,
      onPage: () => undefined,
      onError: () => undefined,
      onLoading: () => undefined
    });

    const deletion = controller.deleteAndRefresh("trigger-7", { page: 1 });
    if (mode === "invalidate") controller.invalidate();
    else controller.dispose();
    deletionRequest.resolve();
    await deletion;

    expect(onTriggerDeleted).toHaveBeenCalledTimes(1);
    expect(fetchPage).not.toHaveBeenCalled();
  });

  test("does not let a polling tick supersede an in-flight delete refresh", async () => {
    const deletionRequest = deferred<void>();
    const pageRequest = deferred<string>();
    const queries: Array<{ page: number }> = [];
    const state = { page: "existing", error: "", loading: false };
    const controller = createLiveStatisticsRequestController({
      fetchPage: (query: { page: number }) => {
        queries.push(query);
        return pageRequest.promise;
      },
      deleteTrigger: () => deletionRequest.promise,
      onPage: (next) => { state.page = next; },
      onError: (error) => { state.error = error; },
      onLoading: (loading) => { state.loading = loading; }
    });

    const deletion = controller.deleteAndRefresh("trigger-9", { page: 5 });
    const poll = controller.refresh({ page: 5 });
    await Promise.resolve();
    expect(queries).toEqual([]);
    await poll;

    deletionRequest.resolve();
    await Promise.resolve();
    expect(queries).toEqual([{ page: 5 }]);
    pageRequest.resolve("post-delete-page-5");
    await deletion;

    expect(state).toEqual({ page: "post-delete-page-5", error: "", loading: false });
  });

  test("preserves the current page and exposes the latest request error", async () => {
    const request = deferred<string>();
    const state = { page: "last-good-page", error: "", loading: false };
    const controller = createLiveStatisticsRequestController({
      fetchPage: () => request.promise,
      deleteTrigger: async () => undefined,
      onPage: (next) => { state.page = next; },
      onError: (error) => { state.error = error; },
      onLoading: (loading) => { state.loading = loading; }
    });

    const refresh = controller.refresh({ page: 4 });
    request.reject(new Error("poll failed"));
    await refresh;

    expect(state).toEqual({ page: "last-good-page", error: "poll failed", loading: false });
  });

  test("does not publish request completion after disposal", async () => {
    const request = deferred<string>();
    const events: string[] = [];
    const controller = createLiveStatisticsRequestController({
      fetchPage: () => request.promise,
      deleteTrigger: async () => undefined,
      onPage: (next) => events.push(`page:${next}`),
      onError: (error) => events.push(`error:${error}`),
      onLoading: (loading) => events.push(`loading:${loading}`)
    });

    const refresh = controller.refresh({ page: 1 });
    controller.dispose();
    request.resolve("late-page");
    await refresh;

    expect(events).toEqual(["loading:true"]);
  });
});

test("renders the paged live delivery table with shared date-time fields and deleted snapshots", () => {
  const markup = renderToStaticMarkup(<LiveStatisticsViewContent locale="en" onDeleteTrigger={() => undefined} onExport={() => undefined} page={page} timeZone="Europe/Moscow" />);

  expect(markup).not.toContain('type="datetime-local"');
  expect(markup.match(/class="dateTimeField"/g)).toHaveLength(2);
  expect(markup).toContain('aria-haspopup="dialog"');
  expect(markup).toContain("50");
  expect(markup).toContain("1000");
  expect(markup).toContain("Source message");
  expect(markup).toContain("Trigger");
  expect(markup).toContain("Type");
  expect(markup).toContain("Date");
  expect(markup).toContain("Time");
  expect(markup).toContain("Telegram account");
  expect(markup).toContain("Status");
  expect(markup).toContain("Select start date");
  expect(markup).toContain("Select end date");
  expect(markup).toContain("Live statistics data");
  expect(markup).toContain("Export to file");
  expect(markup).toContain("Need help");
  expect(markup).toContain('data-message-id="audit-1"');
  expect(markup).toContain("credit");
  expect(markup).toContain("Deleted");
  expect(markup).not.toContain("Delete trigger");
  expect(markup).toContain("2.5 MB");
  expect(markup).toContain("07/15/2026");
  expect(markup).toContain("10:00:00");
  expect(markup).toContain('aria-label="Live delivery statistics"');
  expect(markup).toContain('stats.live.table.v1');
});

test("marks newly arrived rows with a lightweight reduced-motion-safe animation", () => {
  const markup = renderToStaticMarkup(
    <LiveStatisticsViewContent
      locale="en"
      newRowIDs={new Set(["audit-1"])}
      onDeleteTrigger={() => undefined}
      page={page}
      timeZone="Europe/Moscow"
    />
  );
  const css = readFileSync(new URL("./styles.css", import.meta.url), "utf8");

  expect(markup).toContain("configurableStatsTable__row--new");
  expect(css).toMatch(/\.liveStatsWorkspace\s+\.configurableStatsTable__row--new\s*\{[^}]*animation:/s);
  expect(css).toMatch(/@media\s*\(prefers-reduced-motion:\s*reduce\)[\s\S]*\.configurableStatsTable__row--new[\s\S]*animation:\s*none/s);
});

test("renders the frozen selection action with its local busy error state", () => {
  const markup = renderToStaticMarkup(
    <LiveStatisticsViewContent
      locale="en"
      onDeleteTrigger={() => undefined}
      onSelectionAction={() => undefined}
      onSelectionClose={() => undefined}
      page={page}
      selection={{ messageId: "audit-1", position: { x: 40, y: 60 }, text: "credit card" }}
      selectionBusy
      selectionError="Import failed"
      timeZone="Europe/Moscow"
    />
  );

  expect(markup).toContain("Add to negative keywords");
  expect(markup).toContain('aria-label="Add selected text to negative keywords"');
  expect(markup).toContain("Import failed");
  expect(markup).toContain('role="alert"');
  expect(markup).toContain("disabled");
  expect(markup).toContain("left:40px;top:60px");
});

test("renders completed selection feedback as an accessible status", () => {
  const markup = renderToStaticMarkup(
    <LiveStatisticsViewContent
      locale="en"
      onDeleteTrigger={() => undefined}
      page={page}
      selectionStatus="Added to negative keywords."
      timeZone="Europe/Moscow"
    />
  );

  expect(markup).toContain(
    '<div class="liveStatsSelectionFeedback" role="status">Added to negative keywords.</div>'
  );
  expect(markup).not.toContain('role="alert"');
});

test("defines complete Russian and English live-selection feedback", () => {
  expect({
    en: {
      error: messages.en.liveSelectionImportRejected,
      success: messages.en.liveSelectionImportSuccess
    },
    ru: {
      error: messages.ru.liveSelectionImportRejected,
      success: messages.ru.liveSelectionImportSuccess
    }
  }).toEqual({
    en: {
      error: "The selected phrase was not added.",
      success: "Added to negative keywords."
    },
    ru: {
      error: "\u0412\u044b\u0434\u0435\u043b\u0435\u043d\u043d\u0430\u044f \u0444\u0440\u0430\u0437\u0430 \u043d\u0435 \u0434\u043e\u0431\u0430\u0432\u043b\u0435\u043d\u0430.",
      success: "\u0414\u043e\u0431\u0430\u0432\u043b\u0435\u043d\u043e \u0432 \u043d\u0435\u0433\u0430\u0442\u0438\u0432\u043d\u044b\u0435 \u043a\u043b\u044e\u0447\u0435\u0432\u044b\u0435 \u0441\u043b\u043e\u0432\u0430."
    }
  });
});

test("exports visible live columns in their saved table order", () => {
  const storage = {
    getItem: () => JSON.stringify({
      order: ["status", "trigger", "sourceMessage", "date", "time", "type", "account"],
      visible: ["status", "sourceMessage", "date"],
      widths: {}
    }),
    setItem: () => undefined
  };

  expect(liveStatisticsExportColumns(storage)).toEqual(["status", "sourceMessage", "date"]);
});

test("defaults the live range to 24 hours before and after now", () => {
  const range = (liveStatisticsViewModule as typeof liveStatisticsViewModule & {
    liveStatisticsDateTimeRange?: (now: Date) => { from: string; to: string };
  }).liveStatisticsDateTimeRange;

  expect(range).toBeTypeOf("function");
  if (!range) return;
  expect(range(new Date(2026, 6, 17, 10, 15))).toEqual({
    from: "2026-07-16T10:15",
    to: "2026-07-18T10:15"
  });
});

test("renders the trigger delete control before the trigger word", () => {
  const activePage: LiveDeliveryPage = {
    ...page,
    rows: [{ ...page.rows[0], triggerCanonicalID: "credit-id", triggerSnapshot: "credit" }]
  };
  const markup = renderToStaticMarkup(<LiveStatisticsViewContent locale="en" onDeleteTrigger={() => undefined} page={activePage} timeZone="Europe/Moscow" />);
  const triggerCell = markup.slice(markup.indexOf('class="liveStatsTrigger"'));

  expect(triggerCell.indexOf("lucide-x")).toBeGreaterThanOrEqual(0);
  expect(triggerCell.indexOf("lucide-x")).toBeLessThan(triggerCell.indexOf("credit"));
});

test("renders the descriptive total messages label in Russian", () => {
  const markup = renderToStaticMarkup(<LiveStatisticsViewContent locale="ru" onDeleteTrigger={() => undefined} page={page} timeZone="Europe/Moscow" />);

  expect(markup).toContain("Всего сообщений");
});

test("keeps compact calendar typography and requested live toolbar spacing", () => {
  const css = readFileSync(new URL("./styles.css", import.meta.url), "utf8");

  expect(css).toMatch(/\.liveStatsDateRange\s*\{[^}]*margin-right:\s*20px/s);
  expect(css).toMatch(/\.liveStatsMetrics\s*>\s*div:last-child\s*\{[^}]*margin-left:\s*20px/s);
  expect(css).toMatch(/\.liveStatsDateRange\s+\.dateTimeField\s*\{[^}]*font-size:\s*80%/s);
  expect(css).toMatch(/\.liveStatsTrigger\s*\{[^}]*gap:\s*6px/s);
  expect(css).toMatch(/\.liveStatsWorkspace\s+\.configurableStatsTable__row\s*>\s*div\s*\{[^}]*font-weight:\s*400/s);
  expect(css).toMatch(/\.liveStatsWorkspace\s+\.configurableStatsTable__row\s+strong,[^}]*\.liveStatsStatus\s*\{[^}]*font-weight:\s*inherit/s);
  expect(css).toMatch(/\.liveStatsStatus--success\s*\{[^}]*color:\s*var\(--success\)/s);
  expect(css).toMatch(/\.liveStatsStatus--error\s*\{[^}]*color:\s*var\(--danger\)/s);
});

test("renders exact Russian deleted and final-status presentation", () => {
  const russianPage: LiveDeliveryPage = {
    ...page,
    rows: [
      page.rows[0],
      { ...page.rows[0], id: "audit-2", triggerCanonicalID: "loan-id", triggerSnapshot: "loan", finalStatus: "not_delivered", errorCode: "send_failed" }
    ],
    total: 2
  };
  const russian = renderToStaticMarkup(<LiveStatisticsViewContent locale="ru" onDeleteTrigger={() => undefined} page={russianPage} timeZone="Europe/Moscow" />);
  const english = renderToStaticMarkup(<LiveStatisticsViewContent locale="en" onDeleteTrigger={() => undefined} page={russianPage} timeZone="America/New_York" />);

  expect(russian).toContain("Удалён");
  expect(russian).toContain("Успешно");
  expect(russian).toContain("Не доставлено");
  expect(russian).toContain("Выбор даты начала");
  expect(russian).toContain("Выбор даты окончания");
  expect(russian).toContain("Данные Live-статистики");
  expect(russian).toContain('class="liveStatsStatus liveStatsStatus--success"');
  expect(russian).toContain('class="liveStatsStatus liveStatsStatus--error"');
  expect(russian).toContain("lucide-x");
  expect(russian).not.toContain("lucide-trash-2");
  expect(english).toContain("Deleted");
  expect(english).toContain("Delivered");
  expect(english).toContain("Not delivered");
});
