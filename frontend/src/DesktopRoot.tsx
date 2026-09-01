import { useEffect, useRef, useState, type ComponentType } from "react";

import App from "./App";
import { ActivationScreen, type ActivationScreenLabels } from "./ActivationScreen";
import { activationRestartFallback, activationSnapshotErrorMessage, type ActivationSnapshot } from "./activation";
import { desktopBridge, type DesktopBridge } from "./desktopBridge";
import { desktopStartupView, normalizeDesktopStartupStatus, type DesktopStartupStatus } from "./desktopStartup";
import { readStoredLocale, type Locale } from "./i18n";
import { RecoveryScreen } from "./RecoveryScreen";
import { readStoredTheme, resolveTheme, type ThemePreference } from "./theme";

type DesktopGateViewProps = {
  locale: Locale;
  snapshot: ActivationSnapshot;
  startup: DesktopStartupStatus | null;
  Workspace: ComponentType;
  onActivate: (token: string) => Promise<void> | void;
  onChooseAnotherLicense: () => Promise<void> | void;
  onImportLicense: () => Promise<void> | void;
  onRetryStartup: () => Promise<void> | void;
};

const activationLabels: Record<Locale, ActivationScreenLabels> = {
  ru: {
    activate: "\u0410\u043a\u0442\u0438\u0432\u0438\u0440\u043e\u0432\u0430\u0442\u044c",
    activationError: "\u041d\u0435 \u0443\u0434\u0430\u043b\u043e\u0441\u044c \u0430\u043a\u0442\u0438\u0432\u0438\u0440\u043e\u0432\u0430\u0442\u044c \u043f\u0440\u0438\u043b\u043e\u0436\u0435\u043d\u0438\u0435.",
    copyMachineID: "\u0421\u043a\u043e\u043f\u0438\u0440\u043e\u0432\u0430\u0442\u044c Machine ID",
    importLicense: "\u041e\u0442\u043a\u0440\u044b\u0442\u044c \u0444\u0430\u0439\u043b \u043b\u0438\u0446\u0435\u043d\u0437\u0438\u0438",
    licenseKey: "\u041a\u043b\u044e\u0447 \u043b\u0438\u0446\u0435\u043d\u0437\u0438\u0438",
    loading: "\u041f\u0440\u043e\u0432\u0435\u0440\u043a\u0430 \u0430\u043a\u0442\u0438\u0432\u0430\u0446\u0438\u0438...",
    machineID: "Machine ID",
    title: "\u0410\u043a\u0442\u0438\u0432\u0430\u0446\u0438\u044f Telegram Companion",
    validating: "\u041f\u0440\u043e\u0432\u0435\u0440\u043a\u0430..."
  },
  en: {
    activate: "Activate",
    activationError: "The application could not be activated.",
    copyMachineID: "Copy Machine ID",
    importLicense: "Open license file",
    licenseKey: "License key",
    loading: "Checking activation...",
    machineID: "Machine ID",
    title: "Activate Telegram Companion",
    validating: "Validating..."
  }
};

type DesktopShellElement = {
  lang: string;
  dataset: { theme?: string };
};

export function applyDesktopShellPreferences(
  root: DesktopShellElement,
  locale: Locale,
  theme: ThemePreference
): void {
  root.lang = locale;
  root.dataset.theme = resolveTheme(theme);
}

export function DesktopGateView({
  locale,
  onActivate,
  onChooseAnotherLicense,
  onImportLicense,
  onRetryStartup,
  snapshot,
  startup,
  Workspace
}: DesktopGateViewProps) {
  const view = desktopStartupView(startup);
  if (view === "loading") {
    return <ActivationScreen
      labels={activationLabels[locale]}
      onActivate={onActivate}
      onCopyMachineID={() => undefined}
      onImportLicense={onImportLicense}
      snapshot={{ mode: snapshot.mode, state: "checking" }}
    />;
  }
  if (view === "recovery") {
    return <RecoveryScreen
      locale={locale}
      onChooseAnotherLicense={onChooseAnotherLicense}
      onRetry={onRetryStartup}
      status={startup!}
    />;
  }
  if (view === "activation") {
    return <ActivationScreen
      error={activationSnapshotErrorMessage(snapshot, locale)}
      labels={activationLabels[locale]}
      onActivate={onActivate}
      onCopyMachineID={(machineID) => navigator.clipboard.writeText(machineID)}
      onImportLicense={onImportLicense}
      snapshot={snapshot}
    />;
  }
  return <Workspace />;
}

export function DesktopRoot({ bridge = desktopBridge }: { bridge?: DesktopBridge }) {
  const [locale] = useState<Locale>(() => readStoredLocale(localStorage));
  const [theme] = useState<ThemePreference>(() => readStoredTheme(localStorage));
  const [snapshot, setSnapshot] = useState<ActivationSnapshot>({
    mode: "public-macos-arm64",
    state: "checking"
  });
  const [startup, setStartup] = useState<DesktopStartupStatus | null>(null);
  const restartFallbackTimer = useRef<number | undefined>(undefined);

  useEffect(() => {
    applyDesktopShellPreferences(document.documentElement, locale, theme);
  }, [locale, theme]);

  useEffect(() => {
    let active = true;
    bridge.startup.status()
      .then(async (status) => {
        if (!active) return;
        setStartup(status);
        if (status.mode !== "activation") return;
        try {
          const activation = await bridge.activation.status();
          if (active) setSnapshot(activation);
        } catch {
          if (active) setSnapshot({ mode: "public-macos-arm64", state: "error", errorCode: "activation_unavailable" });
        }
      })
      .catch(() => { if (active) setStartup(normalizeDesktopStartupStatus(undefined)); });
    return () => {
      active = false;
      if (restartFallbackTimer.current !== undefined) window.clearTimeout(restartFallbackTimer.current);
    };
  }, [bridge]);

  const waitForRestart = async (operation: () => Promise<ActivationSnapshot>) => {
    const next = await operation();
    if (restartFallbackTimer.current !== undefined) window.clearTimeout(restartFallbackTimer.current);
    if (next.state === "activated") {
      setSnapshot({ mode: next.mode, state: "checking" });
      restartFallbackTimer.current = window.setTimeout(() => {
        setSnapshot(activationRestartFallback(next));
      }, 5000);
      return;
    }
    setSnapshot(next);
  };

  const runStartupAction = async (operation: () => Promise<DesktopStartupStatus>) => {
    const next = await operation();
    setStartup(next);
  };

  return <DesktopGateView
    locale={locale}
    onActivate={(token) => waitForRestart(() => bridge.activation.activate(token))}
    onChooseAnotherLicense={() => runStartupAction(bridge.startup.chooseAnotherLicense)}
    onImportLicense={() => waitForRestart(bridge.activation.importFile)}
    onRetryStartup={() => runStartupAction(bridge.startup.retry)}
    snapshot={snapshot}
    startup={startup}
    Workspace={App}
  />;
}
