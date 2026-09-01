import { Plus } from "lucide-react";
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { t, type Locale } from "./i18n";

const VIEWPORT_MARGIN = 8;

export type SelectionActionMenuPosition = { x: number; y: number };
type MenuSize = { width: number; height: number };
type ViewportSize = { width: number; height: number };
type KeyboardTarget = {
  addEventListener(type: string, listener: EventListener): void;
  removeEventListener(type: string, listener: EventListener): void;
};
type FocusTarget = { focus(): void; isConnected: boolean };
type MenuElement = { getBoundingClientRect(): { width: number; height: number } };
type MenuResizeObserver = {
  disconnect(): void;
  observe(element: MenuElement): void;
};
type ResizeViewport = {
  innerHeight: number;
  innerWidth: number;
  addEventListener(type: "resize", listener: EventListener): void;
  removeEventListener(type: "resize", listener: EventListener): void;
};

export function clampSelectionActionMenuPosition(
  position: SelectionActionMenuPosition,
  menuSize: MenuSize,
  viewport: ViewportSize
): SelectionActionMenuPosition {
  const maxX = Math.max(VIEWPORT_MARGIN, viewport.width - menuSize.width - VIEWPORT_MARGIN);
  const maxY = Math.max(VIEWPORT_MARGIN, viewport.height - menuSize.height - VIEWPORT_MARGIN);
  return {
    x: Math.min(Math.max(VIEWPORT_MARGIN, position.x), maxX),
    y: Math.min(Math.max(VIEWPORT_MARGIN, position.y), maxY)
  };
}

export function watchSelectionActionMenuPosition({
  createResizeObserver,
  element,
  onPosition,
  position,
  viewport
}: {
  createResizeObserver?: (callback: () => void) => MenuResizeObserver;
  element: MenuElement;
  onPosition(position: SelectionActionMenuPosition): void;
  position: SelectionActionMenuPosition;
  viewport: ResizeViewport;
}): () => void {
  const updatePosition = () => {
    const { width, height } = element.getBoundingClientRect();
    onPosition(clampSelectionActionMenuPosition(
      position,
      { width, height },
      { width: viewport.innerWidth, height: viewport.innerHeight }
    ));
  };
  const observer = createResizeObserver?.(updatePosition);

  updatePosition();
  viewport.addEventListener("resize", updatePosition);
  observer?.observe(element);
  return () => {
    viewport.removeEventListener("resize", updatePosition);
    observer?.disconnect();
  };
}

export function listenForSelectionActionMenuEscape(
  target: KeyboardTarget,
  onClose: () => void
): () => void {
  const listener: EventListener = (event) => {
    if ((event as KeyboardEvent).key === "Escape") onClose();
  };
  target.addEventListener("keydown", listener);
  return () => target.removeEventListener("keydown", listener);
}

export function focusSelectionActionMenu(
  action: Pick<FocusTarget, "focus">,
  previous: FocusTarget | null
): () => void {
  action.focus();
  return () => {
    if (previous?.isConnected) previous.focus();
  };
}

export function isSelectionActionMenuEventTarget(target: Node | null): boolean {
  if (!target) return false;
  const element = target.nodeType === 1 ? target as Element : target.parentElement;
  return Boolean(element?.closest(".selectionActionMenu"));
}

export function SelectionActionMenu({
  busy = false,
  error = "",
  locale,
  onAction,
  onClose,
  position
}: {
  busy?: boolean;
  error?: string;
  locale: Locale;
  onAction(): void | Promise<void>;
  onClose(): void;
  position: SelectionActionMenuPosition;
}) {
  const [menuPosition, setMenuPosition] = useState(position);
  const menuRef = useRef<HTMLDivElement>(null);
  const actionRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (!actionRef.current) return;
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    return focusSelectionActionMenu(actionRef.current, previous);
  }, []);

  useEffect(() => listenForSelectionActionMenuEscape(window, onClose), [onClose]);

  useLayoutEffect(() => {
    if (!menuRef.current) return;
    const createResizeObserver = typeof ResizeObserver === "undefined"
      ? undefined
      : (callback: () => void): MenuResizeObserver => {
          const observer = new ResizeObserver(() => callback());
          return {
            disconnect: () => observer.disconnect(),
            observe: (element) => observer.observe(element as Element)
          };
        };
    return watchSelectionActionMenuPosition({
      createResizeObserver,
      element: menuRef.current,
      onPosition: setMenuPosition,
      position,
      viewport: window
    });
  }, [error, locale, position]);

  return <div
    aria-label={t(locale, "liveSelectionMenuLabel")}
    className="selectionActionMenu"
    ref={menuRef}
    role="menu"
    style={{ left: menuPosition.x, top: menuPosition.y }}
  >
    <button
      className="selectionActionMenu__action"
      disabled={busy}
      onClick={() => void onAction()}
      ref={actionRef}
      role="menuitem"
      type="button"
    >
      <Plus aria-hidden="true" size={15} />
      {t(locale, "liveSelectionAction")}
    </button>
    {error && <div className="selectionActionMenu__error" role="alert">{error}</div>}
  </div>;
}
