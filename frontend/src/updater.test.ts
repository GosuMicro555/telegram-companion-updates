import { describe, expect, test } from "vitest";

import { clampUpdateProgress, updateButtonModel, updateErrorMessage } from "./updater";

describe("updater button model", () => {
  test.each(["disabled", "idle", "checking"] as const)("hides the topbar button while %s", (state) => {
    expect(updateButtonModel({ state })).toEqual({ action: "none", visible: false });
  });

  test("exposes a manual download action for an available public version", () => {
    expect(updateButtonModel({ state: "available", version: "0.8.0" })).toEqual({
      action: "download",
      version: "0.8.0",
      visible: true
    });
  });

  test("shows clamped download progress without a second update command", () => {
    expect(updateButtonModel({ state: "downloading", progress: 42.4 })).toEqual({
      action: "none",
      progress: 42,
      visible: true
    });
    expect(clampUpdateProgress(42.6)).toBe(43);
    expect(clampUpdateProgress(-2)).toBe(0);
    expect(clampUpdateProgress(140)).toBe(100);
    expect(clampUpdateProgress(Number.NaN)).toBe(0);
  });

  test("exposes installation only after the archive is ready", () => {
    expect(updateButtonModel({ state: "ready", version: "0.8.0" })).toEqual({
      action: "install",
      version: "0.8.0",
      visible: true
    });
  });

  test("keeps update errors out of the display model", () => {
    expect(updateButtonModel({ state: "error", errorCode: "backend path: /private/update" })).toEqual({
      action: "none",
      visible: false
    });
  });

  test("keeps the failed download or install action available for retry", () => {
    expect(updateButtonModel({ state: "error", retryState: "available", version: "0.8.0" })).toEqual({
      action: "download",
      version: "0.8.0",
      visible: true
    });
    expect(updateButtonModel({ state: "error", retryState: "ready", version: "0.8.0" })).toEqual({
      action: "install",
      version: "0.8.0",
      visible: true
    });
  });
});

describe("updater diagnostic messages", () => {
  test("maps stable backend codes without displaying native details", () => {
    expect(updateErrorMessage("update_signature_invalid", "ru")).toMatch(/подпись/i);
    expect(updateErrorMessage("update_signature_invalid", "ru")).toMatch(/подпись архива/i);
    expect(updateErrorMessage("update_signature_invalid", "ru")).toMatch(/целостность пакета/i);
    expect(updateErrorMessage("update_signature_invalid", "ru")).toMatch(/тем же сертификатом разработчика/i);
    expect(updateErrorMessage("update_signature_invalid", "en")).toMatch(/archive signature/i);
    expect(updateErrorMessage("update_signature_invalid", "en")).toMatch(/package integrity/i);
    expect(updateErrorMessage("update_signature_invalid", "en")).toMatch(/same developer certificate/i);
    expect(updateErrorMessage("update_validation_failed", "ru")).toMatch(/проверк/i);
    expect(updateErrorMessage("update_validation_failed", "ru")).toMatch(/целостность пакета/i);
    expect(updateErrorMessage("update_validation_failed", "ru")).toMatch(/тем же сертификатом разработчика/i);
    expect(updateErrorMessage("update_validation_failed", "en")).toMatch(/package integrity/i);
    expect(updateErrorMessage("update_validation_failed", "en")).toMatch(/same developer certificate/i);
    expect(updateErrorMessage("update_running_from_disk_image", "ru")).toMatch(/программ/i);
    expect(updateErrorMessage("update_install_failed", "en")).toContain("installed");
    expect(updateErrorMessage("/private/path", "en")).toBe("The update operation could not be completed.");
  });
});
