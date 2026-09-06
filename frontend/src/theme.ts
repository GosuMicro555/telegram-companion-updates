export type ThemePreference = "light" | "dark";

type ThemeStorage = Pick<Storage, "getItem" | "setItem">;

export const THEME_STORAGE_KEY = "telegram-companion-theme";

export function readStoredTheme(storage: Pick<ThemeStorage, "getItem">): ThemePreference {
  return storage.getItem(THEME_STORAGE_KEY) === "dark" ? "dark" : "light";
}

export function persistTheme(storage: Pick<ThemeStorage, "setItem">, theme: ThemePreference): void {
  storage.setItem(THEME_STORAGE_KEY, theme);
}

export function resolveTheme(theme: ThemePreference): ThemePreference {
  return theme;
}
