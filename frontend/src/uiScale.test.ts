import { describe, expect, test, vi } from "vitest";
import { readFileSync } from "node:fs";
import { autoScale, installScaleShortcuts, nextScale, readStoredScale } from "./uiScale";

describe("adaptive UI scale", () => {
  test("scale shortcuts clamp and reset", () => {
    expect(nextScale(100, 1)).toBe(110);
    expect(nextScale(150, 1)).toBe(150);
    expect(nextScale(80, -1)).toBe(80);
    expect(autoScale({ width: 1920, height: 1080 }, 1)).toBe(100);
    expect(autoScale({ width: 2880, height: 1864 }, 1)).toBe(125);
    expect(autoScale({ width: 3456, height: 2234 }, 2)).toBe(100);
  });

  test("normalizes physical viewport dimensions by device pixel ratio once", () => {
    expect(autoScale({ width: 3840, height: 2160 }, 2)).toBe(100);
  });

  test("shell keeps full viewport width instead of inverse-compensating zoom", () => {
    const css = readFileSync(new URL("./styles.css", import.meta.url), "utf8");
    const shellRule = css.match(/\.shell\s*\{[\s\S]*?\n\}/)?.[0] ?? "";

    expect(shellRule).toContain("width: 100%;");
    expect(shellRule).toContain("min-height: 100vh;");
    expect(shellRule).toContain("zoom: var(--ui-scale);");
    expect(shellRule).not.toContain("width: calc(100% / var(--ui-scale));");
    expect(shellRule).not.toContain("height: calc(100vh / var(--ui-scale));");
  });

  test("keeps a stable minimum canvas without changing layout width per scale", () => {
    const css = readFileSync(new URL("./styles.css", import.meta.url), "utf8");

    expect(css).toContain("--minimum-shell-width: 860px;");
    expect(css).toContain("min-width: var(--minimum-shell-width);");
    expect(css).not.toContain(':root[data-ui-scale="80"]');
  });

  test("uses injected event target and storage and prevents WebView zoom", () => {
    let listener: ((event: KeyboardEvent) => void) | undefined;
    const target = {
      addEventListener: vi.fn((_type: string, next: EventListener) => { listener = next as (event: KeyboardEvent) => void; }),
      removeEventListener: vi.fn()
    };
    const values = new Map<string, string>();
    const storage = {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => values.set(key, value)
    };
    const onScale = vi.fn();
    const uninstall = installScaleShortcuts({ target, storage, initialScale: 100, onScale });
    const event = { ctrlKey: true, metaKey: false, key: "+", preventDefault: vi.fn() } as unknown as KeyboardEvent;

    listener?.(event);

    expect(event.preventDefault).toHaveBeenCalledOnce();
    expect(onScale).toHaveBeenLastCalledWith(110);
    expect(readStoredScale(storage)).toBe(110);
    uninstall();
    expect(target.removeEventListener).toHaveBeenCalledOnce();
  });
});
