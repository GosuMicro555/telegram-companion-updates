import { describe, expect, test } from "vitest";
import { readFileSync } from "node:fs";
import {
  activateImportedPositiveCanons,
  analyticsStatsFromStatus,
  cycleConfirmationFocusIndex,
  refreshKeywordSettingsAuthoritatively
} from "./App";
import { messages } from "./i18n";
import {
  createLatestKeywordSettingsSaveQueue,
  type KeywordSettingsSnapshot
} from "./keywordSettingsSaveQueue";

test("activates imported positive canons as keyword triggers without duplicates", () => {
  expect(activateImportedPositiveCanons(
    [{ keyword: "деньги", dm: false }],
    [
      { canonical: "деньги", forms: ["денег"] },
      { canonical: "машина едет", forms: ["машины", "едет"] }
    ]
  )).toEqual([
    { keyword: "деньги", dm: false },
    { keyword: "машина едет", dm: true }
  ]);
});

test("exposes the positive canonical bulk import from the keywords section", () => {
  const appSource = readFileSync(new URL("./App.tsx", import.meta.url), "utf8");

  expect(appSource).toContain("BulkImportCanonicalKeywords");
  expect(appSource).toContain('t(locale, "analyticsBulkAdd")');
  expect(appSource).toContain('className="analyticsRedesign__bulkModal"');
});

test("polls live statistics and marks the counter read only when leaving the section", () => {
  const appSource = readFileSync(new URL("./App.tsx", import.meta.url), "utf8");

  expect(appSource).toContain("createLiveStatisticsUnreadTracker");
  expect(appSource).toContain("GetLiveDeliveryStatistics");
  expect(appSource).toContain("formatLiveStatisticsNavLabel");
  expect(appSource).toContain("liveStatisticsUnreadTracker.markRead()");
  expect(appSource).toContain('previous === "liveStats" && next !== "liveStats"');
  expect(appSource).not.toContain('if (next === "liveStats") setLiveStatisticsUnread');
});

test("adds the localized account rest navigation and settings duration control", () => {
  const appSource = readFileSync(new URL("./App.tsx", import.meta.url), "utf8");

  expect(appSource).toContain('section: "accountRest"');
  expect(appSource).toContain('label: "accountRest"');
  expect(appSource).toContain('<AccountRestView locale={locale} />');
  expect(appSource).toContain('t(locale, "groupRestHours")');
  expect(appSource).toContain('max={720} min={1}');
  expect(appSource).toMatch(
    /joinSettingsSaveGuard\.save\(\{[\s\S]*?groupRestHours:\s*appSettings\.groupRestHours/
  );
});

test("keeps persisted settings editors unavailable until hydration completes", () => {
  const appSource = readFileSync(new URL("./App.tsx", import.meta.url), "utf8");

  expect(appSource).toContain('keywordHydration.phase === "loaded"');
  expect(appSource).toContain('const settingsReady = hydration.phase === "loaded";');
  expect(appSource).toContain('disabled={!settingsReady}');
});

test("guards channel deletion against rapid duplicate requests", () => {
  const appSource = readFileSync(new URL("./App.tsx", import.meta.url), "utf8");

  expect(appSource).toContain("const deletingRef = useRef(false);");
  expect(appSource).toContain("deletingRef.current = true;");
  expect(appSource).toContain("deletingRef.current = false;");
  expect(appSource).toMatch(/if \(tab === "topics" \|\| deleting \|\| deletingRef\.current \|\| selected\.size === 0\) return;/);
});

test("separates channel membership actions from active processing", () => {
  const appSource = readFileSync(new URL("./App.tsx", import.meta.url), "utf8");

  expect(appSource).toContain("JoinCatalogEntry");
  expect(appSource).toContain("LeaveCatalogEntry");
  expect(appSource).toContain('t(locale, "membership")');
  expect(appSource).toContain('t(locale, "join")');
  expect(appSource).toContain('t(locale, "leave")');
  expect(appSource).toMatch(
    /<span>\{t\(locale, "membership"\)\}<\/span>[\s\S]*?<span>\{t\(locale, "active"\)\}<\/span>/
  );
});

test("marks the topic field invalid when links are submitted without a topic", () => {
  const appSource = readFileSync(new URL("./App.tsx", import.meta.url), "utf8");

  expect(appSource).toContain("const [topicInvalid, setTopicInvalid] = useState(false);");
  expect(appSource).toContain("aria-invalid={topicInvalid}");
  expect(appSource).toContain('className={topicInvalid ? "invalid" : ""}');
  expect(appSource).toContain('className="fieldError"');
});

test("uses an in-app confirmation dialog instead of unsupported macOS window.confirm", () => {
  const appSource = readFileSync(new URL("./App.tsx", import.meta.url), "utf8");

  expect(appSource).not.toContain("window.confirm");
  expect(appSource).toContain("<ConfirmationDialog");
  expect(appSource).toContain("confirmInApp");
});

test("keeps confirmation keyboard focus inside cancel and confirm controls", () => {
  expect(cycleConfirmationFocusIndex(0, false)).toBe(1);
  expect(cycleConfirmationFocusIndex(1, false)).toBe(0);
  expect(cycleConfirmationFocusIndex(0, true)).toBe(1);
  expect(cycleConfirmationFocusIndex(1, true)).toBe(0);

  const appSource = readFileSync(new URL("./App.tsx", import.meta.url), "utf8");
  const css = readFileSync(new URL("./styles.css", import.meta.url), "utf8");
  expect(appSource).toContain("event.preventDefault()");
  expect(appSource).toContain("previousFocus?.focus()");
  expect(appSource).toMatch(/<button autoFocus className="secondaryButton"/);
  expect(appSource).not.toMatch(/<button autoFocus className="primaryButton"/);
  expect(appSource).toContain('className="secondaryButton confirmDangerButton"');
  expect(css).toMatch(/\.confirmDangerButton\s*\{[^}]*background:\s*#b42318;[^}]*color:\s*#fff;/s);
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, reject, resolve };
}

const keywordSettings = (keyword: string, sharedReply: string, revision = 0): KeywordSettingsSnapshot => ({
  keywords: [keyword],
  minusKeywords: [],
  sharedReply,
  privateReply: "private reply",
  deliveryMode: "comments",
  directMessageKeywords: [keyword],
  revision
});

describe("keyword settings save queue", () => {
  test("serializes saves and applies only the latest backend response", async () => {
    const requests: KeywordSettingsSnapshot[] = [];
    const responses = [deferred<KeywordSettingsSnapshot>(), deferred<KeywordSettingsSnapshot>()];
    const applied: Array<{ settings: KeywordSettingsSnapshot; version: number }> = [];
    const queue = createLatestKeywordSettingsSaveQueue(
      (settings) => {
        requests.push(settings);
        return responses[requests.length - 1].promise;
      },
      (settings, version) => applied.push({ settings, version })
    );
    const first = keywordSettings("old", "old reply");
    const second = keywordSettings("new", "new reply");

    const firstSave = queue.enqueue(first, 1);
    await Promise.resolve();
    const secondSave = queue.enqueue(second, 2);
    await Promise.resolve();

    expect(requests).toEqual([first]);

    responses[0].resolve(keywordSettings("server-old", "server old reply", 4));
    await firstSave;
    await Promise.resolve();

    expect(requests).toEqual([first, second]);
    expect(applied).toEqual([]);

    const authoritative = keywordSettings("server-new", "server new reply", 5);
    responses[1].resolve(authoritative);
    await secondSave;

    expect(applied).toEqual([{ settings: authoritative, version: 2 }]);
  });

  test("invalidates an in-flight response as soon as settings change", async () => {
    const response = deferred<KeywordSettingsSnapshot>();
    const applied: KeywordSettingsSnapshot[] = [];
    const queue = createLatestKeywordSettingsSaveQueue(
      () => response.promise,
      (settings) => applied.push(settings)
    );

    const save = queue.enqueue(keywordSettings("old", "old reply"), 1);
    await Promise.resolve();
    queue.invalidate();
    response.resolve(keywordSettings("server-old", "server old reply", 2));
    await save;

    expect(applied).toEqual([]);
  });

  test("forwards an independent private reply with the latest save snapshot", async () => {
    const requests: KeywordSettingsSnapshot[] = [];
    const queue = createLatestKeywordSettingsSaveQueue(
      async (settings) => {
        requests.push(settings);
        return settings;
      },
      () => undefined
    );
    const snapshot: KeywordSettingsSnapshot = {
      keywords: ["keyword"],
      minusKeywords: [],
      sharedReply: "comment reply",
      privateReply: "private reply",
      deliveryMode: "both",
      directMessageKeywords: ["keyword"],
      revision: 0
    };

    await queue.enqueue(snapshot, 1);

    expect(requests).toEqual([snapshot]);
  });

  test("runs deletion exclusively after in-flight work and drops stale queued saves", async () => {
    const firstSave = deferred<KeywordSettingsSnapshot>();
    const deletionStarted = deferred<void>();
    const deletionGate = deferred<void>();
    const persisted: string[] = [];
    const events: string[] = [];
    const queue = createLatestKeywordSettingsSaveQueue(
      async (settings) => {
        persisted.push(settings.keywords[0]);
        if (settings.keywords[0] === "in-flight") return firstSave.promise;
        return settings;
      },
      (settings) => events.push(`apply:${settings.keywords[0]}`)
    );

    const inFlight = queue.enqueue(keywordSettings("in-flight", "old reply"), 1);
    await Promise.resolve();
    const queuedBeforeDelete = queue.enqueue(keywordSettings("queued-before", "old reply"), 2);
    const deletion = queue.runExclusive(async () => {
      events.push("delete:start");
      deletionStarted.resolve();
      await deletionGate.promise;
      events.push("delete:done");
    });
    const queuedDuringDelete = queue.enqueue(keywordSettings("queued-during", "old reply"), 3);

    expect(persisted).toEqual(["in-flight"]);
    firstSave.resolve(keywordSettings("in-flight", "old reply", 1));
    await inFlight;
    await deletionStarted.promise;
    expect(events).toEqual(["delete:start"]);

    deletionGate.resolve();
    await deletion;
    await Promise.all([queuedBeforeDelete, queuedDuringDelete]);

    expect(persisted).toEqual(["in-flight"]);
    expect(events).toEqual(["delete:start", "delete:done"]);

    await queue.enqueue(keywordSettings("fresh", "new reply"), 4);
    expect(persisted).toEqual(["in-flight", "fresh"]);
    expect(events).toEqual(["delete:start", "delete:done", "apply:fresh"]);
  });
});

test("refreshes deleted trigger state from authoritative settings including private reply", async () => {
  const events: string[] = [];
  const authoritative = keywordSettings("remaining", "comment reply", 12);

  const result = await refreshKeywordSettingsAuthoritatively({
    invalidatePendingSave: () => events.push("invalidate"),
    load: async () => { events.push("load"); return authoritative; },
    apply: (settings) => events.push(`apply:${settings.privateReply}:${settings.revision}`)
  });

  expect(events).toEqual(["invalidate", "load", "apply:private reply:12"]);
  expect(result).toEqual(authoritative);
});

test("uses separate localized single-line labels for comment and private replies", () => {
  const appSource = readFileSync(new URL("./App.tsx", import.meta.url), "utf8");

  expect(messages.ru.sharedReplyComments).toBe("\u0415\u0434\u0438\u043d\u044b\u0439 \u043e\u0442\u0432\u0435\u0442 \u0432 \u043a\u043e\u043c\u043c\u0435\u043d\u0442\u0430\u0440\u0438\u044f\u0445");
  expect(messages.ru.privateReply).toBe("\u0415\u0434\u0438\u043d\u044b\u0439 \u043e\u0442\u0432\u0435\u0442 \u0432 \u041b\u0421");
  expect(appSource).toContain('t(locale, "sharedReplyComments")');
  expect(appSource).toContain('t(locale, "privateReply")');
  expect(appSource.match(/className="sharedReplyInput"/g)).toHaveLength(2);
});

test("uses a thin native Ubuntu font for release notes", () => {
  const css = readFileSync(new URL("./styles.css", import.meta.url), "utf8");
  const appSource = readFileSync(new URL("./App.tsx", import.meta.url), "utf8");

  expect(css).toMatch(/:root\s*\{[^}]*font-family:\s*Inter,\s*-apple-system,\s*BlinkMacSystemFont,\s*"SF Pro Display",\s*"Segoe UI",\s*sans-serif;/s);
  expect(css).toMatch(/\.releaseOverlayContent,\s*\.releaseNotesPage\s*\{[^}]*font-family:\s*"Ubuntu Sans",\s*"Noto Sans",\s*sans-serif;[^}]*font-synthesis:\s*none;[^}]*font-weight:\s*350;/s);
  expect(css).not.toContain(".content--releaseNotes");
  expect(appSource).not.toContain('section === "releaseNotes" ? "content content--releaseNotes"');
});

test("renders infinity for unlimited system capacity in the account assignment dropdown", () => {
  const appSource = readFileSync(new URL("./App.tsx", import.meta.url), "utf8");

  expect(appSource).toContain('{proxyRouteLabel(locale, profile.id, profile.name)} · {profile.usage}/{profile.capacity === 0 ? "∞" : profile.capacity}');
});

test("wires safe localized managed-proxy labels into account and settings surfaces", () => {
  const appSource = readFileSync(new URL("./App.tsx", import.meta.url), "utf8");

  expect(appSource).toContain('proxyRouteLabel(locale, account.proxyProfileId ?? "", account.proxyRouteName || account.proxy)');
  expect(appSource).toContain('proxyRouteLabel(locale, profile.id, profile.name)');
  expect(appSource).toContain('proxyTransportLabel(locale, proxyStatus.transport)');
  expect(appSource).toContain('rows={proxyProfiles.map((profile) => ({ ...profile, name: proxyRouteLabel(locale, profile.id, profile.name) }))}');
  expect(appSource).toContain('mode: "tor_snowflake"');
});

test("shows the manual update control in the release-notes topbar without duplicating it in the history", () => {
  const appSource = readFileSync(new URL("./App.tsx", import.meta.url), "utf8");

  expect(appSource).toMatch(/section === "releaseNotes" && <div className="topbarActions topbarActions--updates">\s*<UpdatePanel locale=\{locale\} \/>\s*<\/div>/);
  expect(appSource).not.toMatch(/function ReleaseNotesView[\s\S]*?return <section className="releaseNotesPage">\s*<UpdatePanel/);
});

test("removes the renderer local-import surface", () => {
  const appSource = readFileSync(new URL("./App.tsx", import.meta.url), "utf8");
  const css = readFileSync(new URL("./styles.css", import.meta.url), "utf8");
  const i18n = readFileSync(new URL("./i18n.ts", import.meta.url), "utf8");
  const bindings = readFileSync(new URL("../wailsjs/go/wails/Bindings.js", import.meta.url), "utf8");
  const declarations = readFileSync(new URL("../wailsjs/go/wails/Bindings.d.ts", import.meta.url), "utf8");
  const models = readFileSync(new URL("../wailsjs/go/models.ts", import.meta.url), "utf8");

  expect(appSource).toMatch(/className="catalogDeleteButton"[\s\S]*?<Trash2 size=\{16\} \/>\{t\(locale, "delete"\)\}/);
  expect(appSource).not.toContain("HardDrive" + "Download");
  expect(appSource).not.toContain("SubmitTData" + "DriveLinks");
  expect(appSource).not.toContain("ListTData" + "ImportItems");
  expect(appSource).not.toContain("TData" + "ImportModal");
  expect(i18n).not.toContain("Add tdata " + "accounts");
  expect(i18n).not.toContain("Добавить tdata " + "аккаунты");
  expect(css).not.toContain(".tdata" + "ImportModal");
  expect(bindings).not.toContain("SubmitTData" + "DriveLinks");
  expect(bindings).not.toContain("ListTData" + "ImportItems");
  expect(declarations).not.toContain("SubmitTData" + "DriveLinks");
  expect(declarations).not.toContain("ListTData" + "ImportItems");
  expect(models).not.toContain("TData" + "ImportItemDTO");
  expect(models).not.toContain("TData" + "ImportSubmitDTO");
});

test("keeps the Friday system-font rendering without a bundled variable font override", () => {
  const mainSource = readFileSync(new URL("./main.tsx", import.meta.url), "utf8");
  const packageJSON = JSON.parse(readFileSync(new URL("../package.json", import.meta.url), "utf8"));

  expect(packageJSON.dependencies["@fontsource-variable/inter"]).toBeUndefined();
  expect(mainSource).not.toContain("@fontsource-variable/inter");
});

test("shows the build timestamp above the developer credit", () => {
  const appSource = readFileSync(new URL("./App.tsx", import.meta.url), "utf8");
  const timestampIndex = appSource.indexOf('className="buildTimestamp"');
  const creditIndex = appSource.indexOf("Developed by Denis Myskar");

  expect(timestampIndex).toBeGreaterThan(-1);
  expect(timestampIndex).toBeLessThan(creditIndex);
  expect(appSource).toContain("BUILD_TIMESTAMP_LABEL");
});

describe("analytics statistics status", () => {
  test("normalizes camelCase live collection statistics", () => {
    expect(analyticsStatsFromStatus({
      running: true,
      lastRunAt: "2026-07-15T09:00:00Z",
      nextRunAt: "2026-07-15T10:00:00Z",
      latestCollection: {
        newMessages: 18,
        extractedWords: 73,
        newCanonicalWords: 11,
        processedGroups: 4,
        durationMillis: 1200,
        errors: ["timeout"]
      },
      totals: { messages: 450, canonicalWords: 91, forms: 126, groups: 22, keywordDbBytes: 2048 }
    })).toMatchObject({
      running: true,
      lastRunAt: "2026-07-15T09:00:00Z",
      nextRunAt: "2026-07-15T10:00:00Z",
      latestCollection: { newMessages: 18, extractedWords: 73, newCanonicalWords: 11, processedGroups: 4, durationMillis: 1200, errors: ["timeout"] },
      totals: { messages: 450, canonicalWords: 91, forms: 126, groups: 22, keywordDbBytes: 2048 }
    });
  });

  test("normalizes PascalCase flat scheduler and metrics fields", () => {
    expect(analyticsStatsFromStatus({
      Running: false,
      Collecting: true,
      LastRunAt: "2026-07-15T09:00:00Z",
      NextRunAt: "2026-07-15T10:00:00Z",
      NewMessages: 9,
      ExtractedWords: 40,
      NewCanonicals: 6,
      ProcessedGroups: 2,
      DurationMillis: 500,
      Errors: "collector unavailable",
      MessageCount: 99,
      CanonicalCount: 25,
      FormCount: 38,
      GroupCount: 8,
      LogicalKeywordBytes: 512
    })).toMatchObject({
      running: true,
      latestCollection: { newMessages: 9, extractedWords: 40, newCanonicalWords: 6, processedGroups: 2, durationMillis: 500, errors: ["collector unavailable"] },
      totals: { messages: 99, canonicalWords: 25, forms: 38, groups: 8, keywordDbBytes: 512 }
    });
  });
});
