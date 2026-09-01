import { describe, expect, test, vi } from "vitest";
import {
  createCanonicalAnalyticsController,
  filterCanonicalKeywords,
  type CanonicalAnalyticsDependencies,
  type CanonicalKeyword
} from "./analyticsController";

const money: CanonicalKeyword = {
  id: "money-id",
  canonical: "money",
  language: "en",
  class: "neutral",
  frequency: 8,
  frequencyDelta: 0,
  messageCount: 5,
  lastSeen: "",
  forms: [{ value: "money", frequency: 6 }],
  triggerActive: false
};

function harness(overrides: Partial<CanonicalAnalyticsDependencies> = {}) {
  const list = vi.fn(async () => [money]);
  const dependencies: CanonicalAnalyticsDependencies = {
    list,
    classify: vi.fn(async () => undefined),
    setTrigger: vi.fn(async () => undefined),
    remove: vi.fn(async () => undefined),
    clear: vi.fn(async () => undefined),
    detachForm: vi.fn(async () => undefined),
    moveForm: vi.fn(async () => undefined),
    addForm: vi.fn(async () => undefined),
    analyze: vi.fn(async () => undefined),
    syncTriggers: vi.fn(async () => undefined),
    ...overrides
  };
  const published: CanonicalKeyword[][] = [];
  const errors: string[] = [];
  const controller = createCanonicalAnalyticsController(dependencies, {
    onRows: (rows) => published.push(rows),
    onError: (message) => errors.push(message)
  });
  return { controller, dependencies, errors, list, published };
}

describe("canonical analytics controller", () => {
  test("loads canonical rows for the selected class", async () => {
    const { controller, list, published } = harness();

    await controller.load("neutral");

    expect(list).toHaveBeenCalledWith("neutral");
    expect(published).toEqual([[money]]);
  });

  test("classifies a neutral word and refreshes the current tab", async () => {
    const { controller, dependencies, list } = harness();

    await controller.classify("money-id", "positive", "neutral");

    expect(dependencies.classify).toHaveBeenCalledWith("money-id", "positive");
    expect(list).toHaveBeenCalledWith("neutral");
  });

  test("reports a failed mutation without pretending it succeeded", async () => {
    const failure = new Error("sqlite unavailable");
    const { controller, errors, list } = harness({
      classify: vi.fn(async () => { throw failure; })
    });

    const succeeded = await controller.classify("money-id", "negative");

    expect(succeeded).toBe(false);
    expect(errors).toEqual([failure.message]);
    expect(list).not.toHaveBeenCalled();
  });

  test("relies on the trigger mutation to synchronize before refreshing positive words", async () => {
    const { controller, dependencies, list } = harness();

    await controller.setTrigger("money-id", true, "positive");

    expect(dependencies.setTrigger).toHaveBeenCalledWith("money-id", true);
    expect(dependencies.syncTriggers).not.toHaveBeenCalled();
    expect(list).toHaveBeenCalledWith("positive");
  });

  test("detaches and moves forms before refreshing the active table", async () => {
    const { controller, dependencies, list } = harness();

    await controller.detachForm("money-id", "money", "positive");
    await controller.moveForm("money-id", "cash", "positive");
    await controller.addForm("money-id", "funds", "positive");

    expect(dependencies.detachForm).toHaveBeenCalledWith("money-id", "money");
    expect(dependencies.moveForm).toHaveBeenCalledWith("money-id", "cash");
    expect(dependencies.addForm).toHaveBeenCalledWith("money-id", "funds");
    expect(list).toHaveBeenCalledTimes(3);
    expect(list).toHaveBeenLastCalledWith("positive");
  });

  test("analyzes then synchronizes triggers before reloading neutral words", async () => {
    const { controller, dependencies, list } = harness();

    await controller.analyze("neutral");

    expect(dependencies.analyze).toHaveBeenCalledOnce();
    expect(dependencies.syncTriggers).toHaveBeenCalledOnce();
    expect(list).toHaveBeenCalledWith("neutral");
  });

  test("does not overlap an analytics run while one is already in progress", async () => {
    let finishAnalysis: (() => void) | undefined;
    const { controller, dependencies } = harness({
      analyze: vi.fn(() => new Promise<void>((resolve) => { finishAnalysis = resolve; }))
    });

    const first = controller.analyze("neutral");
    const second = controller.analyze("neutral");

    expect(dependencies.analyze).toHaveBeenCalledOnce();
    expect(await second).toBe(false);
    finishAnalysis?.();
    expect(await first).toBe(true);
  });

  test("finds canonical rows by a matching form", () => {
    const rows = [{ ...money, forms: [{ value: "cash", frequency: 3 }] }];

    expect(filterCanonicalKeywords(rows, "cash")).toEqual(rows);
    expect(filterCanonicalKeywords(rows, "missing")).toEqual([]);
  });
});
