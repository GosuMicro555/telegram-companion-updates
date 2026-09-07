import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";

import { APP_VERSION, APP_VERSION_LABEL } from "./version";

describe("release version", () => {
  it("declares the 0.8.7 release and compact sidebar label", () => {
    expect(APP_VERSION).toBe("0.8.7");
    expect(APP_VERSION_LABEL).toBe("v0.8.7");
  });

  it("keeps the Wails product version aligned", () => {
    const config = JSON.parse(readFileSync(new URL("../../wails.json", import.meta.url), "utf8"));
    const packageManifest = JSON.parse(readFileSync(new URL("../package.json", import.meta.url), "utf8"));
    const packageLock = JSON.parse(readFileSync(new URL("../package-lock.json", import.meta.url), "utf8"));
    expect(config.info.productVersion).toBe(APP_VERSION);
    expect(packageManifest.version).toBe(APP_VERSION);
    expect(packageLock.version).toBe(APP_VERSION);
    expect(packageLock.packages[""].version).toBe(APP_VERSION);
  });
});
