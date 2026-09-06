export type UpdateState = "disabled" | "idle" | "checking" | "available" | "downloading" | "ready" | "error";

export type UpdateSnapshot = {
  state: UpdateState;
  retryState?: UpdateState;
  version?: string;
  progress?: number;
  errorCode?: string;
};

export type UpdateLocale = "ru" | "en";

const updateErrorMessages: Record<UpdateLocale, Record<string, string>> = {
  ru: {
    update_signature_invalid: "Подпись обновления не подтверждена. Проверьте подпись архива и целостность пакета; при необходимости убедитесь, что сборка подписана тем же сертификатом разработчика, что и установленное приложение. Установка остановлена.",
    update_validation_failed: "Проверка обновления не пройдена. Проверьте подпись архива и целостность пакета; при необходимости убедитесь, что сборка подписана тем же сертификатом разработчика, что и установленное приложение. Установка остановлена.",
    update_running_from_disk_image: "Скопируйте приложение из образа диска в папку «Программы» и повторите обновление.",
    update_install_failed: "Не удалось установить обновление. Проверьте, что приложение находится в папке «Программы», и повторите попытку.",
    update_failed: "Не удалось выполнить операцию обновления."
  },
  en: {
    update_signature_invalid: "The update signature could not be verified. Check the archive signature and package integrity; if needed, make sure the build uses the same developer certificate as the installed application. Installation was stopped.",
    update_validation_failed: "The update could not be validated. Check the archive signature and package integrity; if needed, make sure the build uses the same developer certificate as the installed application. Installation was stopped.",
    update_running_from_disk_image: "Copy the application from the disk image to Applications, then try again.",
    update_install_failed: "The update could not be installed. Make sure the app is in Applications, then try again.",
    update_failed: "The update operation could not be completed."
  }
};

export function updateErrorMessage(errorCode: string | undefined, locale: UpdateLocale): string {
  return updateErrorMessages[locale][errorCode ?? ""] ?? updateErrorMessages[locale].update_failed;
}

export type UpdateButtonModel =
  | { visible: false; action: "none" }
  | { visible: true; action: "download"; version?: string }
  | { visible: true; action: "none"; progress: number }
  | { visible: true; action: "install"; version?: string };

export function clampUpdateProgress(progress: number | undefined): number {
  if (!Number.isFinite(progress)) return 0;
  return Math.round(Math.min(100, Math.max(0, progress!)));
}

export function updateButtonModel(snapshot: UpdateSnapshot): UpdateButtonModel {
  const actionableState = snapshot.state === "error" ? snapshot.retryState : snapshot.state;
  switch (actionableState) {
    case "available":
      return { visible: true, action: "download", version: snapshot.version };
    case "downloading":
      return { visible: true, action: "none", progress: clampUpdateProgress(snapshot.progress) };
    case "ready":
      return { visible: true, action: "install", version: snapshot.version };
    default:
      return { visible: false, action: "none" };
  }
}
