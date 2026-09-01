import { useRef, useState } from "react";
import { Copy, FileUp, LoaderCircle } from "lucide-react";

import type { ActivationSnapshot } from "./activation";

export type ActivationScreenLabels = {
  activate: string;
  activationError: string;
  copyMachineID: string;
  importLicense: string;
  licenseKey: string;
  loading: string;
  machineID: string;
  title: string;
  validating: string;
};

export type ActivationScreenProps = {
  snapshot: ActivationSnapshot;
  labels: ActivationScreenLabels;
  error?: string;
  onActivate: (licenseKey: string) => Promise<void> | void;
  onCopyMachineID: (machineID: string) => Promise<void> | void;
  onImportLicense: () => Promise<void> | void;
};

export type ActivationOperation = "activate" | "copy" | "import";

export function createActivationActionRunner(
  onPendingChange: (operation: ActivationOperation | "") => void
) {
  let pending = false;

  return {
    isPending: () => pending,
    async run(
      operation: ActivationOperation,
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

export function activationButtonLabel(
  busy: ActivationOperation | "",
  labels: Pick<ActivationScreenLabels, "activate" | "validating">
): string {
  return busy === "activate" ? labels.validating : labels.activate;
}

export function activationDisplayedError(
  operationFailed: boolean,
  error: string,
  labels: Pick<ActivationScreenLabels, "activationError">
): string {
  return operationFailed ? labels.activationError : error;
}

export function ActivationScreen({
  error = "",
  labels,
  onActivate,
  onCopyMachineID,
  onImportLicense,
  snapshot
}: ActivationScreenProps) {
  const [licenseKey, setLicenseKey] = useState("");
  const [busy, setBusy] = useState<ActivationOperation | "">("");
  const [operationFailed, setOperationFailed] = useState(false);
  const actionRunner = useRef<ReturnType<typeof createActivationActionRunner> | null>(null);
  if (!actionRunner.current) {
    actionRunner.current = createActivationActionRunner(setBusy);
  }
  const isChecking = snapshot.state === "checking";
  const displayedError = activationDisplayedError(operationFailed, error, labels);

  const run = (operation: ActivationOperation, action: () => Promise<void> | void) => {
    setOperationFailed(false);
    return actionRunner.current!.run(operation, action, () => setOperationFailed(true));
  };

  if (isChecking) {
    return <main aria-busy="true" className="activationScreen" role="status">
      <LoaderCircle aria-hidden="true" className="activationScreen__spinner" />
      <span>{labels.loading}</span>
    </main>;
  }

  const machineID = snapshot.machineID ?? "";
  const isBusy = busy !== "";

  return <main className="activationScreen">
    <section aria-labelledby="activation-title" className="glassPanel activationScreen__panel">
      <header>
        <h1 id="activation-title">{labels.title}</h1>
      </header>
      <div className="activationScreen__machineID">
        <span>{labels.machineID}</span>
        <code>{machineID}</code>
        <button
          aria-label={labels.copyMachineID}
          className="secondaryButton"
          disabled={!machineID || isBusy}
          onClick={() => { void run("copy", () => onCopyMachineID(machineID)); }}
          title={labels.copyMachineID}
          type="button"
        >
          <Copy aria-hidden="true" size={16} />
        </button>
      </div>
      <label className="activationScreen__licenseKey">
        <span>{labels.licenseKey}</span>
        <textarea
          aria-label={labels.licenseKey}
          disabled={isBusy}
          onChange={(event) => setLicenseKey(event.target.value)}
          value={licenseKey}
        />
      </label>
      {displayedError ? <p className="inlineError" role="alert">{displayedError}</p> : null}
      <footer>
        <button
          className="secondaryButton"
          disabled={isBusy}
          onClick={() => { void run("import", onImportLicense); }}
          type="button"
        >
          <FileUp aria-hidden="true" size={16} />
          <span>{labels.importLicense}</span>
        </button>
        <button
          className="primaryButton"
          disabled={!licenseKey.trim() || isBusy}
          onClick={() => { void run("activate", () => onActivate(licenseKey.trim())); }}
          type="button"
        >
          {busy === "activate" ? <LoaderCircle aria-hidden="true" className="activationScreen__spinner" size={16} /> : null}
          <span>{activationButtonLabel(busy, labels)}</span>
        </button>
      </footer>
    </section>
  </main>;
}
