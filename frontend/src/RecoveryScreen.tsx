import { useRef, useState } from "react";
import { FileUp, LoaderCircle, RefreshCw } from "lucide-react";

import {
  recoveryCopy,
  type DesktopStartupLocale,
  type DesktopStartupStatus
} from "./desktopStartup";

export type RecoveryOperation = "retry" | "choose";

export type RecoveryScreenProps = {
  locale: DesktopStartupLocale;
  status: DesktopStartupStatus;
  onRetry: () => Promise<void> | void;
  onChooseAnotherLicense: () => Promise<void> | void;
};

export function createRecoveryActionRunner(
  onPendingChange: (operation: RecoveryOperation | "") => void
) {
  let pending = false;

  return {
    isPending: () => pending,
    async run(
      operation: RecoveryOperation,
      action: () => Promise<void> | void,
      onError: () => void
    ): Promise<boolean> {
      if (pending) return false;

      pending = true;
      onPendingChange(operation);
      try {
        await action();
        return true;
      } catch {
        onError();
        return false;
      } finally {
        pending = false;
        onPendingChange("");
      }
    }
  };
}

export function RecoveryScreen({
  locale,
  onChooseAnotherLicense,
  onRetry,
  status
}: RecoveryScreenProps) {
  const [busy, setBusy] = useState<RecoveryOperation | "">("");
  const [operationFailed, setOperationFailed] = useState(false);
  const actionRunner = useRef<ReturnType<typeof createRecoveryActionRunner> | null>(null);
  if (!actionRunner.current) {
    actionRunner.current = createRecoveryActionRunner(setBusy);
  }

  const copy = recoveryCopy(status.errorCode, locale);
  const isBusy = status.restarting === true || busy !== "";
  const run = (operation: RecoveryOperation, action: () => Promise<void> | void) => {
    setOperationFailed(false);
    return actionRunner.current!.run(operation, action, () => setOperationFailed(true));
  };

  const retryLabel = status.restarting
    ? copy.restarting
    : busy === "retry" ? copy.retrying : copy.retry;
  const chooseLabel = busy === "choose" ? copy.choosing : copy.chooseAnotherLicense;

  return <main aria-busy={isBusy ? "true" : undefined} className="recoveryScreen">
    <section aria-labelledby="recovery-title" className="glassPanel recoveryScreen__panel">
      <header>
        <h1 id="recovery-title">{copy.title}</h1>
      </header>
      <p className="recoveryScreen__message" role="alert">{copy.message}</p>
      <p className="recoveryScreen__code">
        <span>{copy.diagnosticCode}</span>
        <code>{copy.code}</code>
      </p>
      {operationFailed ? <p className="inlineError" role="alert">{copy.actionFailed}</p> : null}
      <footer className="recoveryScreen__actions">
        <button
          className="primaryButton"
          disabled={isBusy}
          onClick={() => { void run("retry", onRetry); }}
          type="button"
        >
          {isBusy ? <LoaderCircle aria-hidden="true" className="activationScreen__spinner" size={16} /> : <RefreshCw aria-hidden="true" size={16} />}
          <span>{retryLabel}</span>
        </button>
        <button
          className="secondaryButton"
          disabled={isBusy}
          onClick={() => { void run("choose", onChooseAnotherLicense); }}
          type="button"
        >
          <FileUp aria-hidden="true" size={16} />
          <span>{chooseLabel}</span>
        </button>
      </footer>
    </section>
  </main>;
}
