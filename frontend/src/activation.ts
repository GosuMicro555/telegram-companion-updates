export type ActivationMode = "internal" | "public-macos-arm64";
export type ActivationState = "checking" | "needs_activation" | "activated" | "error" | "revoked" | "check_required";

export type ActivationSnapshot = {
  mode: ActivationMode;
  state: ActivationState;
  machineID?: string;
  errorCode?: string;
};

export type ActivationView = "loading" | "activation";
export type ActivationLocale = "en" | "ru";

const activationErrors: Record<ActivationLocale, Record<string, string>> = {
  en: {
    expired: "The license has expired.",
    invalid_signature: "The license signature is invalid.",
    machine_unavailable: "The Machine ID could not be determined.",
    malformed: "The license key could not be read.",
    malformed_license: "The license key could not be read.",
    license_revoked: "The license was deactivated by its owner. Local data remains intact. Activate a new license to restore access.",
    revocation_check_required: "An internet connection is required to check the license. Local data remains intact.",
    restart_required: "The license was saved. Restart the application manually.",
    unsupported_schema: "This license version is not supported.",
    wrong_channel: "This license is not valid for this build.",
    wrong_machine: "This license was issued for a different Mac.",
    wrong_product: "This license is not valid for this application."
  },
  ru: {
    expired: "\u0421\u0440\u043e\u043a \u0434\u0435\u0439\u0441\u0442\u0432\u0438\u044f \u043b\u0438\u0446\u0435\u043d\u0437\u0438\u0438 \u0438\u0441\u0442\u0451\u043a.",
    invalid_signature: "\u041f\u043e\u0434\u043f\u0438\u0441\u044c \u043b\u0438\u0446\u0435\u043d\u0437\u0438\u0438 \u043d\u0435\u0434\u0435\u0439\u0441\u0442\u0432\u0438\u0442\u0435\u043b\u044c\u043d\u0430.",
    machine_unavailable: "\u041d\u0435 \u0443\u0434\u0430\u043b\u043e\u0441\u044c \u043e\u043f\u0440\u0435\u0434\u0435\u043b\u0438\u0442\u044c Machine ID.",
    malformed: "\u041d\u0435 \u0443\u0434\u0430\u043b\u043e\u0441\u044c \u043f\u0440\u043e\u0447\u0438\u0442\u0430\u0442\u044c \u043a\u043b\u044e\u0447 \u043b\u0438\u0446\u0435\u043d\u0437\u0438\u0438.",
    malformed_license: "\u041d\u0435 \u0443\u0434\u0430\u043b\u043e\u0441\u044c \u043f\u0440\u043e\u0447\u0438\u0442\u0430\u0442\u044c \u043a\u043b\u044e\u0447 \u043b\u0438\u0446\u0435\u043d\u0437\u0438\u0438.",
    license_revoked: "\u041b\u0438\u0446\u0435\u043d\u0437\u0438\u044f \u0434\u0435\u0430\u043a\u0442\u0438\u0432\u0438\u0440\u043e\u0432\u0430\u043d\u0430 \u0432\u043b\u0430\u0434\u0435\u043b\u044c\u0446\u0435\u043c. \u041b\u043e\u043a\u0430\u043b\u044c\u043d\u044b\u0435 \u0434\u0430\u043d\u043d\u044b\u0435 \u0441\u043e\u0445\u0440\u0430\u043d\u0435\u043d\u044b. \u0414\u043b\u044f \u0432\u043e\u0441\u0441\u0442\u0430\u043d\u043e\u0432\u043b\u0435\u043d\u0438\u044f \u0434\u043e\u0441\u0442\u0443\u043f\u0430 \u0430\u043a\u0442\u0438\u0432\u0438\u0440\u0443\u0439\u0442\u0435 \u043d\u043e\u0432\u0443\u044e \u043b\u0438\u0446\u0435\u043d\u0437\u0438\u044e.",
    revocation_check_required: "\u0414\u043b\u044f \u043f\u0440\u043e\u0432\u0435\u0440\u043a\u0438 \u043b\u0438\u0446\u0435\u043d\u0437\u0438\u0438 \u0442\u0440\u0435\u0431\u0443\u0435\u0442\u0441\u044f \u043f\u043e\u0434\u043a\u043b\u044e\u0447\u0435\u043d\u0438\u0435 \u043a \u0438\u043d\u0442\u0435\u0440\u043d\u0435\u0442\u0443. \u041b\u043e\u043a\u0430\u043b\u044c\u043d\u044b\u0435 \u0434\u0430\u043d\u043d\u044b\u0435 \u0441\u043e\u0445\u0440\u0430\u043d\u0435\u043d\u044b.",
    restart_required: "\u041b\u0438\u0446\u0435\u043d\u0437\u0438\u044f \u0441\u043e\u0445\u0440\u0430\u043d\u0435\u043d\u0430. \u041f\u0435\u0440\u0435\u0437\u0430\u043f\u0443\u0441\u0442\u0438\u0442\u0435 \u043f\u0440\u0438\u043b\u043e\u0436\u0435\u043d\u0438\u0435 \u0432\u0440\u0443\u0447\u043d\u0443\u044e.",
    unsupported_schema: "\u0412\u0435\u0440\u0441\u0438\u044f \u043b\u0438\u0446\u0435\u043d\u0437\u0438\u0438 \u043d\u0435 \u043f\u043e\u0434\u0434\u0435\u0440\u0436\u0438\u0432\u0430\u0435\u0442\u0441\u044f.",
    wrong_channel: "\u041b\u0438\u0446\u0435\u043d\u0437\u0438\u044f \u043d\u0435 \u043f\u043e\u0434\u0445\u043e\u0434\u0438\u0442 \u0434\u043b\u044f \u044d\u0442\u043e\u0439 \u0441\u0431\u043e\u0440\u043a\u0438.",
    wrong_machine: "\u041b\u0438\u0446\u0435\u043d\u0437\u0438\u044f \u0432\u044b\u043f\u0443\u0449\u0435\u043d\u0430 \u0434\u043b\u044f \u0434\u0440\u0443\u0433\u043e\u0433\u043e Mac.",
    wrong_product: "\u041b\u0438\u0446\u0435\u043d\u0437\u0438\u044f \u043d\u0435 \u043f\u043e\u0434\u0445\u043e\u0434\u0438\u0442 \u0434\u043b\u044f \u044d\u0442\u043e\u0433\u043e \u043f\u0440\u0438\u043b\u043e\u0436\u0435\u043d\u0438\u044f."
  }
};

export function activationView(snapshot: ActivationSnapshot): ActivationView {
  return snapshot.state === "checking" ? "loading" : "activation";
}

export function activationErrorMessage(errorCode: string | undefined, locale: ActivationLocale): string {
  return activationErrors[locale][errorCode ?? ""] ?? (locale === "ru"
    ? "\u041d\u0435 \u0443\u0434\u0430\u043b\u043e\u0441\u044c \u043f\u0440\u043e\u0432\u0435\u0440\u0438\u0442\u044c \u043b\u0438\u0446\u0435\u043d\u0437\u0438\u044e."
    : "The license could not be verified.");
}

/**
 * Converts a non-active backend state into the only user-facing activation
 * message that is safe to show. States that do not represent a failure keep
 * the activation form uncluttered.
 */
export function activationSnapshotErrorMessage(
  snapshot: Pick<ActivationSnapshot, "state" | "errorCode">,
  locale: ActivationLocale
): string {
  switch (snapshot.state) {
    case "revoked":
      return activationErrorMessage("license_revoked", locale);
    case "check_required":
      return activationErrorMessage("revocation_check_required", locale);
    case "error":
      return activationErrorMessage(snapshot.errorCode, locale);
    default:
      return "";
  }
}

export function activationRestartFallback(snapshot: ActivationSnapshot): ActivationSnapshot {
  if (snapshot.state !== "activated") return snapshot;
  return { ...snapshot, state: "error", errorCode: "restart_required" };
}
