import { useCallback, useEffect, useRef, useState } from "react";
import { RefreshCw } from "lucide-react";

import { desktopBridge, type DesktopBridge } from "./desktopBridge";
import type { Locale } from "./i18n";
import { UpdateButton } from "./UpdateButton";
import { updateErrorMessage, type UpdateSnapshot } from "./updater";

type UpdatePanelViewProps = {
  locale: Locale;
  status: UpdateSnapshot;
  busy: boolean;
  error: string;
  onCheck: () => Promise<void> | void;
  onDownload: () => Promise<void> | void;
  onInstall: () => Promise<void> | void;
};

const labels = {
  ru: {
    check: "\u041f\u0440\u043e\u0432\u0435\u0440\u0438\u0442\u044c \u043e\u0431\u043d\u043e\u0432\u043b\u0435\u043d\u0438\u044f",
    checking: "\u041f\u0440\u043e\u0432\u0435\u0440\u043a\u0430...",
    download: (version: string) => `\u0421\u043a\u0430\u0447\u0430\u0442\u044c ${version}`.trim(),
    downloading: (progress: number) => `\u0417\u0430\u0433\u0440\u0443\u0437\u043a\u0430 ${progress}%`,
    install: "\u041f\u0435\u0440\u0435\u0437\u0430\u043f\u0443\u0441\u0442\u0438\u0442\u044c \u0438 \u0443\u0441\u0442\u0430\u043d\u043e\u0432\u0438\u0442\u044c",
    error: "\u041d\u0435 \u0443\u0434\u0430\u043b\u043e\u0441\u044c \u0432\u044b\u043f\u043e\u043b\u043d\u0438\u0442\u044c \u043e\u043f\u0435\u0440\u0430\u0446\u0438\u044e \u043e\u0431\u043d\u043e\u0432\u043b\u0435\u043d\u0438\u044f."
  },
  en: {
    check: "Check for updates",
    checking: "Checking...",
    download: (version: string) => `Download ${version}`.trim(),
    downloading: (progress: number) => `Downloading ${progress}%`,
    install: "Restart and install",
    error: "The update operation could not be completed."
  }
} as const;

export async function refreshUpdateStatus(
  bridge: DesktopBridge,
  locale: Locale,
  setStatus: (status: UpdateSnapshot) => void,
  setError: (error: string) => void
): Promise<void> {
  try {
    setStatus(await bridge.updates.status());
    setError("");
  } catch {
    setError(labels[locale].error);
  }
}

export function UpdatePanelView({ busy, error, locale, onCheck, onDownload, onInstall, status }: UpdatePanelViewProps) {
  const text = labels[locale];
  const checking = busy || status.state === "checking";
  const displayedError = status.state === "error" && status.errorCode
    ? updateErrorMessage(status.errorCode, locale)
    : error;
  return <section className="updatePanel" aria-label={text.check}>
    <div className="updatePanel__actions">
      <button className="secondaryButton" disabled={checking || status.state === "disabled"} onClick={() => { void onCheck(); }} type="button">
        <RefreshCw aria-hidden="true" className={checking ? "updateButton__spinner" : ""} size={16} />
        <span>{checking ? text.checking : text.check}</span>
      </button>
      <UpdateButton
        labels={{ download: text.download, downloading: text.downloading, install: text.install, updateError: text.error }}
        onDownload={onDownload}
        onInstall={onInstall}
        status={status}
      />
    </div>
    {displayedError ? <p className="inlineError" role="alert">{displayedError}</p> : null}
  </section>;
}

export function UpdatePanel({ bridge = desktopBridge, locale }: { bridge?: DesktopBridge; locale: Locale }) {
  const [status, setStatus] = useState<UpdateSnapshot>({ state: "disabled" });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const busyRef = useRef(false);

  const refresh = useCallback(() => refreshUpdateStatus(bridge, locale, setStatus, setError), [bridge, locale]);

  useEffect(() => {
    void refresh();
    const timer = window.setInterval(() => { void refresh(); }, 5000);
    return () => window.clearInterval(timer);
  }, [refresh]);

  const run = async (operation: () => Promise<UpdateSnapshot>) => {
    if (busyRef.current) return;
    busyRef.current = true;
    setBusy(true);
    setError("");
    try {
      setStatus(await operation());
    } catch {
      setError(labels[locale].error);
      refresh();
    } finally {
      busyRef.current = false;
      setBusy(false);
    }
  };

  return <UpdatePanelView
    busy={busy}
    error={error}
    locale={locale}
    onCheck={() => run(bridge.updates.check)}
    onDownload={() => run(bridge.updates.download)}
    onInstall={() => run(bridge.updates.install)}
    status={status}
  />;
}
