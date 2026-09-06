import { ChevronLeft, ChevronRight, Columns3, GripVertical } from "lucide-react";
import { Fragment, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import {
  clampTableColumnWidth,
  cycleConfigurableTableSort,
  loadTablePreferences,
  MAX_TABLE_COLUMN_WIDTH,
  MIN_TABLE_COLUMN_WIDTH,
  normalizeTablePreferences,
  reorderTableColumns,
  saveTablePreferences,
  tablePreferencesFromDefinitions,
  toggleTableColumnVisibility,
  type ConfigurableStatsTablePreferences,
  type ConfigurableTableSort,
  type StorageLike,
  type TableColumnPreferenceDefinition
} from "./configurableStatsTable";

export type ConfigurableStatsTableColumnDefinition<Row, ColumnID extends string> = TableColumnPreferenceDefinition<ColumnID> & {
  label: string;
  cell: (row: Row) => ReactNode;
  sortable?: boolean;
  initialSortDirection?: "asc" | "desc";
};

type TablePagination = {
  label: string;
  pageLabel: string;
  previousLabel: string;
  nextLabel: string;
  previousDisabled: boolean;
  nextDisabled: boolean;
  onPrevious(): void;
  onNext(): void;
};

export type ConfigurableStatsTableProps<Row, ColumnID extends string> = {
  definitions: readonly ConfigurableStatsTableColumnDefinition<Row, ColumnID>[];
  rows: readonly Row[];
  rowKey: (row: Row) => string;
  settingsKey: string;
  sort: ConfigurableTableSort<ColumnID>;
  onSortChange: (sort: ConfigurableTableSort<ColumnID>) => void;
  tableLabel: string;
  columnMenuLabel: string;
  emptyState: ReactNode;
  pagination?: TablePagination;
  renderAfterRow?: (row: Row) => ReactNode;
  rowClassName?: (row: Row) => string | undefined;
  storage?: StorageLike;
};

type ConfigurableStatsTableInteractionControllerOptions<Row, ColumnID extends string> = {
  defaults: ConfigurableStatsTablePreferences<ColumnID>;
  preferences: ConfigurableStatsTablePreferences<ColumnID>;
  sort: ConfigurableTableSort<ColumnID>;
  settingsKey: string;
  storage?: StorageLike;
  onPreferencesChange(preferences: ConfigurableStatsTablePreferences<ColumnID>): void;
  onSortChange(sort: ConfigurableTableSort<ColumnID>): void;
  renderAfterRow?: (row: Row) => ReactNode;
};

export function createConfigurableStatsTableInteractionController<Row, ColumnID extends string>(
  options: ConfigurableStatsTableInteractionControllerOptions<Row, ColumnID>
) {
  let preferences = normalizeTablePreferences(options.preferences, options.defaults);
  let sort = options.sort;
  const updatePreferences = (next: ConfigurableStatsTablePreferences<ColumnID>, persist: boolean) => {
    preferences = normalizeTablePreferences(next, options.defaults);
    options.onPreferencesChange(preferences);
    if (persist) saveTablePreferences(options.storage, options.settingsKey, preferences);
  };

  return {
    commitResize() {
      saveTablePreferences(options.storage, options.settingsKey, preferences);
    },
    getPreferences() {
      return { order: [...preferences.order], visible: [...preferences.visible], widths: { ...preferences.widths } };
    },
    renderAfterRow(row: Row) {
      return options.renderAfterRow?.(row);
    },
    reorder(source: ColumnID, target: ColumnID) {
      if (source === target) return;
      updatePreferences({ ...preferences, order: reorderTableColumns(preferences.order, source, target) }, true);
    },
    resize(column: ColumnID, width: number) {
      updatePreferences({ ...preferences, widths: { ...preferences.widths, [column]: clampTableColumnWidth(width) } }, false);
    },
    sort(column: ColumnID, initialDirection?: "asc" | "desc") {
      sort = cycleConfigurableTableSort(sort, column, initialDirection);
      options.onSortChange(sort);
    },
    toggleVisibility(column: ColumnID) {
      updatePreferences(toggleTableColumnVisibility(preferences, column), true);
    }
  };
}

type PointerResizeTarget = {
  addEventListener(type: string, listener: EventListener): void;
  removeEventListener(type: string, listener: EventListener): void;
};

type ColumnMenuDismissalTarget = {
  addEventListener(type: string, listener: EventListener): void;
  removeEventListener(type: string, listener: EventListener): void;
};

export function createColumnMenuDismissal(
  target: ColumnMenuDismissalTarget,
  root: { contains(target: Node | null): boolean },
  dismiss: () => void
): () => void {
  const onPointerDown: EventListener = (event) => {
    if (!root.contains(event.target as Node | null)) dismiss();
  };
  const onKeyDown: EventListener = (event) => {
    if ((event as KeyboardEvent).key === "Escape") dismiss();
  };
  target.addEventListener("pointerdown", onPointerDown);
  target.addEventListener("keydown", onKeyDown);
  return () => {
    target.removeEventListener("pointerdown", onPointerDown);
    target.removeEventListener("keydown", onKeyDown);
  };
}

export function createPointerResizeSession(
  target: PointerResizeTarget,
  onMove: (event: PointerEvent) => void,
  onCommit: () => void
): () => void {
  let active = true;
  const move: EventListener = (event) => { if (active) onMove(event as PointerEvent); };
  const cleanup = () => {
    if (!active) return;
    active = false;
    target.removeEventListener("pointermove", move);
    target.removeEventListener("pointerup", end);
  };
  const end: EventListener = () => {
    if (!active) return;
    onCommit();
    cleanup();
  };
  target.addEventListener("pointermove", move);
  target.addEventListener("pointerup", end);
  return cleanup;
}

export function ConfigurableStatsTable<Row, ColumnID extends string>(props: ConfigurableStatsTableProps<Row, ColumnID>) {
  const defaults = useMemo(() => tablePreferencesFromDefinitions(props.definitions), [props.definitions]);
  const storage = useMemo(() => props.storage ?? browserStorage(), [props.storage]);
  const [preferences, setPreferences] = useState<ConfigurableStatsTablePreferences<ColumnID>>(() => loadTablePreferences(storage, props.settingsKey, defaults));
  const [menuOpen, setMenuOpen] = useState(false);
  const root = useRef<HTMLElement | null>(null);
  const draggedColumn = useRef<ColumnID | null>(null);
  const activeResizeCleanup = useRef<(() => void) | null>(null);

  useEffect(() => {
    setPreferences(loadTablePreferences(storage, props.settingsKey, defaults));
  }, [defaults, props.settingsKey, storage]);

  useEffect(() => () => {
    activeResizeCleanup.current?.();
    activeResizeCleanup.current = null;
  }, []);

  useEffect(() => {
    if (!menuOpen || typeof document === "undefined" || root.current === null) return;
    return createColumnMenuDismissal(document, root.current, () => setMenuOpen(false));
  }, [menuOpen]);

  const columns = preferences.order.filter((id) => preferences.visible.includes(id));
  const definitions = new Map(props.definitions.map((definition) => [definition.id, definition]));
  const gridTemplateColumns = columns.map((column) => `${preferences.widths[column]}px`).join(" ");
  const gridPixelWidth = `${columns.reduce((width, column) => width + preferences.widths[column], 0)}px`;
  const gridStyle = { gridTemplateColumns };
  const interactions = createConfigurableStatsTableInteractionController({
    defaults,
    preferences,
    sort: props.sort,
    settingsKey: props.settingsKey,
    storage,
    onPreferencesChange: setPreferences,
    onSortChange: props.onSortChange,
    renderAfterRow: props.renderAfterRow
  });
  const beginResize = (event: React.PointerEvent<HTMLSpanElement>, column: ColumnID) => {
    event.preventDefault();
    activeResizeCleanup.current?.();
    const startX = event.clientX;
    const startWidth = preferences.widths[column];
    activeResizeCleanup.current = createPointerResizeSession(window, (moveEvent) => {
      interactions.resize(column, startWidth + moveEvent.clientX - startX);
    }, () => {
      interactions.commitResize();
      activeResizeCleanup.current = null;
    });
  };

  return <section className="configurableStatsTable" aria-label={props.tableLabel} ref={root}>
    <div className="configurableStatsTable__toolbar" role="toolbar" aria-label={props.tableLabel}>
      <button aria-expanded={menuOpen} aria-label={props.columnMenuLabel} className="configurableStatsTable__columnsButton" onClick={() => setMenuOpen((current) => !current)} title={props.columnMenuLabel} type="button"><Columns3 size={15} /></button>
      <div className="configurableStatsTable__columnMenu" hidden={!menuOpen} role="menu">
        {preferences.order.map((column) => {
          const definition = definitions.get(column);
          if (!definition) return null;
          const checked = preferences.visible.includes(column);
          return <label aria-checked={checked} key={column} role="menuitemcheckbox"><input checked={checked} disabled={checked && preferences.visible.length === 1} onChange={() => interactions.toggleVisibility(column)} type="checkbox" />{definition.label}</label>;
        })}
      </div>
    </div>
    <div className="configurableStatsTable__frame">
      <div className="configurableStatsTable__body" role="grid" aria-label={props.tableLabel}>
        <div className="configurableStatsTable__grid configurableStatsTable__header" role="row" style={gridStyle}>
          {columns.map((column, index) => {
            const definition = definitions.get(column);
            if (!definition) return null;
            const sortable = definition.sortable !== false;
            const ariaSort = props.sort.column === column ? props.sort.direction === "asc" ? "ascending" : "descending" : "none";
            return <div className="configurableStatsTable__headerCell" key={column} onDragOver={(event) => event.preventDefault()} onDrop={(event) => {
              event.preventDefault();
              const source = draggedColumn.current ?? event.dataTransfer?.getData("text/plain") as ColumnID;
              if (source) interactions.reorder(source, column);
              draggedColumn.current = null;
            }} role="columnheader" aria-sort={sortable ? ariaSort : undefined}>
              <button aria-label={`Reorder ${definition.label}`} className="configurableStatsTable__dragHandle" draggable onDragEnd={() => { draggedColumn.current = null; }} onDragStart={(event) => {
                draggedColumn.current = column;
                event.dataTransfer?.setData("text/plain", column);
                if (event.dataTransfer) event.dataTransfer.effectAllowed = "move";
              }} onKeyDown={(event) => {
                if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
                event.preventDefault();
                const target = columns[index + (event.key === "ArrowLeft" ? -1 : 1)];
                if (target) interactions.reorder(column, target);
              }} title={`Reorder ${definition.label}`} type="button"><GripVertical size={14} /></button>
              {sortable ? <button className="configurableStatsTable__sortButton" onClick={() => interactions.sort(column, definition.initialSortDirection)} type="button">{definition.label}</button> : <span>{definition.label}</span>}
              <span aria-label={`Resize ${definition.label}`} aria-orientation="vertical" aria-valuemax={MAX_TABLE_COLUMN_WIDTH} aria-valuemin={MIN_TABLE_COLUMN_WIDTH} aria-valuenow={preferences.widths[column]} className="configurableStatsTable__resizeHandle" onKeyDown={(event) => {
                if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
                event.preventDefault();
                interactions.resize(column, preferences.widths[column] + (event.key === "ArrowLeft" ? -16 : 16));
                interactions.commitResize();
              }} onPointerDown={(event) => beginResize(event, column)} role="separator" tabIndex={0} title={`Resize ${definition.label}`} />
            </div>;
          })}
        </div>
        {props.rows.map((row) => {
          const afterRow = interactions.renderAfterRow(row);
          return <Fragment key={props.rowKey(row)}>
            <div className={["configurableStatsTable__grid", "configurableStatsTable__row", props.rowClassName?.(row)].filter(Boolean).join(" ")} role="row" style={gridStyle}>{columns.map((column) => <div key={column} role="gridcell">{definitions.get(column)?.cell(row)}</div>)}</div>
            {afterRow != null && <div className="configurableStatsTable__expandedRow" role="row" style={{ width: gridPixelWidth }}><div role="gridcell" aria-colspan={columns.length}>{afterRow}</div></div>}
          </Fragment>;
        })}
        {!props.rows.length && <div className="configurableStatsTable__empty" role="status">{props.emptyState}</div>}
      </div>
      {props.pagination && <footer className="configurableStatsTable__pagination">
        <span>{props.pagination.pageLabel}</span>
        <nav aria-label={props.pagination.label}><button aria-label={props.pagination.previousLabel} disabled={props.pagination.previousDisabled} onClick={props.pagination.onPrevious} type="button"><ChevronLeft size={16} /></button><button aria-label={props.pagination.nextLabel} disabled={props.pagination.nextDisabled} onClick={props.pagination.onNext} type="button"><ChevronRight size={16} /></button></nav>
      </footer>}
    </div>
  </section>;
}

function browserStorage(): StorageLike | undefined {
  try { return typeof window === "undefined" ? undefined : window.localStorage; } catch { return undefined; }
}
