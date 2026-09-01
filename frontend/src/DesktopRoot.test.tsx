import { renderToStaticMarkup } from "react-dom/server";
import { expect, test } from "vitest";

import { DesktopGateView, applyDesktopShellPreferences } from "./DesktopRoot";

const Workspace = () => <div>WORKSPACE_SECRET_MARKER</div>;

test("public activation view never mounts the existing workspace", () => {
  const markup = renderToStaticMarkup(<DesktopGateView
    locale="ru"
    onChooseAnotherLicense={async () => undefined}
    onActivate={async () => undefined}
    onImportLicense={async () => undefined}
    onRetryStartup={async () => undefined}
    snapshot={{ mode: "public-macos-arm64", state: "needs_activation", machineID: "ABC" }}
    startup={{ mode: "activation" }}
    Workspace={Workspace}
  />);

  expect(markup).not.toContain("WORKSPACE_SECRET_MARKER");
  expect(markup).toContain("ABC");
});

test("recovery wins over an activated license and exposes only recovery actions", () => {
  const markup = renderToStaticMarkup(<DesktopGateView
    locale="ru"
    onChooseAnotherLicense={async () => undefined}
    onActivate={async () => undefined}
    onImportLicense={async () => undefined}
    onRetryStartup={async () => undefined}
    snapshot={{ mode: "public-macos-arm64", state: "activated" }}
    startup={{ mode: "recovery", errorCode: "seed_incompatible" }}
    Workspace={Workspace}
  />);

  expect(markup).not.toContain("WORKSPACE_SECRET_MARKER");
  expect(markup).toContain("Повторить");
  expect(markup).toContain("Выбрать другую лицензию");
  expect(markup).not.toContain('disabled=""');
});

test("an activated license cannot mount the workspace while backend mode is activation", () => {
  const markup = renderToStaticMarkup(<DesktopGateView
    locale="en"
    onChooseAnotherLicense={async () => undefined}
    onActivate={async () => undefined}
    onImportLicense={async () => undefined}
    onRetryStartup={async () => undefined}
    snapshot={{ mode: "public-macos-arm64", state: "activated" }}
    startup={{ mode: "activation" }}
    Workspace={Workspace}
  />);

  expect(markup).not.toContain("WORKSPACE_SECRET_MARKER");
  expect(markup).toContain("Activate Telegram Companion");
});

test.each([
  ["revoked", "Лицензия деактивирована владельцем."],
  ["check_required", "Для проверки лицензии требуется подключение к интернету."]
] as const)("shows a safe message for the %s activation state", (state, expectedMessage) => {
  const markup = renderToStaticMarkup(<DesktopGateView
    locale="ru"
    onChooseAnotherLicense={async () => undefined}
    onActivate={async () => undefined}
    onImportLicense={async () => undefined}
    onRetryStartup={async () => undefined}
    snapshot={{ mode: "public-macos-arm64", state }}
    startup={{ mode: "activation" }}
    Workspace={Workspace}
  />);

  expect(markup).toContain(expectedMessage);
  expect(markup).toContain('role="alert"');
});

test("mounts the workspace only when backend startup mode is workspace", () => {
  const markup = renderToStaticMarkup(<DesktopGateView
    locale="ru"
    onChooseAnotherLicense={async () => undefined}
    onActivate={async () => undefined}
    onImportLicense={async () => undefined}
    onRetryStartup={async () => undefined}
    snapshot={{ mode: "public-macos-arm64", state: "error", errorCode: "invalid_signature" }}
    startup={{ mode: "workspace" }}
    Workspace={Workspace}
  />);

  expect(markup).toContain("WORKSPACE_SECRET_MARKER");
});

test("loading takes precedence before any backend mode is known", () => {
  const markup = renderToStaticMarkup(<DesktopGateView
    locale="en"
    onChooseAnotherLicense={async () => undefined}
    onActivate={async () => undefined}
    onImportLicense={async () => undefined}
    onRetryStartup={async () => undefined}
    snapshot={{ mode: "public-macos-arm64", state: "activated" }}
    startup={null}
    Workspace={Workspace}
  />);

  expect(markup).not.toContain("WORKSPACE_SECRET_MARKER");
  expect(markup).toContain("Checking activation...");
});

test("applies stored locale and theme while App is unmounted", () => {
  const root = { lang: "", dataset: {} as { theme?: string } };

  applyDesktopShellPreferences(root, "ru", "dark");

  expect(root).toEqual({ lang: "ru", dataset: { theme: "dark" } });
});
