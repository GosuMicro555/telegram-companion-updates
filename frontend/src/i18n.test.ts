import { expect, test } from "vitest";
import { readFileSync } from "node:fs";
import { messages, persistLocale, readStoredLocale, tAccountRuntimeHint, tAccountRuntimeStatus, tCatalogStatus, tRuntimeStatus } from "./i18n";

test("localizes the account rest dashboard and its status labels", () => {
  expect(messages.ru.accountRest).toBe("\u041e\u0442\u043b\u0451\u0436\u043a\u0430 \u0430\u043a\u043a\u0430\u0443\u043d\u0442\u043e\u0432");
  expect(messages.en.accountRest).toBe("Account rest");
  expect(messages.ru.accountResting).toBe("\u041e\u0442\u043b\u0451\u0436\u043a\u0430");
  expect(messages.ru.accountReady).toBe("\u0413\u043e\u0442\u043e\u0432");
});

test("translates catalog rows that are leaving their channels", () => {
  expect(tCatalogStatus("ru", "removing")).toBe("\u0412\u044b\u0445\u043e\u0434 \u0438\u0437 \u043a\u0430\u043d\u0430\u043b\u0430");
  expect(tCatalogStatus("en", "removing")).toBe("Leaving channel");
});

test("persists RU and EN locale and localizes runtime account states", () => {
	const values = new Map<string, string>();
	const storage = {
		getItem: (key: string) => values.get(key) ?? null,
		setItem: (key: string, value: string) => values.set(key, value)
	};
	expect(readStoredLocale(storage)).toBe("ru");
	persistLocale(storage, "en");
	expect(readStoredLocale(storage)).toBe("en");
	expect(tRuntimeStatus("en", "connecting")).toBe("Connecting");
	expect(tRuntimeStatus("ru", "backoff")).toBe("Повторное подключение");
});

test("localizes permanent duplicated Telegram sessions without exposing backend details", () => {
	expect(tAccountRuntimeStatus("ru", "error", "rpc_auth_key_duplicated")).toBe("Сессия недействительна");
	expect(tAccountRuntimeStatus("en", "error", "rpc_auth_key_duplicated")).toBe("Session is invalid");
	expect(tAccountRuntimeHint("ru", "rpc_auth_key_duplicated")).toContain("новая авторизация Telegram");
	expect(tAccountRuntimeHint("en", "rpc_auth_key_duplicated")).toContain("new Telegram authorization");
	expect(tAccountRuntimeHint("en", "/private/raw backend detail")).toBe("");
});

test("App persists the locale selector and translates live status surfaces", () => {
	const source = readFileSync(new URL("./App.tsx", import.meta.url), "utf8");
	expect(source).toContain("readStoredLocale(localStorage)");
	expect(source).toContain("persistLocale(localStorage, locale)");
	expect(source).toContain('aria-label={t(locale, "language")}');
	expect(source.match(/tRuntimeStatus\(locale,/g)?.length).toBeGreaterThanOrEqual(3);
});

test("localizes the appearance setting in Russian and English", () => {
	expect(messages.ru.appearance).toBe("Оформление");
	expect(messages.ru.lightTheme).toBe("Светлая");
	expect(messages.ru.darkTheme).toBe("Темная");
	expect(messages.en.appearance).toBe("Appearance");
	expect(messages.en.lightTheme).toBe("Light");
	expect(messages.en.darkTheme).toBe("Dark");
});

test("localizes the canonical bulk-import workflow", () => {
  expect(messages.ru.analyticsBulkAdd).toBe("Добавить");
  expect(messages.en.analyticsBulkAdd).toBe("Add");
  expect(messages.ru.analyticsBulkSkipped).toBe("Пропущено:");
  expect(messages.en.analyticsBulkSkipped).toBe("Skipped:");
});

test("App restores and applies the theme through Settings only", () => {
	const source = readFileSync(new URL("./App.tsx", import.meta.url), "utf8");
	expect(source).toContain('useState<ThemePreference>(() => readStoredTheme(localStorage))');
	expect(source).toContain("persistTheme(localStorage, theme)");
	expect(source).toContain('document.documentElement.dataset.theme = resolveTheme(theme)');
	expect(source).toMatch(/section === "settings" && <SettingsView[^>]+locale=\{locale\}[^>]+theme=\{theme\}/);
	expect(source).toContain('aria-label={t(locale, "appearance")}');
});

test("uses the approved Russian Live statistics navigation label", () => {
  expect(messages.ru.liveStats).toBe("Live-статистика");
});
