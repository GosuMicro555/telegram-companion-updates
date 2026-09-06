import { EventEmitter } from "node:events";
import { describe, expect, test } from "vitest";

import { resolveChromeExecutable, waitForChromeSpawn } from "./chrome-executable.mjs";

describe("geometry Chrome executable", () => {
  test("prefers an explicit geometry browser override", () => {
    const override = "/custom/Chrome for Testing";

    expect(resolveChromeExecutable({
      platform: "darwin",
      env: { GEOMETRY_CHROME_BIN: override, HOME: "/Users/test", PATH: "" },
      isExecutable: (candidate) => candidate === override
    })).toBe(override);
  });

  test("detects the standard macOS Chrome.app executable", () => {
    const chrome = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";

    expect(resolveChromeExecutable({
      platform: "darwin",
      env: { HOME: "/Users/test", PATH: "" },
      isExecutable: (candidate) => candidate === chrome
    })).toBe(chrome);
  });

  test("resolves a Linux browser command from PATH", () => {
    const chrome = "/opt/chrome/google-chrome-stable";

    expect(resolveChromeExecutable({
      platform: "linux",
      env: { PATH: "/usr/bin:/opt/chrome" },
      isExecutable: (candidate) => candidate === chrome
    })).toBe(chrome);
  });

  test("reports an actionable error when no browser is installed", () => {
    expect(() => resolveChromeExecutable({
      platform: "darwin",
      env: { HOME: "/Users/test", PATH: "" },
      isExecutable: () => false
    })).toThrow(/GEOMETRY_CHROME_BIN/);
  });
});

describe("geometry Chrome process startup", () => {
  test("turns a spawn error into a contextual rejection", async () => {
    const child = new EventEmitter();
    const startup = waitForChromeSpawn(child, "/missing/Google Chrome");
    const cause = Object.assign(new Error("spawn ENOENT"), { code: "ENOENT" });

    queueMicrotask(() => child.emit("error", cause));

    await expect(startup).rejects.toThrow(
      "Could not launch geometry browser at /missing/Google Chrome: spawn ENOENT"
    );
  });

  test("resolves after the process emits spawn", async () => {
    const child = new EventEmitter();
    const startup = waitForChromeSpawn(child, "/Applications/Google Chrome");

    queueMicrotask(() => child.emit("spawn"));

    await expect(startup).resolves.toBeUndefined();
  });
});
