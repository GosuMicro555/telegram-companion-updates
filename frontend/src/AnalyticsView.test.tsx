import { renderToStaticMarkup } from "react-dom/server";
import type { Dispatch, SetStateAction } from "react";
import { readFileSync } from "node:fs";
import { expect, test } from "vitest";
import {
  AnalyticsView,
  canonicalImportOutcome,
  canonicalTriggerValues,
  formatNextRun,
  handlePositiveTriggerAction,
  normalizeAnalyticsRuntime,
  parseBulkCanonicalValues,
  positiveKeywordCandidates,
  removeCanonicalTriggerKeyword
} from "./AnalyticsView";
import {
  handleCanonicalClearAction,
  handleCanonicalClassificationAction,
  handleCanonicalDeleteAction
} from "./analyticsKeywordActions";
import type { CanonicalKeyword } from "./analyticsController";
import type { KeywordRow } from "./keywords";

const positiveKeyword: CanonicalKeyword = {
  id: "money",
  canonical: "money",
  language: "en",
  class: "positive",
  frequency: 3,
  frequencyDelta: 0,
  messageCount: 2,
  lastSeen: "",
  triggerActive: false,
  forms: [
    { value: "monies", frequency: 2 },
    { value: "money's", frequency: 1 }
  ]
};

test("adds only the canonical value for a positive keyword", () => {
  expect(canonicalTriggerValues([positiveKeyword])).toEqual(["money"]);
});

test("bulk addition includes only canonical values", () => {
  expect(canonicalTriggerValues([
    positiveKeyword,
    { ...positiveKeyword, id: "support", canonical: "support", forms: [{ value: "supported", frequency: 1 }] }
  ])).toEqual(["money", "support"]);
});

test("bulk positive action uses the actual keyword list instead of stale trigger flags", () => {
  const rows = [
    { ...positiveKeyword, triggerActive: true },
    { ...positiveKeyword, id: "support", canonical: "Support", triggerActive: false }
  ];

  expect(positiveKeywordCandidates(rows, [{ keyword: "money", dm: false }])).toEqual([
    expect.objectContaining({ id: "support" })
  ]);
  expect(positiveKeywordCandidates(rows, [
    { keyword: "MONEY", dm: false },
    { keyword: "support", dm: true }
  ])).toEqual([]);
});

test("removes the canonical keyword locally when deactivating an active positive trigger", () => {
  const calls: string[] = [];
  const active = { ...positiveKeyword, triggerActive: true };
  const existing = [
    { keyword: "manual", dm: true },
    { keyword: "Money", dm: false },
    { keyword: "monies", dm: true }
  ];
  let changed = existing;

  handlePositiveTriggerAction(
    active,
    () => calls.push("add"),
    (row) => {
      changed = removeCanonicalTriggerKeyword(existing, row.canonical);
      calls.push(`remove:${row.canonical}`);
    },
    (id, enabled) => calls.push(`trigger:${id}:${enabled}`)
  );

  expect(changed).toEqual([
    { keyword: "manual", dm: true },
    { keyword: "monies", dm: true }
  ]);
  expect(calls).toEqual(["remove:money", "trigger:money:false"]);
});

test.each(["neutral", "negative"] as const)("removes an active canonical after classifying it as %s", async (nextClass) => {
  const active = { ...positiveKeyword, triggerActive: true };
  const events: string[] = [];
  let current: KeywordRow[] = [
    { keyword: "manual", dm: true },
    { keyword: "Money", dm: false }
  ];
  const onKeywordsChange: Dispatch<SetStateAction<KeywordRow[]>> = (next) => {
    current = typeof next === "function" ? next(current) : next;
    events.push(`keywords:${current.map((row) => row.keyword).join(",")}`);
  };

  await handleCanonicalClassificationAction(
    active,
    nextClass,
    onKeywordsChange,
    async (id, keywordClass) => {
      events.push(`classify:${id}:${keywordClass}`);
      return true;
    }
  );
  const laterReplySave = { keywords: current.map((row) => row.keyword), sharedReply: "edited later" };

  expect(events).toEqual([`classify:money:${nextClass}`, "keywords:manual"]);
  expect(laterReplySave).toEqual({ keywords: ["manual"], sharedReply: "edited later" });
});

test("keeps an active canonical when classification fails", async () => {
  const active = { ...positiveKeyword, triggerActive: true };
  let current: KeywordRow[] = [{ keyword: "money", dm: false }];
  const onKeywordsChange: Dispatch<SetStateAction<KeywordRow[]>> = (next) => {
    current = typeof next === "function" ? next(current) : next;
  };

  const succeeded = await handleCanonicalClassificationAction(
    active,
    "negative",
    onKeywordsChange,
    async () => false
  );

  expect(succeeded).toBe(false);
  expect(current).toEqual([{ keyword: "money", dm: false }]);
});

test("removes active positive canonicals after a successful bulk clear", async () => {
  const active = { ...positiveKeyword, triggerActive: true };
  let current: KeywordRow[] = [{ keyword: "manual", dm: false }, { keyword: "money", dm: true }];
  const onKeywordsChange: Dispatch<SetStateAction<KeywordRow[]>> = (next) => {
    current = typeof next === "function" ? next(current) : next;
  };

  const succeeded = await handleCanonicalClearAction(
    [active],
    "positive",
    onKeywordsChange,
    async () => true
  );

  expect(succeeded).toBe(true);
  expect(current).toEqual([{ keyword: "manual", dm: false }]);
});

test("removes an active canonical after deleting it", async () => {
  const active = { ...positiveKeyword, triggerActive: true };
  const events: string[] = [];
  let current: KeywordRow[] = [
    { keyword: "manual", dm: true },
    { keyword: "Money", dm: false }
  ];
  const onKeywordsChange: Dispatch<SetStateAction<KeywordRow[]>> = (next) => {
    current = typeof next === "function" ? next(current) : next;
    events.push(`keywords:${current.map((row) => row.keyword).join(",")}`);
  };

  await handleCanonicalDeleteAction(
    active,
    onKeywordsChange,
    async (id) => {
      events.push(`delete:${id}`);
      return true;
    }
  );

  expect(events).toEqual(["delete:money", "keywords:manual"]);
});

test("restores the persisted analytics collection interval", () => {
  expect(normalizeAnalyticsRuntime({
    running: true,
    collecting: true,
    intervalMinutes: 60,
    nextRunAt: "2026-07-15T12:00:00Z",
    allTimeGroups: 4,
    latestCollection: { newMessages: 12, extractedWords: 73, processedGroups: 3 }
  })).toMatchObject({
    running: true,
    collecting: true,
    intervalMinutes: 60,
    nextRunAt: "2026-07-15T12:00:00Z",
    allTimeGroups: 4,
    latestCollection: { newMessages: 12, extractedWords: 73, processedGroups: 3 }
  });
});

test("formats an empty next run as an em dash in every locale", () => {
  expect(formatNextRun("", "en")).toBe("—");
  expect(formatNextRun("", "ru")).toBe("—");
});

test("renders the approved canonical keyword workspace", () => {
  const markup = renderToStaticMarkup(
    <AnalyticsView locale="en" topics={[]} existingKeywords={[]} onKeywordsChange={() => undefined} />
  );

  expect(markup).toContain("All keywords");
  expect(markup).toContain("Positive");
  expect(markup).toContain("Negative");
  expect(markup).toContain("Service");
  expect(markup).toContain("Search canonical words and forms");
  expect(markup).toContain("Analyze now");
  expect(markup).toContain("Last run");
  expect(markup).toContain("All time");
  expect(markup).toMatch(/<dl\b[^>]*aria-label="Analytics metrics"/);

  const searchAt = markup.indexOf('role="search"');
  const toolbarAt = markup.indexOf('role="toolbar"');
  const gridAt = markup.indexOf('role="grid"');
  const footerAt = markup.indexOf("<footer", gridAt);
  const paginationAt = markup.indexOf('aria-label="Keyword table pagination"', footerAt);

  expect([searchAt, toolbarAt, gridAt, footerAt, paginationAt].every((position) => position >= 0)).toBe(true);
  expect(searchAt).toBeLessThan(gridAt);
  expect(toolbarAt).toBeLessThan(gridAt);
  expect(gridAt).toBeLessThan(footerAt);
  expect(footerAt).toBeLessThan(paginationAt);
  expect(markup).toContain('aria-label="Previous page"');
  expect(markup).toContain('aria-label="Next page"');
});

test("offers only the approved analytics interval stops", () => {
  const markup = renderToStaticMarkup(
    <AnalyticsView locale="en" topics={[]} existingKeywords={[]} onKeywordsChange={() => undefined} />
  );
  const slider = markup.match(/<input\b[^>]*type="range"[^>]*>/)?.[0] ?? "";
  const listId = slider.match(/\blist="([^"]+)"/)?.[1] ?? "";

  expect(slider).toContain('aria-label="Interval"');
  expect(slider).toContain('aria-valuetext="10 minutes"');
  expect(listId).not.toBe("");

  const start = markup.indexOf(`<datalist id="${listId}"`);
  const datalist = markup.slice(start, markup.indexOf("</datalist>", start));
  const ticks = [...datalist.matchAll(/<option\b([^>]*)>/g)].map(([, attributes]) => Number(
    attributes.match(/\blabel="(\d+)/)?.[1] ?? attributes.match(/\bvalue="(\d+)"/)?.[1]
  ));
  expect(ticks).toEqual([1, 5, 10, 30, 60, 120]);
});

test("normalizes bulk canonical input and skips mixed or punctuated values", () => {
  expect(parseBulkCanonicalValues("Money\nmoney\nДЕНЬГИ\nёж\nmixedСлово\nbad!")).toEqual({
    values: [
      { canonical: "money", forms: [] },
      { canonical: "деньги", forms: [] },
      { canonical: "ёж", forms: [] }
    ],
    skipped: 2
  });
});

test("splits commas and semicolons outside forms while preserving phrases and newlines", () => {
  expect(parseBulkCanonicalValues(
    "credit card, fast loan; salary\nбыстрый кредит (займ, ссуда)"
  )).toEqual({
    values: [
      { canonical: "credit card", forms: [] },
      { canonical: "fast loan", forms: [] },
      { canonical: "salary", forms: [] },
      { canonical: "быстрый кредит", forms: ["займ", "ссуда"] }
    ],
    skipped: 0
  });
});

test("preserves each non-empty phrase line as one canonical import entry", () => {
  expect(parseBulkCanonicalValues("  credit\u00a0  card  \n\n  fast loan ")).toEqual({
    values: [
      { canonical: "credit card", forms: [] },
      { canonical: "fast loan", forms: [] }
    ],
    skipped: 0
  });
});

test("parses parenthesized variants as forms of one canonical keyword", () => {
  expect(parseBulkCanonicalValues("Бабки (бабосы, бабосики / баблишко. бабки)\nденьги\nsalary")).toEqual({
    values: [
      { canonical: "бабки", forms: ["бабосы", "бабосики", "баблишко"] },
      { canonical: "деньги", forms: [] },
      { canonical: "salary", forms: [] }
    ],
    skipped: 0
  });
});

test("preserves a phrase canonical while splitting parenthesized additions into one-word forms", () => {
  expect(parseBulkCanonicalValues("Fast loan (credit, loans / lending)")).toEqual({
    values: [{ canonical: "fast loan", forms: ["credit", "loans", "lending"] }],
    skipped: 0
  });
});

test("accepts updated canonical imports and combines parser and backend skips", () => {
  expect(canonicalImportOutcome({ added: 0, updated: 1, skipped: 2 }, 3)).toEqual({
    accepted: true,
    skipped: 5
  });
  expect(canonicalImportOutcome({ added: 0, updated: 0, skipped: 1 }, 0)).toEqual({
    accepted: false,
    skipped: 1
  });
});

test("merges repeated canonical entries and deduplicates imported forms", () => {
  expect(parseBulkCanonicalValues("БАБКИ\nбабки (бабосы, Бабосы, баблишко)")).toEqual({
    values: [{ canonical: "бабки", forms: ["бабосы", "баблишко"] }],
    skipped: 0
  });
});

test("uses primary Russian analytics copy while preserving English locale", () => {
  const russian = renderToStaticMarkup(
    <AnalyticsView locale="ru" topics={[]} existingKeywords={[]} onKeywordsChange={() => undefined} />
  );
  const english = renderToStaticMarkup(
    <AnalyticsView locale="en" topics={[]} existingKeywords={[]} onKeywordsChange={() => undefined} />
  );

  expect(russian).toContain("Все keyword");
  expect(russian).toContain("Позитивные");
  expect(russian).toContain("Негативные");
  expect(russian).toContain("Служебные");
  expect(russian).toContain("Поиск по каноническим словам и формам");
  expect(english).toContain("All keywords");
  expect(english).toContain("Search canonical words and forms");
});

test("keeps canonical rows compact and responsive in isolated CSS", () => {
  const css = readFileSync(new URL("./analyticsRedesign.css", import.meta.url), "utf8");
  const shellCss = readFileSync(new URL("./styles.css", import.meta.url), "utf8");
  const appSource = readFileSync(new URL("./App.tsx", import.meta.url), "utf8");
  const source = readFileSync(new URL("./AnalyticsView.tsx", import.meta.url), "utf8");

  expect(css).toMatch(/\.analyticsRedesign__row\s*\{[^}]*min-height:\s*25px/s);
  expect(css).toMatch(/\.analyticsRedesign__runtime\s*\{[^}]*flex-wrap:\s*nowrap/s);
  expect(css).toMatch(/\.analyticsRedesign\s*\{[^}]*display:\s*flex[^}]*flex-direction:\s*column[^}]*height:\s*100%[^}]*overflow:\s*hidden/s);
  expect(css).toMatch(/\.analyticsRedesign__tableFrame\s*\{[^}]*display:\s*flex[^}]*flex:\s*1[^}]*min-height:\s*0/s);
  expect(css).toMatch(/\.analyticsRedesign__tableWrap\s*\{[^}]*flex:\s*1[^}]*min-height:\s*0[^}]*overflow:\s*auto/s);
  expect(shellCss).toMatch(/\.content--analytics\s*\{[^}]*display:\s*flex[^}]*flex-direction:\s*column[^}]*overflow:\s*hidden/s);
  expect(appSource).toContain('section === "analytics" ? "content content--analytics"');
  expect(css).toContain("@container");
  expect(css).toMatch(/\.analyticsRedesign__controlDeck\s*\{[^}]*grid-template-columns:\s*minmax\(380px, \.95fr\) minmax\(280px, \.85fr\) minmax\(550px, 1\.7fr\)/s);
  expect(css).toMatch(/\.analyticsRedesign__metricGrid\s*\{[^}]*grid-template-columns:\s*minmax\(220px, 1\.25fr\) repeat\(3, minmax\(86px, \.6fr\)\)/s);
  expect(source).toContain('import "./analyticsRedesign.css"');
  expect(source).toContain("StartCanonicalAnalytics");
  expect(source).toContain("StopCanonicalAnalytics");
  expect(source).toContain("AnalyzeCanonicalKeywords");
  expect(source).toContain("GetAnalyticsTablePreferences");
  expect(source).toContain("SaveAnalyticsTablePreferences");
  expect(source).toContain("withAnalyticsTableSort");
  expect(source).toContain("toAnalyticsTablePreference");
  expect(source).toContain("onSortChange={updateSort}");
});

test("exposes direct moderation, keyword additions, and unavailable Wails errors", () => {
  const source = readFileSync(new URL("./AnalyticsView.tsx", import.meta.url), "utf8");

  expect(source).toContain("ClassifyCanonicalKeyword");
  expect(source).toContain("SetCanonicalKeywordTrigger");
  expect(source).toContain("ClearCanonicalKeywords");
  expect(source).toContain("mergeKeywords");
  expect(source).toContain("filterCanonicalKeywords");
  expect(source).toContain("forms.some((form) => form.value");
  expect(source).toContain('Analytics method "${method}" is unavailable.');
  expect(source).toContain("BulkImportCanonicalKeywords");
  expect(source).toContain("BulkImportCanonicalKeywords as unknown");
  expect(source).toContain(")(preview.values, keywordClass)");
  expect(source).toContain('type="file"');
});

test("dismisses the analytics column menu through the shared outside-click contract", () => {
  const source = readFileSync(new URL("./AnalyticsView.tsx", import.meta.url), "utf8");

  expect(source).toContain("createColumnMenuDismissal");
  expect(source).toContain("columnMenuRef");
  expect(source).toMatch(/createColumnMenuDismissal\(document,\s*columnMenuRef\.current,\s*\(\) => setShowColumns\(false\)\)/s);
  expect(source).toMatch(/className="analyticsRedesign__commands"[^>]*ref=\{columnMenuRef\}/s);
});

test("keeps service stop-word editing separate from canonical moderation", () => {
  const source = readFileSync(new URL("./AnalyticsView.tsx", import.meta.url), "utf8");

  expect(source).toContain('call("AddAnalyticsServiceWord", serviceLanguage, serviceWord.trim())');
  expect(source).toContain('controllerRef.current?.load("service")');
  expect(source).toContain('if (props.keywordClass === "service") return "—";');
  expect(source).toContain('if (props.keywordClass === "service") return <IconButton danger label={t(props.locale, "analyticsDelete")}');
  expect(source).toContain('analyticsServiceWord');
  expect(source).toContain('analyticsAddServiceWord');
  expect(source).toContain('analyticsRussian');
  expect(source).toContain('analyticsEnglish');
});
