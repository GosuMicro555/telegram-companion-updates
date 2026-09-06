import { useRef, useState } from "react";
import { Download, LoaderCircle, RefreshCw } from "lucide-react";

import { updateButtonModel, type UpdateSnapshot } from "./updater";

export type UpdateButtonLabels = {
  download: (version: string) => string;
  downloading: (progress: number) => string;
  install: string;
  updateError: string;
};

export type UpdateButtonProps = {
  status: UpdateSnapshot;
  labels: UpdateButtonLabels;
  onDownload: () => Promise<void> | void;
  onInstall: () => Promise<void> | void;
};

export function createUpdateActionRunner(onPendingChange: (pending: boolean) => void) {
  let pending = false;

  return {
    isPending: () => pending,
    async run(action: () => Promise<void> | void, onError: () => void): Promise<boolean> {
      if (pending) return false;

      pending = true;
      onPendingChange(true);
      try {
        await action();
        return true;
      } catch {
        onError();
        return false;
      } finally {
        pending = false;
        onPendingChange(false);
      }
    }
  };
}

export function updateDisplayedError(
  operationFailed: boolean,
  labels: Pick<UpdateButtonLabels, "updateError">
): string {
  return operationFailed ? labels.updateError : "";
}

export function UpdateButton({ labels, onDownload, onInstall, status }: UpdateButtonProps) {
  const model = updateButtonModel(status);
  const [pending, setPending] = useState(false);
  const [operationFailed, setOperationFailed] = useState(false);
  const actionRunner = useRef<ReturnType<typeof createUpdateActionRunner> | null>(null);
  if (!actionRunner.current) {
    actionRunner.current = createUpdateActionRunner(setPending);
  }
  const run = (action: () => Promise<void> | void) => {
    setOperationFailed(false);
    return actionRunner.current!.run(action, () => setOperationFailed(true));
  };
  const displayedError = updateDisplayedError(operationFailed, labels);

  if (!model.visible) return null;

  if (model.action === "none") {
    return <button
      aria-busy="true"
      aria-label={labels.downloading(model.progress)}
      aria-valuemax={100}
      aria-valuemin={0}
      aria-valuenow={model.progress}
      className="secondaryButton updateButton"
      disabled
      role="progressbar"
      type="button"
    >
      <LoaderCircle aria-hidden="true" className="updateButton__spinner" size={16} />
      <span>{labels.downloading(model.progress)}</span>
    </button>;
  }

  if (model.action === "download") {
    return <>
      <button aria-busy={pending || undefined} className="secondaryButton updateButton" disabled={pending} onClick={() => { void run(onDownload); }} type="button">
      <Download aria-hidden="true" size={16} />
      <span>{labels.download(model.version ?? "")}</span>
      </button>
      {displayedError ? <p className="inlineError" role="alert">{displayedError}</p> : null}
    </>;
  }

  return <>
    <button aria-busy={pending || undefined} className="primaryButton updateButton" disabled={pending} onClick={() => { void run(onInstall); }} type="button">
      <RefreshCw aria-hidden="true" size={16} />
      <span>{labels.install}</span>
    </button>
    {displayedError ? <p className="inlineError" role="alert">{displayedError}</p> : null}
  </>;
}
