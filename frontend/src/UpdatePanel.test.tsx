import { renderToStaticMarkup } from "react-dom/server";
import { expect, test } from "vitest";

import { refreshUpdateStatus, UpdatePanelView } from "./UpdatePanel";
import type { DesktopBridge } from "./desktopBridge";
import type { UpdateSnapshot } from "./updater";

test("updates page includes an explicit Russian manual check action", () => {
  const markup = renderToStaticMarkup(<UpdatePanelView
    busy={false}
    error=""
    locale="ru"
    onCheck={async () => undefined}
    onDownload={async () => undefined}
    onInstall={async () => undefined}
    status={{ state: "idle" }}
  />);

  expect(markup).toContain("Проверить обновления");
  expect(markup).not.toContain("disabled");
});

test("manual check is disabled while checking and updater action is shown when available", () => {
  const checking = renderToStaticMarkup(<UpdatePanelView
    busy={true}
    error=""
    locale="en"
    onCheck={async () => undefined}
    onDownload={async () => undefined}
    onInstall={async () => undefined}
    status={{ state: "checking" }}
  />);
  const available = renderToStaticMarkup(<UpdatePanelView
    busy={false}
    error=""
    locale="en"
    onCheck={async () => undefined}
    onDownload={async () => undefined}
    onInstall={async () => undefined}
    status={{ state: "available", version: "0.7.0" }}
  />);

  expect(checking).toContain("disabled");
  expect(available).toContain("Download 0.7.0");
});

test("renders a persisted backend update failure after a status refresh", () => {
  const markup = renderToStaticMarkup(<UpdatePanelView
    busy={false}
    error=""
    locale="en"
    onCheck={async () => undefined}
    onDownload={async () => undefined}
    onInstall={async () => undefined}
    status={{ state: "error", retryState: "available", version: "0.7.0", errorCode: "update_failed" }}
  />);

  expect(markup).toContain("The update operation could not be completed.");
  expect(markup).toContain("Download 0.7.0");
});

test("renders a safe signature diagnostic from the backend error code", () => {
  const markup = renderToStaticMarkup(<UpdatePanelView
    busy={false}
    error=""
    locale="ru"
    onCheck={async () => undefined}
    onDownload={async () => undefined}
    onInstall={async () => undefined}
    status={{ state: "error", retryState: "available", version: "0.8.4", errorCode: "update_signature_invalid" }}
  />);

  expect(markup).toMatch(/подпись/i);
  expect(markup).not.toContain("/private");
});

test("prefers a classified backend diagnostic over a transient generic error", () => {
  const markup = renderToStaticMarkup(<UpdatePanelView
    busy={false}
    error="generic transient error"
    locale="en"
    onCheck={async () => undefined}
    onDownload={async () => undefined}
    onInstall={async () => undefined}
    status={{ state: "error", errorCode: "update_install_failed" }}
  />);

  expect(markup).toContain("installed");
  expect(markup).not.toContain("generic transient error");
});

test("a successful refresh clears a previous transient error", async () => {
  let status: UpdateSnapshot = { state: "error" };
  let error = "old error";
  const bridge = {
    updates: { status: async () => ({ state: "idle" as const }) }
  } as unknown as DesktopBridge;

  await refreshUpdateStatus(bridge, "ru", (next) => { status = next; }, (next) => { error = next; });

  expect(status).toEqual({ state: "idle" });
  expect(error).toBe("");
});
