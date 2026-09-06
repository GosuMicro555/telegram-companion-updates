import { readFileSync } from "node:fs";
import { renderToStaticMarkup } from "react-dom/server";
import { expect, test, vi } from "vitest";
import {
  SelectionActionMenu,
  clampSelectionActionMenuPosition,
  focusSelectionActionMenu,
  isSelectionActionMenuEventTarget,
  listenForSelectionActionMenuEscape,
  watchSelectionActionMenuPosition
} from "./SelectionActionMenu";

test.each([
  {
    action: "Add to negative keywords",
    label: "Add selected text to negative keywords",
    locale: "en"
  },
  {
    action: "\u0412 \u043d\u0435\u0433\u0430\u0442\u0438\u0432\u043d\u044b\u0435",
    label: "\u0414\u043e\u0431\u0430\u0432\u0438\u0442\u044c \u0432\u044b\u0434\u0435\u043b\u0435\u043d\u043d\u044b\u0439 \u0442\u0435\u043a\u0441\u0442 \u0432 \u043d\u0435\u0433\u0430\u0442\u0438\u0432\u043d\u044b\u0435 \u043a\u043b\u044e\u0447\u0435\u0432\u044b\u0435 \u0441\u043b\u043e\u0432\u0430",
    locale: "ru"
  }
] as const)("localizes the compact action and accessible menu label for $locale", ({ action, label, locale }) => {
  const markup = renderToStaticMarkup(
    <SelectionActionMenu
      busy
      error="Import failed"
      locale={locale}
      onAction={() => undefined}
      onClose={() => undefined}
      position={{ x: 40, y: 60 }}
    />
  );

  expect(markup).toContain(action);
  expect(markup).toContain(`aria-label="${label}"`);
  expect(markup).toContain("Import failed");
  expect(markup).toContain("disabled");
  expect(markup).toContain('role="menu"');
  expect(markup).toContain('role="menuitem"');
  expect(markup).toContain('role="alert"');
});

test("clamps the menu within every viewport edge", () => {
  expect(clampSelectionActionMenuPosition(
    { x: -30, y: -20 },
    { width: 120, height: 44 },
    { width: 800, height: 600 }
  )).toEqual({ x: 8, y: 8 });
  expect(clampSelectionActionMenuPosition(
    { x: 790, y: 590 },
    { width: 120, height: 44 },
    { width: 800, height: 600 }
  )).toEqual({ x: 672, y: 548 });
});

test("reclamps after menu resize and disconnects every observer on cleanup", () => {
  let size = { width: 120, height: 44 };
  let resizeObserverCallback: (() => void) | undefined;
  let viewportResizeCallback: EventListener | undefined;
  const element = { getBoundingClientRect: () => size };
  const observer = { disconnect: vi.fn(), observe: vi.fn() };
  const viewport = {
    addEventListener: vi.fn((_type: string, listener: EventListener) => { viewportResizeCallback = listener; }),
    innerHeight: 600,
    innerWidth: 800,
    removeEventListener: vi.fn()
  };
  const positions: Array<{ x: number; y: number }> = [];

  const cleanup = watchSelectionActionMenuPosition({
    createResizeObserver: (callback) => {
      resizeObserverCallback = callback;
      return observer;
    },
    element,
    onPosition: (position) => positions.push(position),
    position: { x: 790, y: 590 },
    viewport
  });

  expect(positions).toEqual([{ x: 672, y: 548 }]);
  size = { width: 220, height: 84 };
  resizeObserverCallback?.();
  expect(positions).toEqual([{ x: 672, y: 548 }, { x: 572, y: 508 }]);
  viewportResizeCallback?.(new Event("resize"));
  expect(positions.at(-1)).toEqual({ x: 572, y: 508 });

  cleanup();
  expect(observer.observe).toHaveBeenCalledWith(element);
  expect(observer.disconnect).toHaveBeenCalledTimes(1);
  expect(viewport.removeEventListener).toHaveBeenCalledWith("resize", viewportResizeCallback);
});

test("closes on Escape and removes its keyboard listener", () => {
  let listener: EventListener | undefined;
  const target = {
    addEventListener: vi.fn((_type: string, next: EventListener) => { listener = next; }),
    removeEventListener: vi.fn()
  };
  const onClose = vi.fn();
  const cleanup = listenForSelectionActionMenuEscape(target, onClose);

  listener?.({ key: "Enter" } as KeyboardEvent);
  listener?.({ key: "Escape" } as KeyboardEvent);
  cleanup();

  expect(onClose).toHaveBeenCalledTimes(1);
  expect(target.removeEventListener).toHaveBeenCalledWith("keydown", listener);
});

test("focuses the action and restores the connected previous target", () => {
  const action = { focus: vi.fn() };
  const previous = { focus: vi.fn(), isConnected: true };
  const restore = focusSelectionActionMenu(action, previous);

  expect(action.focus).toHaveBeenCalledTimes(1);
  restore();
  expect(previous.focus).toHaveBeenCalledTimes(1);

  const disconnected = { focus: vi.fn(), isConnected: false };
  focusSelectionActionMenu(action, disconnected)();
  expect(disconnected.focus).not.toHaveBeenCalled();
});

test("recognizes nested menu event targets without treating source cells as menu actions", () => {
  const menu = { nodeType: 1, parentElement: null, closest: (selector: string) => selector === ".selectionActionMenu" ? menu : null };
  const nested = { nodeType: 1, parentElement: menu, closest: (selector: string) => selector === ".selectionActionMenu" ? menu : null };
  const text = { nodeType: 3, parentElement: nested };
  const source = { nodeType: 1, parentElement: null, closest: () => null };

  expect(isSelectionActionMenuEventTarget(text as unknown as Node)).toBe(true);
  expect(isSelectionActionMenuEventTarget(source as unknown as Node)).toBe(false);
});

test("keeps the selection action fixed, compact, and above the live table", () => {
  const css = readFileSync(new URL("./styles.css", import.meta.url), "utf8");

  expect(css).toMatch(/\.selectionActionMenu\s*\{[^}]*position:\s*fixed/s);
  expect(css).toMatch(/\.selectionActionMenu\s*\{[^}]*z-index:\s*110/s);
  expect(css).toMatch(/\.selectionActionMenu__action\s*\{[^}]*min-height:\s*32px/s);
  expect(css).toMatch(/\.selectionActionMenu__error\s*\{[^}]*overflow-wrap:\s*anywhere/s);
});
