import type { TopicSummary } from "./analyticsController";

export type AnalyticsSurface = "candidates" | "comparison";

export type AnalyticsPresentation = {
  surface: AnalyticsSurface;
  comparisonRows: TopicSummary[];
};

export type AnalyticsPresentationAction =
  | { type: "operationRestored"; operation: string }
  | { type: "surfaceSelected"; surface: AnalyticsSurface }
  | { type: "comparisonLoaded"; rows: TopicSummary[] }
  | { type: "comparisonCleared" };

export const initialAnalyticsPresentation: AnalyticsPresentation = {
  surface: "candidates",
  comparisonRows: []
};

export function analyticsPresentationReducer(state: AnalyticsPresentation, action: AnalyticsPresentationAction): AnalyticsPresentation {
  switch (action.type) {
    case "operationRestored":
      if (action.operation === "comparison") return { ...state, surface: "comparison" };
      if (action.operation === "analysis") return { ...state, surface: "candidates" };
      return state;
    case "surfaceSelected":
      return { ...state, surface: action.surface };
    case "comparisonLoaded":
      return { surface: "comparison", comparisonRows: [...action.rows] };
    case "comparisonCleared":
      return { ...state, comparisonRows: [] };
  }
}

export function selectAnalyticsSurface(state: AnalyticsPresentation): AnalyticsSurface {
  return state.surface;
}
