export function arrayFromBridge<T>(value: T[] | null | undefined): T[];
export function arrayFromBridge<T>(value: unknown): T[];
export function arrayFromBridge<T>(value: unknown): T[] {
  return Array.isArray(value) ? value as T[] : [];
}
