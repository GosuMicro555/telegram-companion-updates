export const MIN_TABLE_COLUMN_WIDTH = 48;
export const MAX_TABLE_COLUMN_WIDTH = 900;

export type StorageLike = Pick<Storage, "getItem" | "setItem">;

export type ConfigurableStatsTablePreferences<ColumnID extends string = string> = {
  order: ColumnID[];
  visible: ColumnID[];
  widths: Record<ColumnID, number>;
};

export type TableColumnPreferenceDefinition<ColumnID extends string = string> = {
  id: ColumnID;
  width: number;
  [key: string]: unknown;
};

export type ConfigurableTableSort<ColumnID extends string = string> = {
  column: ColumnID;
  direction: "asc" | "desc";
};

export function tablePreferencesFromDefinitions<ColumnID extends string>(
  definitions: readonly TableColumnPreferenceDefinition<ColumnID>[]
): ConfigurableStatsTablePreferences<ColumnID> {
  const order = definitions.map((definition) => definition.id);
  return {
    order,
    visible: [...order],
    widths: Object.fromEntries(definitions.map((definition) => [definition.id, clampTableColumnWidth(definition.width)])) as Record<ColumnID, number>
  };
}

export function loadTablePreferences<ColumnID extends string>(
  storage: StorageLike | undefined,
  settingsKey: string,
  defaults: ConfigurableStatsTablePreferences<ColumnID>
): ConfigurableStatsTablePreferences<ColumnID> {
  if (!storage) return copyTablePreferences(defaults);
  try {
    const stored = storage.getItem(settingsKey);
    return stored ? normalizeTablePreferences(JSON.parse(stored), defaults) : copyTablePreferences(defaults);
  } catch {
    return copyTablePreferences(defaults);
  }
}

export function saveTablePreferences<ColumnID extends string>(
  storage: StorageLike | undefined,
  settingsKey: string,
  preferences: ConfigurableStatsTablePreferences<ColumnID>
): void {
  if (!storage) return;
  try {
    storage.setItem(settingsKey, JSON.stringify(preferences));
  } catch {
    // A disabled or full browser storage must not prevent table interaction.
  }
}

export function normalizeTablePreferences<ColumnID extends string>(
  value: unknown,
  defaults: ConfigurableStatsTablePreferences<ColumnID>
): ConfigurableStatsTablePreferences<ColumnID> {
  const source = value && typeof value === "object" ? value as Record<string, unknown> : {};
  const known = new Set<ColumnID>(defaults.order);
  const sourceOrder = validColumnIDs<ColumnID>(source.order, known);
  const order = [...sourceOrder, ...defaults.order.filter((id) => !sourceOrder.includes(id))];
  const requestedVisible = Array.isArray(source.visible) ? validColumnIDs<ColumnID>(source.visible, known) : defaults.visible.filter((id) => known.has(id));
  const visible = requestedVisible.length ? order.filter((id) => requestedVisible.includes(id)) : [order[0]];
  const sourceWidths = source.widths && typeof source.widths === "object" ? source.widths as Record<string, unknown> : {};
  const widths = Object.fromEntries(order.map((id) => [id, typeof sourceWidths[id] === "number" && Number.isFinite(sourceWidths[id])
    ? clampTableColumnWidth(sourceWidths[id])
    : clampTableColumnWidth(defaults.widths[id])])) as Record<ColumnID, number>;

  return { order, visible, widths };
}

export function reorderTableColumns<ColumnID extends string>(order: readonly ColumnID[], source: ColumnID, target: ColumnID): ColumnID[] {
  const sourceIndex = order.indexOf(source);
  const targetIndex = order.indexOf(target);
  if (sourceIndex < 0 || targetIndex < 0 || sourceIndex === targetIndex) return [...order];
  const next = [...order];
  next.splice(sourceIndex, 1);
  next.splice(targetIndex, 0, source);
  return next;
}

export function clampTableColumnWidth(width: number): number {
  return Math.max(MIN_TABLE_COLUMN_WIDTH, Math.min(MAX_TABLE_COLUMN_WIDTH, Math.round(width)));
}

export function cycleConfigurableTableSort<ColumnID extends string>(
  current: ConfigurableTableSort<ColumnID>,
  column: ColumnID,
  initialDirection: "asc" | "desc" = "asc"
): ConfigurableTableSort<ColumnID> {
  if (current.column !== column) return { column, direction: initialDirection };
  return { column, direction: current.direction === "asc" ? "desc" : "asc" };
}

export function toggleTableColumnVisibility<ColumnID extends string>(
  preferences: ConfigurableStatsTablePreferences<ColumnID>,
  column: ColumnID
): ConfigurableStatsTablePreferences<ColumnID> {
  if (preferences.visible.includes(column) && preferences.visible.length === 1) return copyTablePreferences(preferences);
  const visible = preferences.visible.includes(column)
    ? preferences.visible.filter((id) => id !== column)
    : preferences.order.filter((id) => id === column || preferences.visible.includes(id));
  return { ...preferences, visible };
}

function validColumnIDs<ColumnID extends string>(value: unknown, known: ReadonlySet<ColumnID>): ColumnID[] {
  if (!Array.isArray(value)) return [];
  const seen = new Set<string>();
  return value.flatMap((id) => typeof id === "string" && known.has(id as ColumnID) && !seen.has(id)
    ? (seen.add(id), [id as ColumnID])
    : []);
}

function copyTablePreferences<ColumnID extends string>(preferences: ConfigurableStatsTablePreferences<ColumnID>): ConfigurableStatsTablePreferences<ColumnID> {
  return { order: [...preferences.order], visible: [...preferences.visible], widths: { ...preferences.widths } };
}
