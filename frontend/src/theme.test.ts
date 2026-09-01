import { describe, expect, it } from "vitest";
import { persistTheme, readStoredTheme, resolveTheme, THEME_STORAGE_KEY } from "./theme";

function createStorage(initial: Record<string, string> = {}) {
  const values = new Map(Object.entries(initial));
  return {
    getItem(key: string) {
      return values.get(key) ?? null;
    },
    setItem(key: string, value: string) {
      values.set(key, value);
    }
  };
}

describe("theme preferences", () => {
  it("defaults to the light theme and persists an explicit selection", () => {
    const storage = createStorage();

    expect(readStoredTheme(storage)).toBe("light");
    persistTheme(storage, "dark");

    expect(storage.getItem(THEME_STORAGE_KEY)).toBe("dark");
    expect(readStoredTheme(storage)).toBe("dark");
  });

  it("falls back to light for an unknown stored theme", () => {
    const storage = createStorage({ [THEME_STORAGE_KEY]: "midnight" });

    expect(readStoredTheme(storage)).toBe("light");
  });

  it("resolves only the supported visual themes", () => {
    expect(resolveTheme("light")).toBe("light");
    expect(resolveTheme("dark")).toBe("dark");
  });
});
