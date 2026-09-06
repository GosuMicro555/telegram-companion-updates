import type { CanonicalClass } from "./analyticsController";

export type AnalyticsColumnId = "canonical" | "class" | "language" | "frequency" | "messages" | "trend" | "lastSeen" | "forms" | "actions";
export type AnalyticsTableTab = CanonicalClass;

export type AnalyticsTablePreferences = {
  order: AnalyticsColumnId[];
  visible: AnalyticsColumnId[];
  widths: Partial<Record<AnalyticsColumnId, number>>;
  sortBy: AnalyticsColumnId;
  sortDirection: SortDirection;
  pageSize: number;
};

export type SortDirection = "asc" | "desc" | "none";

export type AnalyticsTablePreferencePayload = {
  Tab: AnalyticsTableTab;
  Columns: Array<{ Key: AnalyticsColumnId; Width: number; Visible: boolean }>;
  SortBy: AnalyticsColumnId;
  SortDirection: "none" | "ascending" | "descending";
  PageSize: number;
};

export type AnalyticsTablePreferencesDependencies = {
  load(tab: AnalyticsTableTab): Promise<unknown>;
  save(tab: AnalyticsTableTab, preference: AnalyticsTablePreferencePayload): Promise<void>;
};

export function createAnalyticsTablePreferenceSaveQueue(
  dependencies: AnalyticsTablePreferencesDependencies,
  onError: (cause: unknown) => void
) {
  let tail = Promise.resolve();
  return {
    save(tab: AnalyticsTableTab, preference: AnalyticsTablePreferencePayload): Promise<void> {
      const next = tail.then(async () => {
        try {
          await dependencies.save(tab, preference);
        } catch (cause) {
          onError(cause);
        }
      });
      tail = next;
      return next;
    }
  };
}

export const analyticsColumnIds: AnalyticsColumnId[] = ["canonical", "class", "language", "frequency", "messages", "trend", "lastSeen", "forms", "actions"];

const defaultWidths: Record<AnalyticsColumnId, number> = {
  canonical: 260,
  class: 116,
  language: 72,
  frequency: 128,
  messages: 94,
  trend: 128,
  lastSeen: 164,
  forms: 76,
  actions: 144
};

export function defaultAnalyticsTablePreferences(_tab: AnalyticsTableTab): AnalyticsTablePreferences {
  return {
    order: [...analyticsColumnIds],
    visible: [...analyticsColumnIds],
    widths: { ...defaultWidths },
    sortBy: "frequency",
    sortDirection: "desc",
    pageSize: 20
  };
}

export function normalizeAnalyticsTablePreferences(value: unknown): AnalyticsTablePreferences {
  const fallback = defaultAnalyticsTablePreferences("neutral");
  if (!value || typeof value !== "object") return fallback;
  const raw = value as Record<string, unknown>;
  const columns = knownColumns(field(raw, "Columns", "columns"));
  const persistedOrder = columns.length ? columns.map((column) => column.key) : uniqueKnownColumns(field(raw, "order", "Order"));
  const order = columns.length ? persistedOrder : [...persistedOrder, ...analyticsColumnIds.filter((column) => !persistedOrder.includes(column))];
  const visible = columns.length ? columns.filter((column) => column.visible).map((column) => column.key) : uniqueKnownColumns(field(raw, "visible", "Visible"));
  const widths = columns.length ? Object.fromEntries(columns.map((column) => [column.key, column.width])) : knownWidths(field(raw, "widths", "Widths"));
  const upgradedWidths = upgradeLegacyWidths(widths, persistedOrder);
  return {
    order,
    visible: visible.length ? visible : fallback.visible,
    widths: { ...fallback.widths, ...upgradedWidths },
    sortBy: knownColumn(field(raw, "SortBy", "sortBy")) ?? fallback.sortBy,
    sortDirection: normalizeSortDirection(field(raw, "SortDirection", "sortDirection")) ?? fallback.sortDirection,
    pageSize: positiveInteger(field(raw, "PageSize", "pageSize")) ?? fallback.pageSize
  };
}

function upgradeLegacyWidths(widths: Partial<Record<AnalyticsColumnId, number>>, persistedOrder: AnalyticsColumnId[]): Partial<Record<AnalyticsColumnId, number>> {
  const newColumns: AnalyticsColumnId[] = ["class", "trend", "lastSeen"];
  if (widths.frequency !== 86 || newColumns.some((column) => persistedOrder.includes(column))) return widths;
  return { ...widths, frequency: defaultWidths.frequency };
}

export function withAnalyticsTableSort(preferences: AnalyticsTablePreferences, sort: { column: AnalyticsColumnId; direction: SortDirection }): AnalyticsTablePreferences {
  return { ...preferences, sortBy: sort.column, sortDirection: sort.direction };
}

export function reorderAnalyticsColumns(order: AnalyticsColumnId[], dragged: AnalyticsColumnId, target: AnalyticsColumnId): AnalyticsColumnId[] {
  if (dragged === target || !order.includes(dragged) || !order.includes(target)) return [...order];
  const next = order.filter((column) => column !== dragged);
  next.splice(next.indexOf(target), 0, dragged);
  return next;
}

export function resolveDraggedAnalyticsColumn(active: AnalyticsColumnId | null, transferValue: string): AnalyticsColumnId | undefined {
  return active ?? knownColumn(transferValue);
}

export function toAnalyticsTablePreference(tab: AnalyticsTableTab, preferences: AnalyticsTablePreferences): AnalyticsTablePreferencePayload {
  return {
    Tab: tab,
    Columns: preferences.order.map((key) => ({ Key: key, Width: preferences.widths[key] ?? defaultWidths[key], Visible: preferences.visible.includes(key) })),
    SortBy: preferences.sortBy,
    SortDirection: preferences.sortDirection === "asc" ? "ascending" : preferences.sortDirection === "desc" ? "descending" : "none",
    PageSize: preferences.pageSize
  };
}

function uniqueKnownColumns(value: unknown): AnalyticsColumnId[] {
  if (!Array.isArray(value)) return [];
  return value.reduce<AnalyticsColumnId[]>((columns, item) => {
    if (typeof item === "string" && analyticsColumnIds.includes(item as AnalyticsColumnId) && !columns.includes(item as AnalyticsColumnId)) {
      columns.push(item as AnalyticsColumnId);
    }
    return columns;
  }, []);
}

function knownWidths(value: unknown): Partial<Record<AnalyticsColumnId, number>> {
  if (!value || typeof value !== "object") return {};
  const widths: Partial<Record<AnalyticsColumnId, number>> = {};
  for (const column of analyticsColumnIds) {
    const width = (value as Record<string, unknown>)[column];
    if (typeof width === "number" && Number.isFinite(width) && width >= 48 && width <= 900) widths[column] = width;
  }
  return widths;
}

function knownColumns(value: unknown): Array<{ key: AnalyticsColumnId; width: number; visible: boolean }> {
  if (!Array.isArray(value)) return [];
  const seen: AnalyticsColumnId[] = [];
  return value.reduce<Array<{ key: AnalyticsColumnId; width: number; visible: boolean }>>((columns, item) => {
    if (!item || typeof item !== "object") return columns;
    const raw = item as Record<string, unknown>;
    const key = knownColumn(field(raw, "Key", "key"));
    const width = field(raw, "Width", "width");
    const visible = field(raw, "Visible", "visible");
    if (key && !seen.includes(key) && typeof width === "number" && Number.isFinite(width) && width >= 48 && width <= 900 && typeof visible === "boolean") {
      seen.push(key);
      columns.push({ key, width, visible });
    }
    return columns;
  }, []);
}

function knownColumn(value: unknown): AnalyticsColumnId | undefined {
  return typeof value === "string" && analyticsColumnIds.includes(value as AnalyticsColumnId) ? value as AnalyticsColumnId : undefined;
}

function normalizeSortDirection(value: unknown): SortDirection | undefined {
  if (value === "asc" || value === "ascending") return "asc";
  if (value === "desc" || value === "descending") return "desc";
  return value === "none" ? "none" : undefined;
}

function positiveInteger(value: unknown): number | undefined {
  return typeof value === "number" && Number.isInteger(value) && value > 0 ? value : undefined;
}

function field(record: Record<string, unknown>, primary: string, alternate: string): unknown {
  return record[primary] ?? record[alternate];
}
