import { expect, test, vi } from "vitest";

import { bridgeFromRuntime } from "./desktopBridge";

test("desktop bridge delegates activation and updater calls without exposing globals to views", async () => {
  const runtime = {
    go: { wails: {
      ActivationBindings: {
        GetActivationStatus: vi.fn(async () => ({ mode: "internal", state: "activated" })),
        ActivateLicenseKey: vi.fn(async (token: string) => ({ mode: "public-macos-arm64", state: "activated", token })),
        ImportLicenseFile: vi.fn(async () => ({ mode: "public-macos-arm64", state: "activated" }))
      },
      StartupBindings: {
        GetDesktopStartupStatus: vi.fn(async () => ({ mode: "recovery", errorCode: "seed_unavailable" })),
        RetryStartup: vi.fn(async () => ({ mode: "recovery", errorCode: "seed_unavailable", restarting: true })),
        ChooseAnotherLicense: vi.fn(async () => ({ mode: "recovery", errorCode: "seed_unavailable", restarting: true }))
      },
      UpdateBindings: {
        GetUpdateStatus: vi.fn(async () => ({ state: "idle" })),
        CheckForUpdates: vi.fn(async () => ({ state: "available", version: "0.7.0" })),
        DownloadUpdate: vi.fn(async () => ({ state: "downloading", progress: 1 })),
        RestartAndInstallUpdate: vi.fn(async () => ({ state: "ready", version: "0.7.0" }))
      }
    }}
  };

  const bridge = bridgeFromRuntime(runtime as Parameters<typeof bridgeFromRuntime>[0]);

  await expect(bridge.activation.status()).resolves.toEqual({ mode: "internal", state: "activated" });
  await expect(bridge.activation.activate("TCPLIC1.token.signature")).resolves.toMatchObject({ state: "activated" });
  await expect(bridge.activation.importFile()).resolves.toMatchObject({ state: "activated" });
  await expect(bridge.startup.status()).resolves.toEqual({
    mode: "recovery",
    errorCode: "seed_unavailable",
    restarting: false
  });
  await expect(bridge.startup.retry()).resolves.toMatchObject({ restarting: true });
  await expect(bridge.startup.chooseAnotherLicense()).resolves.toMatchObject({ restarting: true });
  await expect(bridge.updates.check()).resolves.toEqual({ state: "available", version: "0.7.0" });
  expect(runtime.go.wails.ActivationBindings.ActivateLicenseKey).toHaveBeenCalledWith("TCPLIC1.token.signature");
  expect(runtime.go.wails.StartupBindings.RetryStartup).toHaveBeenCalledTimes(1);
});

test("missing Wails bindings fail closed instead of mounting the workspace", async () => {
  const bridge = bridgeFromRuntime({});

  await expect(bridge.activation.status()).resolves.toEqual({
    mode: "public-macos-arm64",
    state: "error",
    errorCode: "activation_unavailable"
  });
  await expect(bridge.activation.activate("token")).rejects.toThrow("activation unavailable");
  await expect(bridge.startup.status()).resolves.toEqual({
    mode: "recovery",
    errorCode: "runtime_unavailable",
    restarting: false
  });
  await expect(bridge.startup.retry()).rejects.toThrow("startup unavailable");
  await expect(bridge.startup.chooseAnotherLicense()).rejects.toThrow("startup unavailable");
  await expect(bridge.updates.status()).resolves.toEqual({ state: "disabled" });
});

test("startup bridge redacts malformed backend status", async () => {
  const bridge = bridgeFromRuntime({
    go: { wails: { StartupBindings: {
      GetDesktopStartupStatus: vi.fn(async () => ({
        mode: "unknown",
        errorCode: "/Users/alice/token=secret",
        restarting: "yes"
      })),
      RetryStartup: vi.fn(async () => ({ mode: "workspace" })),
      ChooseAnotherLicense: vi.fn(async () => ({ mode: "workspace" }))
    } } }
  } as unknown as Parameters<typeof bridgeFromRuntime>[0]);

  const status = await bridge.startup.status();

  expect(status).toEqual({ mode: "recovery", errorCode: "runtime_unavailable", restarting: false });
  expect(JSON.stringify(status)).not.toContain("alice");
  expect(JSON.stringify(status)).not.toContain("secret");
});
