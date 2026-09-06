import { renderToStaticMarkup } from "react-dom/server";
import { expect, test, vi } from "vitest";

import { ActivationScreen, activationButtonLabel, activationDisplayedError, createActivationActionRunner } from "./ActivationScreen";

const labels = {
  activate: "Activate",
  activationError: "Activation could not be completed.",
  copyMachineID: "Copy Machine ID",
  importLicense: "Import license file",
  licenseKey: "License key",
  loading: "Checking activation…",
  machineID: "Machine ID",
  title: "Activate Telegram Companion",
  validating: "Validating license…"
};

test("renders the isolated activation form with copy, token, and file-import actions", () => {
  const markup = renderToStaticMarkup(<ActivationScreen
    labels={labels}
    onActivate={async () => undefined}
    onCopyMachineID={() => undefined}
    onImportLicense={async () => undefined}
    snapshot={{ mode: "public-macos-arm64", state: "needs_activation", machineID: "A1B2C3" }}
  />);

  expect(markup).toContain("Activate Telegram Companion");
  expect(markup).toContain("A1B2C3");
  expect(markup).toContain("Copy Machine ID");
  expect(markup).toContain('aria-label="License key"');
  expect(markup).toContain("Import license file");
  expect(markup).toContain("lucide-copy");
  expect(markup).toContain("lucide-file-up");
  expect(markup).not.toContain("wailsjs");
});

test("renders a progress state and a non-secret backend error supplied by the integration", () => {
  const loading = renderToStaticMarkup(<ActivationScreen
    labels={labels}
    onActivate={async () => undefined}
    onCopyMachineID={() => undefined}
    onImportLicense={async () => undefined}
    snapshot={{ mode: "public-macos-arm64", state: "checking" }}
  />);
  const error = renderToStaticMarkup(<ActivationScreen
    error="License signature is invalid."
    labels={labels}
    onActivate={async () => undefined}
    onCopyMachineID={() => undefined}
    onImportLicense={async () => undefined}
    snapshot={{ mode: "public-macos-arm64", state: "error", machineID: "A1B2C3" }}
  />);

  expect(loading).toContain("Checking activation…");
  expect(loading).toContain('aria-busy="true"');
  expect(error).toContain('role="alert"');
  expect(error).toContain("License signature is invalid.");
});

test("keeps a revoked license in the replacement activation form without exposing identifiers", () => {
  const message = "The license was deactivated. Local data remains intact.";
  const markup = renderToStaticMarkup(<ActivationScreen
    error={message}
    labels={labels}
    onActivate={async () => undefined}
    onCopyMachineID={() => undefined}
    onImportLicense={async () => undefined}
    snapshot={{ mode: "public-macos-arm64", state: "revoked", machineID: "A1B2C3" }}
  />);

  expect(markup).toContain(message);
  expect(markup).toContain('aria-label="License key"');
  expect(markup).not.toContain("license-revoked-by-checker");
});

test("runs each activation callback once while pending so controls can disable", async () => {
  let resolve!: () => void;
  const action = vi.fn(() => new Promise<void>((resolvePromise) => { resolve = resolvePromise; }));
  const pending: string[] = [];
  const runner = createActivationActionRunner((operation) => pending.push(operation));

  const first = runner.run("activate", action, () => undefined);
  const second = runner.run("activate", action, () => undefined);

  expect(action).toHaveBeenCalledTimes(1);
  expect(runner.isPending()).toBe(true);
  expect(pending).toEqual(["activate"]);
  expect(await second).toBe(false);

  resolve();
  expect(await first).toBe(true);
  expect(runner.isPending()).toBe(false);
  expect(pending).toEqual(["activate", ""]);
});

test("handles a rejected copy callback with the safe component error", async () => {
  const errors: string[] = [];
  const runner = createActivationActionRunner(() => undefined);

  await expect(runner.run(
    "copy",
    () => Promise.reject(new Error("backend path: /private/license")),
    () => errors.push(labels.activationError)
  )).resolves.toBe(false);

  expect(errors).toEqual(["Activation could not be completed."]);
  expect(runner.isPending()).toBe(false);
});

test.each(["activate", "import"] as const)("handles a rejected %s callback with the same safe error", async (operation) => {
  const errors: string[] = [];
  const runner = createActivationActionRunner(() => undefined);

  await expect(runner.run(
    operation,
    () => Promise.reject(new Error("backend details")),
    () => errors.push(labels.activationError)
  )).resolves.toBe(false);

  expect(errors).toEqual(["Activation could not be completed."]);
  expect(runner.isPending()).toBe(false);
});

test("uses the current localized activation error after labels change", async () => {
  const errors: string[] = [];
  const runner = createActivationActionRunner(() => undefined);

  await runner.run(
    "activate",
    () => Promise.reject(new Error("backend details")),
    () => errors.push("\u0410\u043a\u0442\u0438\u0432\u0430\u0446\u0438\u044e \u043d\u0435 \u0443\u0434\u0430\u043b\u043e\u0441\u044c \u0437\u0430\u0432\u0435\u0440\u0448\u0438\u0442\u044c.")
  );

  expect(errors).toEqual(["\u0410\u043a\u0442\u0438\u0432\u0430\u0446\u0438\u044e \u043d\u0435 \u0443\u0434\u0430\u043b\u043e\u0441\u044c \u0437\u0430\u0432\u0435\u0440\u0448\u0438\u0442\u044c."]);
});

test("derives an operation failure from the current activation labels", () => {
  const russianLabels = {
    ...labels,
    activationError: "\u0410\u043a\u0442\u0438\u0432\u0430\u0446\u0438\u044e \u043d\u0435 \u0443\u0434\u0430\u043b\u043e\u0441\u044c \u0437\u0430\u0432\u0435\u0440\u0448\u0438\u0442\u044c."
  };

  expect(activationDisplayedError(true, "", labels)).toBe(labels.activationError);
  expect(activationDisplayedError(true, "", russianLabels)).toBe(russianLabels.activationError);
  expect(activationDisplayedError(false, "Safe integration error", russianLabels)).toBe("Safe integration error");
});

test("labels only an activation request as validating", () => {
  expect(activationButtonLabel("activate", labels)).toBe(labels.validating);
  expect(activationButtonLabel("copy", labels)).toBe("Activate");
  expect(activationButtonLabel("import", labels)).toBe("Activate");
  expect(activationButtonLabel("", labels)).toBe("Activate");
});
