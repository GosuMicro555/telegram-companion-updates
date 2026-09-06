import { describe, expect, test } from "vitest";
import { messages } from "./i18n";
import {
  ANALYTICS_REFRESH_MS,
  analyticsReducer,
  initialAnalyticsState,
  mergeCandidateKeywords,
  nextMetricsRefreshAt,
  selectVisibleCandidates,
  type AnalyticsCandidate
} from "./analytics";

const candidates: AnalyticsCandidate[] = [
  { runId: "run-1", normalizedValue: "деньги", displayValue: "Деньги", kind: "word", frequency: 8, sourceDiversity: 3, score: 19, source: "scout", moderationState: "new" },
  { runId: "run-1", normalizedValue: "не хватает", displayValue: "Не хватает", kind: "phrase", frequency: 5, sourceDiversity: 2, score: 14, source: "scout", moderationState: "new" }
];

describe("analytics state", () => {
  test("filters candidates by tab and query", () => {
    const state = { ...initialAnalyticsState, candidates, tab: "general" as const, query: "день" };
    expect(selectVisibleCandidates(state).map((row) => row.displayValue)).toEqual(["Деньги"]);
  });

  test("schedules storage refresh exactly every 60 seconds", () => {
    expect(ANALYTICS_REFRESH_MS).toBe(60_000);
    expect(nextMetricsRefreshAt(1_000)).toBe(61_000);
  });

  test("moderation hides rejected rows", () => {
    const loaded = analyticsReducer(initialAnalyticsState, { type: "resultsLoaded", candidates });
    const rejected = analyticsReducer(loaded, { type: "moderated", normalizedValue: "деньги", kind: "word", state: "rejected" });
    expect(selectVisibleCandidates(rejected).map((row) => row.normalizedValue)).toEqual(["не хватает"]);
  });

  test("add all merges visible candidates without case-insensitive duplicates", () => {
    expect(mergeCandidateKeywords([{ keyword: "деньги", dm: true }], candidates)).toEqual([
      { keyword: "деньги", dm: true },
      { keyword: "Не хватает", dm: true }
    ]);
  });

  test("successful add-all marks submitted rows and removes them from visibility", () => {
    const loaded = analyticsReducer(initialAnalyticsState, { type: "resultsLoaded", candidates });
    const added = analyticsReducer(loaded, { type: "candidatesAdded", candidates });
    expect(selectVisibleCandidates(added)).toEqual([]);
    expect(added.candidates.every((candidate) => candidate.moderationState === "added_to_keywords")).toBe(true);
  });

  test("Russian and English translation key sets match exactly", () => {
    expect(Object.keys(messages.ru).sort()).toEqual(Object.keys(messages.en).sort());
  });
});
