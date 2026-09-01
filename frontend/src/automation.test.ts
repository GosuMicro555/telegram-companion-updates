import { describe, expect, it } from "vitest";

import * as automation from "./automation";
import { readAutomationRunning } from "./automation";

describe("readAutomationRunning", () => {
  it("reflects the backend dashboard instead of stale local state", async () => {
    const running = await readAutomationRunning(async () => ({ running: true }));

    expect(running).toBe(true);
  });

  it("persists keyword settings before starting automation", async () => {
    const helpers = automation as unknown as {
      startAutomationAfterSavingKeywordSettings?: (
        save: () => Promise<unknown>,
        start: () => Promise<void>
      ) => Promise<void>;
    };
    const events: string[] = [];

    expect(helpers.startAutomationAfterSavingKeywordSettings).toBeTypeOf("function");
    await helpers.startAutomationAfterSavingKeywordSettings?.(
      async () => { events.push("saved"); },
      async () => { events.push("started"); }
    );

    expect(events).toEqual(["saved", "started"]);
  });

  it("does not start automation when keyword settings cannot be saved", async () => {
    const helpers = automation as unknown as {
      startAutomationAfterSavingKeywordSettings?: (
        save: () => Promise<unknown>,
        start: () => Promise<void>
      ) => Promise<void>;
    };
    let started = false;

    await expect(helpers.startAutomationAfterSavingKeywordSettings?.(
      async () => { throw new Error("save failed"); },
      async () => { started = true; }
    )).rejects.toThrow("save failed");

    expect(started).toBe(false);
  });
});
