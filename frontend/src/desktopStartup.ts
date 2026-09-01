export const RECOVERY_CODES = [
  "seed_license_required",
  "seed_unavailable",
  "seed_incompatible",
  "keychain_unavailable",
  "insufficient_space",
  "storage_unavailable",
  "profile_blocked",
  "runtime_unavailable",
  "relaunch_failed"
] as const;

export type RecoveryCode = typeof RECOVERY_CODES[number];
export type DesktopStartupMode = "activation" | "recovery" | "workspace";
export type DesktopStartupView = "loading" | DesktopStartupMode;
export type DesktopStartupLocale = "en" | "ru";

export type DesktopStartupStatus = {
  mode: DesktopStartupMode;
  errorCode?: string;
  restarting?: boolean;
};

export type RecoveryCopy = {
  code: RecoveryCode;
  title: string;
  message: string;
  diagnosticCode: string;
  retry: string;
  chooseAnotherLicense: string;
  retrying: string;
  choosing: string;
  restarting: string;
  actionFailed: string;
};

const recoveryCodeSet = new Set<string>(RECOVERY_CODES);

const recoveryMessages: Record<DesktopStartupLocale, Record<RecoveryCode, string>> = {
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
};

const recoveryLabels: Record<DesktopStartupLocale, Omit<RecoveryCopy, "code" | "message">> = {
  en: {
    title: "Telegram Companion needs attention",
    diagnosticCode: "Diagnostic code",
    retry: "Retry",
    chooseAnotherLicense: "Choose another license",
    retrying: "Retrying...",
    choosing: "Checking license...",
    restarting: "Restarting...",
    actionFailed: "The action could not be completed. Try again."
  },
  ru: {
    title: "Telegram Companion требует внимания",
    diagnosticCode: "Диагностический код",
    retry: "Повторить",
    chooseAnotherLicense: "Выбрать другую лицензию",
    retrying: "Повторная попытка...",
    choosing: "Проверка лицензии...",
    restarting: "Перезапуск...",
    actionFailed: "Не удалось выполнить действие. Повторите попытку."
  }
};

export function normalizeRecoveryCode(value: unknown): RecoveryCode {
  return typeof value === "string" && recoveryCodeSet.has(value)
    ? value as RecoveryCode
    : "runtime_unavailable";
}

export function normalizeDesktopStartupStatus(value: unknown): DesktopStartupStatus {
  if (!value || typeof value !== "object") {
    return { mode: "recovery", errorCode: "runtime_unavailable", restarting: false };
  }

  const candidate = value as Record<string, unknown>;
  const restarting = candidate.restarting === true;
  if (candidate.mode === "recovery") {
    return {
      mode: "recovery",
      errorCode: normalizeRecoveryCode(candidate.errorCode),
      restarting
    };
  }
  if (candidate.mode === "activation" || candidate.mode === "workspace") {
    return { mode: candidate.mode, restarting };
  }
  return { mode: "recovery", errorCode: "runtime_unavailable", restarting: false };
}

export function desktopStartupView(status: DesktopStartupStatus | null | undefined): DesktopStartupView {
  if (!status) return "loading";
  if (status.mode === "recovery") return "recovery";
  if (status.mode === "activation") return "activation";
  return "workspace";
}

export function recoveryCopy(errorCode: unknown, locale: DesktopStartupLocale): RecoveryCopy {
  const code = normalizeRecoveryCode(errorCode);
  return {
    code,
    ...recoveryLabels[locale],
    message: recoveryMessages[locale][code]
  };
}
