import { mergeKeywords, type KeywordRow } from "./keywords";

export const ANALYTICS_REFRESH_MS = 60_000;
export type AnalyticsTab = "topic" | "general" | "imports";
export type ModerationState = "new" | "accepted" | "rejected" | "added_to_keywords";

export type AnalyticsCandidate = {
  runId: string;
  normalizedValue: string;
  displayValue: string;
  kind: "word" | "phrase";
  frequency: number;
  sourceDiversity: number;
  score: number;
  source: string;
  moderationState: ModerationState;
};

export type AnalyticsState = {
  tab: AnalyticsTab;
  query: string;
  profileId: string;
  selectedTopics: ReadonlySet<string>;
  candidates: AnalyticsCandidate[];
};

export type AnalyticsAction =
  | { type: "tabChanged"; tab: AnalyticsTab }
  | { type: "queryChanged"; query: string }
  | { type: "profileChanged"; profileId: string }
  | { type: "topicsChanged"; topics: ReadonlySet<string> }
  | { type: "resultsLoaded"; candidates: AnalyticsCandidate[] }
	| { type: "importCandidatesLoaded"; candidates: AnalyticsCandidate[] }
  | { type: "moderated"; normalizedValue: string; kind: string; state: ModerationState }
  | { type: "candidatesAdded"; candidates: ReadonlyArray<Pick<AnalyticsCandidate, "runId" | "normalizedValue" | "kind">> };

export const initialAnalyticsState: AnalyticsState = {
  tab: "topic",
  query: "",
  profileId: "",
  selectedTopics: new Set<string>(),
  candidates: []
};

export function analyticsReducer(state: AnalyticsState, action: AnalyticsAction): AnalyticsState {
  switch (action.type) {
    case "tabChanged": return { ...state, tab: action.tab };
    case "queryChanged": return { ...state, query: action.query };
    case "profileChanged": return { ...state, profileId: action.profileId };
    case "topicsChanged": return { ...state, selectedTopics: new Set(action.topics) };
    case "resultsLoaded": return { ...state, candidates: [...action.candidates] };
	case "importCandidatesLoaded": return { ...state, tab: "topic", candidates: [...action.candidates] };
    case "moderated":
      return {
        ...state,
        candidates: state.candidates.map((candidate) =>
          candidate.normalizedValue === action.normalizedValue && candidate.kind === action.kind
            ? { ...candidate, moderationState: action.state }
            : candidate
        )
      };
    case "candidatesAdded": {
      const added = new Set(action.candidates.map((candidate) => `${candidate.runId}:${candidate.kind}:${candidate.normalizedValue}`));
      return {
        ...state,
        candidates: state.candidates.map((candidate) => added.has(`${candidate.runId}:${candidate.kind}:${candidate.normalizedValue}`)
          ? { ...candidate, moderationState: "added_to_keywords" }
          : candidate)
      };
    }
  }
}

export function selectVisibleCandidates(state: AnalyticsState): AnalyticsCandidate[] {
  if (state.tab === "imports") return [];
  const needle = state.query.trim().toLocaleLowerCase("ru-RU");
  return state.candidates.filter((candidate) => {
    if (candidate.moderationState === "rejected" || candidate.moderationState === "added_to_keywords") return false;
    if (state.tab === "general" && candidate.kind !== "word") return false;
    if (state.tab === "topic" && candidate.kind !== "phrase") return false;
    return !needle || candidate.displayValue.toLocaleLowerCase("ru-RU").includes(needle);
  });
}

export function nextMetricsRefreshAt(lastRefreshAt: number): number {
  return lastRefreshAt + ANALYTICS_REFRESH_MS;
}

export function mergeCandidateKeywords(rows: KeywordRow[], candidates: ReadonlyArray<AnalyticsCandidate>): KeywordRow[] {
  const values = candidates
    .filter((candidate) => candidate.moderationState !== "rejected")
    .map((candidate) => candidate.displayValue);
  return mergeKeywords(rows, values, true);
}
