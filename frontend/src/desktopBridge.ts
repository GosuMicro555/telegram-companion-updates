import type { ActivationSnapshot } from "./activation";
import { normalizeDesktopStartupStatus, type DesktopStartupStatus } from "./desktopStartup";
import type { UpdateSnapshot } from "./updater";

type ActivationRuntime = {
  GetActivationStatus: () => Promise<ActivationSnapshot>;
  ActivateLicenseKey: (token: string) => Promise<ActivationSnapshot>;
  ImportLicenseFile: () => Promise<ActivationSnapshot>;
};

type UpdateRuntime = {
  GetUpdateStatus: () => Promise<UpdateSnapshot>;
  CheckForUpdates: () => Promise<UpdateSnapshot>;
  DownloadUpdate: () => Promise<UpdateSnapshot>;
  RestartAndInstallUpdate: () => Promise<UpdateSnapshot>;
};

type StartupRuntime = {
  GetDesktopStartupStatus: () => Promise<DesktopStartupStatus>;
  RetryStartup: () => Promise<DesktopStartupStatus>;
  ChooseAnotherLicense: () => Promise<DesktopStartupStatus>;
};

type WailsRuntime = {
  go?: { wails?: {
    ActivationBindings?: ActivationRuntime;
    StartupBindings?: StartupRuntime;
    UpdateBindings?: UpdateRuntime;
  }};
};

export type DesktopBridge = ReturnType<typeof bridgeFromRuntime>;

export function bridgeFromRuntime(runtime: WailsRuntime) {
  const activation = runtime.go?.wails?.ActivationBindings;
  const startup = runtime.go?.wails?.StartupBindings;
  const updates = runtime.go?.wails?.UpdateBindings;

  const unavailableStartup = () => normalizeDesktopStartupStatus(undefined);

  return {
    activation: {
      status: async (): Promise<ActivationSnapshot> => activation
        ? activation.GetActivationStatus()
        : { mode: "public-macos-arm64", state: "error", errorCode: "activation_unavailable" },
      activate: async (token: string): Promise<ActivationSnapshot> => {
        if (!activation) throw new Error("activation unavailable");
        return activation.ActivateLicenseKey(token);
      },
      importFile: async (): Promise<ActivationSnapshot> => {
        if (!activation) throw new Error("activation unavailable");
        return activation.ImportLicenseFile();
      }
    },
    startup: {
      status: async (): Promise<DesktopStartupStatus> => startup
        ? normalizeDesktopStartupStatus(await startup.GetDesktopStartupStatus())
        : unavailableStartup(),
      retry: async (): Promise<DesktopStartupStatus> => {
        if (!startup) throw new Error("startup unavailable");
        return normalizeDesktopStartupStatus(await startup.RetryStartup());
      },
      chooseAnotherLicense: async (): Promise<DesktopStartupStatus> => {
        if (!startup) throw new Error("startup unavailable");
        return normalizeDesktopStartupStatus(await startup.ChooseAnotherLicense());
      }
    },
    updates: {
      status: async (): Promise<UpdateSnapshot> => updates
        ? updates.GetUpdateStatus()
        : { state: "disabled" },
      check: async (): Promise<UpdateSnapshot> => {
        if (!updates) return { state: "disabled" };
        return updates.CheckForUpdates();
      },
      download: async (): Promise<UpdateSnapshot> => {
        if (!updates) throw new Error("updates unavailable");
        return updates.DownloadUpdate();
      },
      install: async (): Promise<UpdateSnapshot> => {
        if (!updates) throw new Error("updates unavailable");
        return updates.RestartAndInstallUpdate();
      }
    }
  };
}

export const desktopBridge = bridgeFromRuntime(globalThis as WailsRuntime);
