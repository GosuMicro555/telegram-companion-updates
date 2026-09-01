import { describe, expect, test } from "vitest";

import {
  RECOVERY_CODES,
  desktopStartupView,
  normalizeDesktopStartupStatus,
  recoveryCopy
} from "./desktopStartup";

const expectedCopy = {
  en: {
    seed_license_required: "Choose a compatible license for the bundled starter data.",
    seed_unavailable: "The bundled starter data is unavailable. Reinstall this build, then try again.",
    seed_incompatible: "This license does not match the bundled starter data. Choose another license.",
    keychain_unavailable: "Keychain is unavailable. Unlock it, then try again.",
    insufficient_space: "There is not enough free space to prepare the workspace.",
    storage_unavailable: "Workspace storage is unavailable. Check this Mac, then try again.",
    profile_blocked: "Startup stopped to protect existing application data.",
    runtime_unavailable: "Telegram Companion could not start. Try again.",
    relaunch_failed: "Telegram Companion could not restart. Try again."
  },
  ru: {
    seed_license_required: "Выберите совместимую лицензию для встроенных стартовых данных.",
    seed_unavailable: "Встроенные стартовые данные недоступны. Переустановите эту сборку и повторите попытку.",
    seed_incompatible: "Лицензия не подходит для встроенных стартовых данных. Выберите другую лицензию.",
    keychain_unavailable: "Связка ключей недоступна. Разблокируйте её и повторите попытку.",
    insufficient_space: "Недостаточно свободного места для подготовки рабочего пространства.",
    storage_unavailable: "Хранилище рабочего пространства недоступно. Проверьте этот Mac и повторите попытку.",
    profile_blocked: "Запуск остановлен для защиты существующих данных приложения.",
    runtime_unavailable: "Не удалось запустить Telegram Companion. Повторите попытку.",
    relaunch_failed: "Не удалось перезапустить Telegram Companion. Повторите попытку."
  }
} as const;

describe("desktop startup model", () => {
  test("resolves views only from backend startup mode in loading-to-workspace order", () => {
    expect(desktopStartupView(null)).toBe("loading");
    expect(desktopStartupView({ mode: "recovery", errorCode: "seed_unavailable" })).toBe("recovery");
    expect(desktopStartupView({ mode: "activation" })).toBe("activation");
    expect(desktopStartupView({ mode: "workspace" })).toBe("workspace");
  });

  test("normalizes malformed backend status to a closed recovery state", () => {
    expect(normalizeDesktopStartupStatus({
      mode: "unexpected",
      errorCode: "/Users/private/raw backend error",
      restarting: "yes"
    })).toEqual({ mode: "recovery", errorCode: "runtime_unavailable", restarting: false });
  });

  test.each(["en", "ru"] as const)("has explicit %s copy for every closed recovery code", (locale) => {
    expect(RECOVERY_CODES).toEqual([
      "seed_license_required",
      "seed_unavailable",
      "seed_incompatible",
      "keychain_unavailable",
      "insufficient_space",
      "storage_unavailable",
      "profile_blocked",
      "runtime_unavailable",
      "relaunch_failed"
    ]);
    for (const code of RECOVERY_CODES) {
      expect(recoveryCopy(code, locale).message).toBe(expectedCopy[locale][code]);
    }
  });

  test("redacts an unknown raw backend value to the runtime fallback", () => {
    const raw = "/Users/alice/Library/Application Support/token=secret";
    const copy = recoveryCopy(raw, "en");

    expect(copy.code).toBe("runtime_unavailable");
    expect(copy.message).toBe(expectedCopy.en.runtime_unavailable);
    expect(JSON.stringify(copy)).not.toContain(raw);
    expect(JSON.stringify(copy)).not.toContain("alice");
    expect(JSON.stringify(copy)).not.toContain("secret");
  });
});
