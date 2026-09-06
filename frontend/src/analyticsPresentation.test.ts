import { expect, test } from "vitest";
import {
  analyticsPresentationReducer,
  initialAnalyticsPresentation,
  selectAnalyticsSurface
} from "./analyticsPresentation";

test("restored comparison operation selects comparison UI before rows are presented", () => {
  const restored = analyticsPresentationReducer(initialAnalyticsPresentation, {
    type: "operationRestored",
    operation: "comparison"
  });

  expect(selectAnalyticsSurface(restored)).toBe("comparison");
  expect(restored.comparisonRows).toEqual([]);

  const withRows = analyticsPresentationReducer(restored, {
    type: "comparisonLoaded",
    rows: [{ topic: "A", count: 3, distinctCandidateCount: 2, topPhrases: [], relativeShare: 1 }]
  });

  expect(selectAnalyticsSurface(withRows)).toBe("comparison");
  expect(withRows.comparisonRows.map((row) => row.topic)).toEqual(["A"]);
});
