export const scaleSteps = [80, 90, 100, 110, 125, 150] as const;
export type UIScale = typeof scaleSteps[number];

export type Viewport = { width: number; height: number };
export type ScaleStorage = Pick<Storage, "getItem" | "setItem">;

type ScaleEventTarget = {
  addEventListener(type: "keydown", listener: (event: KeyboardEvent) => void): void;
  removeEventListener(type: "keydown", listener: (event: KeyboardEvent) => void): void;
};

type ScaleShortcutOptions = {
  target: ScaleEventTarget;
  storage: ScaleStorage;
  initialScale: UIScale;
  onScale(scale: UIScale): void;
};

const STORAGE_KEY = "telegram-companion.ui-scale";

export function nextScale(current: number, direction: -1 | 1): UIScale {
  const index = scaleSteps.findIndex((step) => step >= current);
  const currentIndex = index < 0 ? scaleSteps.length - 1 : index;
  const nextIndex = Math.max(0, Math.min(scaleSteps.length - 1, currentIndex + direction));
  return scaleSteps[nextIndex];
}

export function autoScale(viewport: Viewport, devicePixelRatio: number): UIScale {
  const ratio = Number.isFinite(devicePixelRatio) && devicePixelRatio > 0 ? devicePixelRatio : 1;
  const width = viewport.width / ratio;
  const height = viewport.height / ratio;
  if (width >= 2560 && height >= 1400) return 125;
  if (width >= 1600 && height >= 900) return 100;
  if (width >= 1200 && height >= 720) return 90;
  return 80;
}

export function readStoredScale(storage: Pick<Storage, "getItem">): UIScale | null {
  const value = Number(storage.getItem(STORAGE_KEY));
  return scaleSteps.includes(value as UIScale) ? value as UIScale : null;
}

export function persistScale(storage: Pick<Storage, "setItem">, scale: UIScale): void {
  storage.setItem(STORAGE_KEY, String(scale));
}

export function installScaleShortcuts(options: ScaleShortcutOptions): () => void {
  let scale = options.initialScale;
  const listener = (event: KeyboardEvent) => {
    if (!event.ctrlKey && !event.metaKey) return;
    let next: UIScale | null = null;
    if (event.key === "+" || event.key === "=") next = nextScale(scale, 1);
    if (event.key === "-") next = nextScale(scale, -1);
    if (event.key === "0") next = 100;
    if (next == null) return;
    event.preventDefault();
    scale = next;
    persistScale(options.storage, scale);
    options.onScale(scale);
  };
  options.target.addEventListener("keydown", listener);
  return () => options.target.removeEventListener("keydown", listener);
}
