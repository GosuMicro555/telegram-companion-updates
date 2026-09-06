import { renderToStaticMarkup } from "react-dom/server";
import { expect, test, vi } from "vitest";

import { UpdateButton, createUpdateActionRunner, updateDisplayedError } from "./UpdateButton";

const labels = {
  download: (version: string) => `Update to ${version}`,
  downloading: (progress: number) => `Downloading ${progress}%`,
  install: "Restart and update",
  updateError: "Update could not be completed."
};

test("does not occupy topbar space until an update is available", () => {
  expect(renderToStaticMarkup(<UpdateButton labels={labels} onDownload={async () => undefined} onInstall={async () => undefined} status={{ state: "checking" }} />)).toBe("");
});

test("renders manual download, progress, and install actions without a check action", () => {
  const available = renderToStaticMarkup(<UpdateButton labels={labels} onDownload={async () => undefined} onInstall={async () => undefined} status={{ state: "available", version: "0.8.0" }} />);
  const downloading = renderToStaticMarkup(<UpdateButton labels={labels} onDownload={async () => undefined} onInstall={async () => undefined} status={{ state: "downloading", progress: 42 }} />);
  const ready = renderToStaticMarkup(<UpdateButton labels={labels} onDownload={async () => undefined} onInstall={async () => undefined} status={{ state: "ready", version: "0.8.0" }} />);

  expect(available).toContain("Update to 0.8.0");
  expect(available).toContain("lucide-download");
  expect(downloading).toContain("Downloading 42%");
  expect(downloading).toContain('aria-busy="true"');
  expect(downloading).toMatch(/<button(?=[^>]*role="progressbar")(?=[^>]*aria-valuemin="0")(?=[^>]*aria-valuemax="100")(?=[^>]*aria-valuenow="42")[^>]*>/);
  expect(ready).toContain("Restart and update");
  expect(ready).toContain("lucide-refresh-cw");
  expect(`${available}${downloading}${ready}`).not.toMatch(/check for updates/i);
});

test("runs a download callback once while pending so the button can disable", async () => {
  let resolve!: () => void;
  const download = vi.fn(() => new Promise<void>((resolvePromise) => { resolve = resolvePromise; }));
  const pending: boolean[] = [];
  const errors: string[] = [];
  const runner = createUpdateActionRunner((next) => pending.push(next));

  const first = runner.run(download, () => undefined);
  const second = runner.run(download, () => undefined);

  expect(download).toHaveBeenCalledTimes(1);
  expect(runner.isPending()).toBe(true);
  expect(pending).toEqual([true]);
  expect(await second).toBe(false);

  resolve();
  expect(await first).toBe(true);
  expect(pending).toEqual([true, false]);

  expect(errors).toEqual([]);
});

test.each(["download", "install"])("handles a rejected %s callback with the safe component error", async () => {
  const errors: string[] = [];
  const runner = createUpdateActionRunner(() => undefined);

  await expect(runner.run(
    () => Promise.reject(new Error("backend URL: https://private.example/update")),
    () => errors.push(labels.updateError)
  )).resolves.toBe(false);

  expect(errors).toEqual(["Update could not be completed."]);
  expect(runner.isPending()).toBe(false);
});

test("uses the current localized update error after labels change", async () => {
  const errors: string[] = [];
  const runner = createUpdateActionRunner(() => undefined);

  await runner.run(
    () => Promise.reject(new Error("backend details")),
    () => errors.push("\u041d\u0435 \u0443\u0434\u0430\u043b\u043e\u0441\u044c \u043e\u0431\u043d\u043e\u0432\u0438\u0442\u044c \u043f\u0440\u0438\u043b\u043e\u0436\u0435\u043d\u0438\u0435.")
  );

  expect(errors).toEqual(["\u041d\u0435 \u0443\u0434\u0430\u043b\u043e\u0441\u044c \u043e\u0431\u043d\u043e\u0432\u0438\u0442\u044c \u043f\u0440\u0438\u043b\u043e\u0436\u0435\u043d\u0438\u0435."]);
});

test("derives an operation failure from the current update labels", () => {
  const russianLabels = {
    ...labels,
    updateError: "\u041d\u0435 \u0443\u0434\u0430\u043b\u043e\u0441\u044c \u043e\u0431\u043d\u043e\u0432\u0438\u0442\u044c \u043f\u0440\u0438\u043b\u043e\u0436\u0435\u043d\u0438\u0435."
  };

  expect(updateDisplayedError(true, labels)).toBe(labels.updateError);
  expect(updateDisplayedError(true, russianLabels)).toBe(russianLabels.updateError);
  expect(updateDisplayedError(false, russianLabels)).toBe("");
});
