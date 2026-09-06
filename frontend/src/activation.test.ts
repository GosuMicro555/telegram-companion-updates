import { describe, expect, test } from "vitest";

import {
  activationErrorMessage,
  activationRestartFallback,
  activationSnapshotErrorMessage,
  activationView,
  type ActivationSnapshot
} from "./activation";

describe("activation model", () => {
  test("keeps the public shell loading while its activation status is checked", () => {
    const snapshot: ActivationSnapshot = { mode: "public-macos-arm64", state: "checking" };

    expect(activationView(snapshot)).toBe("loading");
  });

  test("shows activation for a public build that needs a license or reports an error", () => {
    expect(activationView({ mode: "public-macos-arm64", state: "needs_activation", machineID: "MAC-42" })).toBe("activation");
    expect(activationView({ mode: "public-macos-arm64", state: "error", errorCode: "invalid_signature" })).toBe("activation");
  });

  test("keeps revoked and online-check-required states inside the isolated activation shell", () => {
	 expect(activationView({ mode: "public-macos-arm64", state: "revoked" })).toBe("activation");
	 expect(activationView({ mode: "public-macos-arm64", state: "check_required" })).toBe("activation");
	 expect(activationErrorMessage("license_revoked", "ru")).toBe(
	   "Лицензия деактивирована владельцем. Локальные данные сохранены. Для восстановления доступа активируйте новую лицензию."
	 );
	 expect(activationErrorMessage("revocation_check_required", "ru")).toBe(
	   "Для проверки лицензии требуется подключение к интернету. Локальные данные сохранены."
	 );
	 expect(activationErrorMessage("license_revoked", "en")).toContain("Local data remains intact");
	 expect(activationErrorMessage("revocation_check_required", "en")).toContain("internet connection");
  });

  test("never treats activation state as authority to mount the workspace", () => {
    expect(activationView({ mode: "internal", state: "checking" })).toBe("loading");
    expect(activationView({ mode: "public-macos-arm64", state: "activated" })).toBe("activation");
  });

  test("maps non-secret activation errors to decoded concise Russian messages", () => {
    expect(activationErrorMessage("malformed", "ru")).toContain("\u043a\u043b\u044e\u0447");
    expect(activationErrorMessage("malformed_license", "ru")).toContain("\u043a\u043b\u044e\u0447");
    expect(activationErrorMessage("invalid_signature", "ru")).toContain("\u041f\u043e\u0434\u043f\u0438\u0441\u044c");
    expect(activationErrorMessage("wrong_machine", "ru")).toContain("\u0434\u0440\u0443\u0433\u043e\u0433\u043e Mac");
    expect(activationErrorMessage("wrong_product", "ru")).toContain("\u043f\u0440\u0438\u043b\u043e\u0436\u0435\u043d\u0438\u044f");
    expect(activationErrorMessage("wrong_channel", "ru")).toContain("\u0441\u0431\u043e\u0440\u043a\u0438");
    expect(activationErrorMessage("unsupported_schema", "ru")).toContain("\u0412\u0435\u0440\u0441\u0438\u044f");
    expect(activationErrorMessage("expired", "ru")).toContain("\u0438\u0441\u0442\u0451\u043a");
    expect(activationErrorMessage("unknown", "ru")).toContain("\u043f\u0440\u043e\u0432\u0435\u0440\u0438\u0442\u044c");
  });

  test("maps known activation errors to concise English messages and falls back safely", () => {
    expect(activationErrorMessage("malformed", "en")).toBe("The license key could not be read.");
    expect(activationErrorMessage("invalid_signature", "en")).toBe("The license signature is invalid.");
    expect(activationErrorMessage("wrong_machine", "en")).toBe("This license was issued for a different Mac.");
    expect(activationErrorMessage("backend details must stay private", "en")).toBe("The license could not be verified.");
    expect(activationErrorMessage(undefined, "en")).toBe("The license could not be verified.");
  });

  test("maps an unavailable machine ID to specific safe English and Russian messages", () => {
    expect(activationErrorMessage("machine_unavailable", "en")).toBe("The Machine ID could not be determined.");
    expect(activationErrorMessage("machine_unavailable", "ru")).toBe("\u041d\u0435 \u0443\u0434\u0430\u043b\u043e\u0441\u044c \u043e\u043f\u0440\u0435\u0434\u0435\u043b\u0438\u0442\u044c Machine ID.");
  });

  test("derives visible messages from terminal revocation states without exposing backend details", () => {
    expect(activationSnapshotErrorMessage({ state: "revoked" }, "ru")).toContain("Лицензия деактивирована владельцем.");
    expect(activationSnapshotErrorMessage({ state: "check_required" }, "ru")).toContain("подключение к интернету");
    expect(activationSnapshotErrorMessage({ state: "error", errorCode: "storage_error" }, "en")).toBe("The license could not be verified.");
    expect(activationSnapshotErrorMessage({ state: "needs_activation" }, "ru")).toBe("");
    expect(activationSnapshotErrorMessage({ state: "activated" }, "en")).toBe("");
  });

  test("keeps an activated shell closed and instructs a manual restart if relaunch does not finish", () => {
    const activated: ActivationSnapshot = {
      mode: "public-macos-arm64",
      state: "activated",
      machineID: "MAC-42"
    };

    expect(activationRestartFallback(activated)).toEqual({
      mode: "public-macos-arm64",
      state: "error",
      machineID: "MAC-42",
      errorCode: "restart_required"
    });
    expect(activationErrorMessage("restart_required", "ru")).toBe("Лицензия сохранена. Перезапустите приложение вручную.");
    expect(activationErrorMessage("restart_required", "en")).toBe("The license was saved. Restart the application manually.");
  });
});
