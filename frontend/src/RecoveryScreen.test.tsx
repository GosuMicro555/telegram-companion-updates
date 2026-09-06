import { renderToStaticMarkup } from "react-dom/server";
import { expect, test, vi } from "vitest";

import { RecoveryScreen, createRecoveryActionRunner } from "./RecoveryScreen";

test("renders safe Russian recovery actions without backend details", () => {
  const markup = renderToStaticMarkup(<RecoveryScreen
    locale="ru"
    onChooseAnotherLicense={async () => undefined}
    onRetry={async () => undefined}
    status={{ mode: "recovery", errorCode: "seed_incompatible" }}
  />);

  expect(markup).toContain("Повторить");
  expect(markup).toContain("Выбрать другую лицензию");
  expect(markup).toContain("seed_incompatible");
  expect(markup).toContain("Лицензия не подходит");
  expect(markup).not.toContain('disabled=""');
  expect(markup).not.toContain("Reset");
  expect(markup).not.toContain("Удалить");
});

test("renders the English recovery controls and copy", () => {
  const markup = renderToStaticMarkup(<RecoveryScreen
    locale="en"
    onChooseAnotherLicense={async () => undefined}
    onRetry={async () => undefined}
    status={{ mode: "recovery", errorCode: "keychain_unavailable" }}
  />);

  expect(markup).toContain("Retry");
  expect(markup).toContain("Choose another license");
  expect(markup).toContain("Keychain is unavailable");
});

test("redacts an unknown raw error before rendering", () => {
  const raw = "/Users/alice/private-license token=secret";
  const markup = renderToStaticMarkup(<RecoveryScreen
    locale="en"
    onChooseAnotherLicense={async () => undefined}
    onRetry={async () => undefined}
    status={{ mode: "recovery", errorCode: raw }}
  />);

  expect(markup).toContain("runtime_unavailable");
  expect(markup).not.toContain(raw);
  expect(markup).not.toContain("alice");
  expect(markup).not.toContain("secret");
});

test("disables both actions while the backend is restarting", () => {
  const markup = renderToStaticMarkup(<RecoveryScreen
    locale="ru"
    onChooseAnotherLicense={async () => undefined}
    onRetry={async () => undefined}
    status={{ mode: "recovery", errorCode: "relaunch_failed", restarting: true }}
  />);

  expect(markup).toContain('aria-busy="true"');
  expect(markup).toContain("Перезапуск...");
  expect(markup.match(/disabled=""/g)).toHaveLength(2);
});

test("deduplicates retry and license actions while one action is pending", async () => {
  let resolve!: () => void;
  const action = vi.fn(() => new Promise<void>((resolvePromise) => { resolve = resolvePromise; }));
  const pending: string[] = [];
  const runner = createRecoveryActionRunner((operation) => pending.push(operation));

  const first = runner.run("retry", action, () => undefined);
  const duplicateRetry = runner.run("retry", action, () => undefined);
  const duplicateChoose = runner.run("choose", action, () => undefined);

  expect(action).toHaveBeenCalledTimes(1);
  expect(runner.isPending()).toBe(true);
  expect(await duplicateRetry).toBe(false);
  expect(await duplicateChoose).toBe(false);

  resolve();
  expect(await first).toBe(true);
  expect(runner.isPending()).toBe(false);
  expect(pending).toEqual(["retry", ""]);
});

test("turns a rejected action into a safe component error", async () => {
  const errors: string[] = [];
  const runner = createRecoveryActionRunner(() => undefined);

  await expect(runner.run(
    "choose",
    () => Promise.reject(new Error("/private/path token=secret")),
    () => errors.push("safe action error")
  )).resolves.toBe(false);

  expect(errors).toEqual(["safe action error"]);
});
