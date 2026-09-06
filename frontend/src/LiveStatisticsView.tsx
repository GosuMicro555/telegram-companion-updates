import { Download, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type MouseEvent as ReactMouseEvent } from "react";
import { BulkImportCanonicalKeywords, DeleteLiveTrigger, ExportLiveDeliveryStatistics, GetLiveDeliveryStatistics } from "../wailsjs/go/wails/Bindings";
import * as models from "../wailsjs/go/models";
import { ConfigurableStatsTable } from "./ConfigurableStatsTableComponent";
import { loadTablePreferences, tablePreferencesFromDefinitions, type StorageLike } from "./configurableStatsTable";
import { DateTimeField } from "./DateTimeField";
import { t, type Locale } from "./i18n";
import { isSelectionActionMenuEventTarget, SelectionActionMenu } from "./SelectionActionMenu";
import {
  createLiveStatisticsArrivalTracker,
  emptyLiveDeliveryPage,
  datetimeLocalToRFC3339,
  formatLiveDeliveryDate,
  formatLiveDeliveryStatus,
  formatLiveDeliveryTime,
  formatLiveDeliveryType,
  formatMegabytes,
  liveDeliveryTimeZone,
  livePageSizes,
  normalizeLiveDeliveryPage,
  type LiveDeliveryPage,
  type LiveDeliveryRow
} from "./liveStatistics";

type LiveColumn = "sourceMessage" | "trigger" | "type" | "date" | "time" | "account" | "status";
type SortDirection = "asc" | "desc";

const liveTableSettingsKey = "stats.live.table.v1";
const liveColumnPreferenceDefinitions: ReadonlyArray<{ id: LiveColumn; width: number }> = [
  { id: "sourceMessage", width: 280 },
  { id: "trigger", width: 190 },
  { id: "type", width: 86 },
  { id: "date", width: 104 },
  { id: "time", width: 90 },
  { id: "account", width: 180 },
  { id: "status", width: 130 }
];

type LiveQuery = {
  from: string;
  to: string;
  page: number;
  pageSize: number;
  sort: { column: LiveColumn; direction: SortDirection };
};

type VisibilityDocument = {
  visibilityState: string;
  addEventListener(type: "visibilitychange", listener: EventListener): void;
  removeEventListener(type: "visibilitychange", listener: EventListener): void;
};

const initialQuery: LiveQuery = {
  ...liveStatisticsDateTimeRange(),
  page: 1,
  pageSize: 50,
  sort: { column: "date", direction: "desc" }
};

export function normalizeLiveSelectionPhrase(value: string): string {
  return value.trim().replace(/\s+/gu, " ").toLocaleLowerCase("ru-RU");
}

export function isLiveSelectionPhraseEligible(value: string): boolean {
  const phrase = normalizeLiveSelectionPhrase(value);
  if (!phrase || Array.from(phrase).length > 120) return false;
  return phrase.split(" ").length <= 8;
}

export type LiveSelectionSnapshot = {
  messageId: string;
  position: { x: number; y: number };
  text: string;
};

type LiveSelectionImportResult = {
  added: number;
  skipped: number;
  updated: number;
};

type LiveSelectionImportFeedback = {
  error: string;
  success: string;
};

export function createLiveSelectionImportController(dependencies: {
  importKeywords(
    entries: Array<{ canonical: string; forms: string[] }>,
    keywordClass: "negative"
  ): Promise<LiveSelectionImportResult>;
  onBusy(busy: boolean): void;
  onError(error: string): void;
  onSuccess(snapshot: LiveSelectionSnapshot, feedback: string): void;
}) {
  let inFlight = false;
  let disposed = false;
  return {
    async submit(snapshot: LiveSelectionSnapshot, feedback: LiveSelectionImportFeedback): Promise<boolean> {
      if (inFlight || disposed) return false;
      inFlight = true;
      dependencies.onBusy(true);
      dependencies.onError("");
      try {
        const result = await dependencies.importKeywords([{ canonical: snapshot.text, forms: [] }], "negative");
        if (disposed) return false;
        if (result.added + result.updated === 0) {
          dependencies.onError(feedback.error);
          return false;
        }
        dependencies.onSuccess(snapshot, feedback.success);
        return true;
      } catch (cause) {
        if (!disposed) dependencies.onError(messageOf(cause));
        return false;
      } finally {
        if (!disposed) {
          inFlight = false;
          dependencies.onBusy(false);
        }
      }
    },
    dispose(): void {
      disposed = true;
      inFlight = false;
    }
  };
}

export function createLiveSelectionGestureController(callbacks: {
  onClear(): void;
  onSnapshot(snapshot: LiveSelectionSnapshot): void;
}) {
  let selectionGesture = false;
  return {
    handleMouseUp(selection: Selection | null, pointer: { clientX: number; clientY: number }): boolean {
      const snapshot = resolveLiveStatisticsSelection(selection, pointer);
      selectionGesture = Boolean(snapshot);
      if (!snapshot) {
        callbacks.onClear();
        return false;
      }
      callbacks.onSnapshot(snapshot);
      return true;
    },
    handleClick(
      target: Node | null,
      event: { preventDefault(): void; stopPropagation(): void }
    ): boolean {
      const sourceCell = target ? selectionEndpointElement(target)?.closest("[data-message-id]") : null;
      if (!selectionGesture || !sourceCell) return false;
      selectionGesture = false;
      event.preventDefault();
      event.stopPropagation();
      return true;
    },
    reset(): void {
      selectionGesture = false;
    }
  };
}

export function resolveLiveStatisticsSelection(
  selection: Selection | null,
  pointer: { clientX: number; clientY: number }
): LiveSelectionSnapshot | undefined {
  if (!selection || selection.rangeCount !== 1 || selection.isCollapsed) return undefined;
  const range = selection.getRangeAt(0);
  if (range.collapsed) return undefined;

  const startCell = selectionEndpointElement(range.startContainer)?.closest("[data-message-id]");
  const endCell = selectionEndpointElement(range.endContainer)?.closest("[data-message-id]");
  if (!startCell || startCell !== endCell) return undefined;

  const messageId = startCell.getAttribute("data-message-id") ?? "";
  const text = normalizeLiveSelectionPhrase(selection.toString());
  if (!messageId || !isLiveSelectionPhraseEligible(text)) return undefined;

  const rectangle = range.getBoundingClientRect();
  const hasPointerPosition = pointer.clientX !== 0 || pointer.clientY !== 0;
  const hasRangeRectangle = rectangle.width > 0 || rectangle.height > 0;
  return {
    messageId,
    position: hasPointerPosition
      ? { x: pointer.clientX + 8, y: pointer.clientY + 8 }
      : hasRangeRectangle
        ? { x: rectangle.right + 8, y: rectangle.bottom + 8 }
        : { x: 8, y: 8 },
    text
  };
}

function clearOwnedLiveStatisticsSelection(
  selection: Selection | null,
  snapshot: LiveSelectionSnapshot
): void {
  const current = resolveLiveStatisticsSelection(selection, { clientX: 0, clientY: 0 });
  if (current?.messageId === snapshot.messageId && current.text === snapshot.text) {
    selection?.removeAllRanges();
  }
}

function selectionEndpointElement(node: Node): Element | null {
  return node.nodeType === 1 ? node as Element : node.parentElement;
}

export function liveStatisticsDateTimeRange(now = new Date()): { from: string; to: string } {
  const day = 24 * 60 * 60 * 1000;
  return {
    from: formatLocalDateTime(new Date(now.getTime() - day)),
    to: formatLocalDateTime(new Date(now.getTime() + day))
  };
}

function formatLocalDateTime(value: Date): string {
  const pad = (part: number) => String(part).padStart(2, "0");
  return `${value.getFullYear()}-${pad(value.getMonth() + 1)}-${pad(value.getDate())}T${pad(value.getHours())}:${pad(value.getMinutes())}`;
}

const copy = {
  en: {
    table: "Live delivery statistics", columns: "Choose columns", sourceMessage: "Source message", trigger: "Trigger", type: "Type",
    date: "Date", time: "Time", account: "Telegram account", status: "Status", from: "Select start date", to: "Select end date", total: "Total messages",
    database: "Live statistics data", refreshed: "Last refresh", pageSize: "Rows per page", previous: "Previous page", next: "Next page",
    page: "Page", of: "of", empty: "No completed live deliveries exist for this range.", deleted: "Deleted", deleteTrigger: "Delete trigger",
    exportFile: "Export to file", exporting: "Exporting..."
  },
  ru: {
    table: "Live-статистика доставок", columns: "Выбрать столбцы", sourceMessage: "Исходное сообщение", trigger: "Триггер", type: "Тип",
    date: "Дата", time: "Время", account: "Telegram аккаунт", status: "Статус", from: "Выбор даты начала", to: "Выбор даты окончания", total: "Всего сообщений",
    database: "Данные Live-статистики", refreshed: "Последнее обновление", pageSize: "Строк на странице", previous: "Предыдущая страница", next: "Следующая страница",
    page: "Страница", of: "из", empty: "В выбранном диапазоне нет завершенных live-доставок.", deleted: "Удалён", deleteTrigger: "Удалить триггер",
    exportFile: "Выгрузка в файл", exporting: "Выгрузка..."
  }
} as const;

export function createLiveStatisticsRequestController<Query, Page>(dependencies: {
  fetchPage(query: Query): Promise<Page>;
  deleteTrigger(id: string): Promise<void>;
  onTriggerDeleted?: () => Promise<void> | void;
  runTriggerDeletion?: (operation: () => Promise<void>) => Promise<void>;
  onPage(page: Page): void;
  onError(error: string): void;
  onLoading(loading: boolean): void;
}) {
  let generation = 0;
  let disposed = false;
  let deleteGeneration: number | undefined;
  let ordinaryRequest: { generation: number; promise: Promise<void> } | undefined;
  const begin = () => {
    const requestGeneration = ++generation;
    if (!disposed) dependencies.onLoading(true);
    return requestGeneration;
  };
  const owns = (requestGeneration: number) => !disposed && requestGeneration === generation;
  const finish = async (requestGeneration: number, request: () => Promise<Page>) => {
    try {
      const page = await request();
      if (owns(requestGeneration)) {
        dependencies.onPage(page);
        dependencies.onError("");
      }
    } catch (cause) {
      if (owns(requestGeneration)) dependencies.onError(messageOf(cause));
    } finally {
      if (owns(requestGeneration)) dependencies.onLoading(false);
    }
  };

  return {
    refresh(query: Query): Promise<void> {
      if (disposed) return Promise.resolve();
      if (deleteGeneration !== undefined && owns(deleteGeneration)) return Promise.resolve();
      if (ordinaryRequest !== undefined && owns(ordinaryRequest.generation)) return ordinaryRequest.promise;
      const requestGeneration = begin();
      const promise = finish(requestGeneration, () => dependencies.fetchPage(query)).finally(() => {
        if (ordinaryRequest?.generation === requestGeneration) ordinaryRequest = undefined;
      });
      ordinaryRequest = { generation: requestGeneration, promise };
      return promise;
    },
    async deleteAndRefresh(id: string, query: Query): Promise<void> {
      if (disposed) return;
      ordinaryRequest = undefined;
      const requestGeneration = begin();
      deleteGeneration = requestGeneration;
      try {
        if (dependencies.runTriggerDeletion) {
          await dependencies.runTriggerDeletion(async () => {
            await dependencies.deleteTrigger(id);
            if (dependencies.onTriggerDeleted) await dependencies.onTriggerDeleted();
          });
        } else {
          await dependencies.deleteTrigger(id);
          if (dependencies.onTriggerDeleted) await dependencies.onTriggerDeleted();
        }
        if (!owns(requestGeneration)) return;
        await finish(requestGeneration, () => dependencies.fetchPage(query));
      } catch (cause) {
        if (owns(requestGeneration)) {
          dependencies.onError(messageOf(cause));
          dependencies.onLoading(false);
        }
      } finally {
        if (deleteGeneration === requestGeneration) deleteGeneration = undefined;
      }
    },
    invalidate(): void {
      generation += 1;
      deleteGeneration = undefined;
      ordinaryRequest = undefined;
      if (!disposed) dependencies.onLoading(false);
    },
    dispose(): void {
      disposed = true;
      generation += 1;
      deleteGeneration = undefined;
      ordinaryRequest = undefined;
    }
  };
}

export function createLiveStatisticsPolling(documentTarget: VisibilityDocument, refresh: () => void): () => void {
  let timer: ReturnType<typeof setInterval> | undefined;
  const stop = () => {
    if (timer !== undefined) {
      clearInterval(timer);
      timer = undefined;
    }
  };
  const start = () => {
    if (documentTarget.visibilityState !== "visible" || timer !== undefined) return;
    refresh();
    timer = setInterval(refresh, 5000);
  };
  const onVisibilityChange = () => {
    stop();
    start();
  };

  documentTarget.addEventListener("visibilitychange", onVisibilityChange);
  start();
  return () => {
    stop();
    documentTarget.removeEventListener("visibilitychange", onVisibilityChange);
  };
}

export function setupLiveStatisticsRequests<Query, Page>(documentTarget: VisibilityDocument, query: Query, dependencies: {
  fetchPage(query: Query): Promise<Page>;
  deleteTrigger(id: string): Promise<void>;
  onTriggerDeleted?: () => Promise<void> | void;
  runTriggerDeletion?: (operation: () => Promise<void>) => Promise<void>;
  onPage(page: Page): void;
  onError(error: string): void;
  onLoading(loading: boolean): void;
}) {
  const controller = createLiveStatisticsRequestController(dependencies);
  const stopPolling = createLiveStatisticsPolling(documentTarget, () => { void controller.refresh(query); });
  return {
    controller,
    dispose(): void {
      stopPolling();
      controller.dispose();
    }
  };
}

export function LiveStatisticsView({ locale, onTriggerDeleted, runTriggerDeletion }: {
  locale: Locale;
  onTriggerDeleted?: () => Promise<void> | void;
  runTriggerDeletion?: (operation: () => Promise<void>) => Promise<void>;
}) {
  const [query, setQuery] = useState<LiveQuery>(() => ({ ...initialQuery, ...liveStatisticsDateTimeRange() }));
  const [page, setPage] = useState<LiveDeliveryPage>(emptyLiveDeliveryPage);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [exporting, setExporting] = useState(false);
  const [deletingTriggerID, setDeletingTriggerID] = useState("");
  const [selection, setSelection] = useState<LiveSelectionSnapshot>();
  const [selectionBusy, setSelectionBusy] = useState(false);
  const [selectionError, setSelectionError] = useState("");
  const [selectionStatus, setSelectionStatus] = useState("");
  const [newRowIDs, setNewRowIDs] = useState<ReadonlySet<string>>(() => new Set());
  const requestControllerRef = useRef<ReturnType<typeof createLiveStatisticsRequestController<models.wails.LiveDeliveryQueryDTO, LiveDeliveryPage>> | null>(null);
  const selectionGestureControllerRef = useRef<ReturnType<typeof createLiveSelectionGestureController> | null>(null);
  const selectionImportControllerRef = useRef<ReturnType<typeof createLiveSelectionImportController> | null>(null);
  const arrivalTrackerRef = useRef<ReturnType<typeof createLiveStatisticsArrivalTracker> | null>(null);
  const rowAnimationTimerRef = useRef<number | null>(null);

  if (!arrivalTrackerRef.current) arrivalTrackerRef.current = createLiveStatisticsArrivalTracker();

  if (!selectionGestureControllerRef.current) {
    selectionGestureControllerRef.current = createLiveSelectionGestureController({
      onClear: () => {
        setSelection(undefined);
        setSelectionError("");
        setSelectionStatus("");
      },
      onSnapshot: (snapshot) => {
        setSelection(snapshot);
        setSelectionError("");
        setSelectionStatus("");
      }
    });
  }

  useEffect(() => {
    const controller = createLiveSelectionImportController({
      importKeywords: BulkImportCanonicalKeywords,
      onBusy: (busy) => {
        setSelectionBusy(busy);
        if (busy) setSelectionStatus("");
      },
      onError: setSelectionError,
      onSuccess: (snapshot, feedback) => {
        selectionGestureControllerRef.current?.reset();
        setSelection(undefined);
        setSelectionError("");
        setSelectionStatus(feedback);
        clearOwnedLiveStatisticsSelection(window.getSelection(), snapshot);
      }
    });
    selectionImportControllerRef.current = controller;
    return () => {
      if (selectionImportControllerRef.current === controller) selectionImportControllerRef.current = null;
      controller.dispose();
      selectionGestureControllerRef.current?.reset();
    };
  }, []);

  useEffect(() => () => {
    if (rowAnimationTimerRef.current !== null) window.clearTimeout(rowAnimationTimerRef.current);
  }, []);

  useEffect(() => {
    const setup = setupLiveStatisticsRequests(document, liveDeliveryQueryDTO(query), {
      fetchPage: (input: models.wails.LiveDeliveryQueryDTO) => GetLiveDeliveryStatistics(input).then(normalizeLiveDeliveryPage),
      deleteTrigger: DeleteLiveTrigger,
      onTriggerDeleted,
      runTriggerDeletion,
      onPage: (nextPage) => {
        const arrivals = arrivalTrackerRef.current?.observe(nextPage) ?? [];
        setPage(nextPage);
        if (rowAnimationTimerRef.current !== null) window.clearTimeout(rowAnimationTimerRef.current);
        setNewRowIDs(new Set(arrivals));
        if (arrivals.length > 0) {
          rowAnimationTimerRef.current = window.setTimeout(() => {
            rowAnimationTimerRef.current = null;
            setNewRowIDs(new Set());
          }, 900);
        } else {
          rowAnimationTimerRef.current = null;
        }
      },
      onError: setError,
      onLoading: setLoading
    });
    requestControllerRef.current = setup.controller;
    return () => {
      if (requestControllerRef.current === setup.controller) requestControllerRef.current = null;
      setup.dispose();
    };
  }, [onTriggerDeleted, query, runTriggerDeletion]);

  const changeQuery = (next: Partial<LiveQuery>) => {
    requestControllerRef.current?.invalidate();
    arrivalTrackerRef.current?.reset();
    if (rowAnimationTimerRef.current !== null) window.clearTimeout(rowAnimationTimerRef.current);
    rowAnimationTimerRef.current = null;
    setNewRowIDs(new Set());
    setQuery((current) => ({ ...current, ...next }));
  };
  const deleteTrigger = async (id: string) => {
    const requestController = requestControllerRef.current;
    if (!requestController) return;
    setDeletingTriggerID(id);
    try {
      await requestController.deleteAndRefresh(id, liveDeliveryQueryDTO(query));
    } finally {
      setDeletingTriggerID("");
    }
  };
  const exportStatistics = async () => {
    setExporting(true);
    setError("");
    try {
      const storage = typeof window === "undefined" ? undefined : window.localStorage;
      await ExportLiveDeliveryStatistics(new models.wails.LiveDeliveryExportRequestDTO({
        from: datetimeLocalToRFC3339(query.from),
        to: datetimeLocalToRFC3339(query.to),
        sortBy: serverSortColumn(query.sort.column),
        sortDirection: query.sort.direction === "asc" ? "ascending" : "descending",
        columns: liveStatisticsExportColumns(storage),
        locale,
        timeZone: liveDeliveryTimeZone(locale)
      }));
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setExporting(false);
    }
  };
  const handleSelectionMouseUp = (event: ReactMouseEvent<HTMLElement>) => {
    if (selectionBusy || isSelectionActionMenuEventTarget(event.target as Node)) return;
    selectionGestureControllerRef.current?.handleMouseUp(window.getSelection(), {
      clientX: event.clientX,
      clientY: event.clientY
    });
  };
  const handleSelectionClickCapture = (event: ReactMouseEvent<HTMLElement>) => {
    selectionGestureControllerRef.current?.handleClick(event.target as Node, event);
  };
  const closeSelection = () => {
    if (selectionBusy) return;
    selectionGestureControllerRef.current?.reset();
    setSelection(undefined);
    setSelectionError("");
    setSelectionStatus("");
    if (selection) clearOwnedLiveStatisticsSelection(window.getSelection(), selection);
  };

  return <LiveStatisticsViewContent
    deletingTriggerID={deletingTriggerID}
    error={error}
    loading={loading}
    locale={locale}
    onDeleteTrigger={deleteTrigger}
    onExport={exportStatistics}
    onPageChange={(nextPage) => changeQuery({ page: nextPage })}
    onPageSizeChange={(pageSize) => changeQuery({ page: 1, pageSize })}
    onRangeChange={(range) => changeQuery({ ...range, page: 1 })}
    onSelectionAction={() => {
      if (selection) {
        void selectionImportControllerRef.current?.submit(selection, {
          error: t(locale, "liveSelectionImportRejected"),
          success: t(locale, "liveSelectionImportSuccess")
        });
      }
    }}
    onSelectionClickCapture={handleSelectionClickCapture}
    onSelectionClose={closeSelection}
    onSelectionMouseUp={handleSelectionMouseUp}
    onSortChange={(sort) => changeQuery({ page: 1, sort })}
    page={page}
    newRowIDs={newRowIDs}
    query={query}
    selection={selection}
    selectionBusy={selectionBusy}
    selectionError={selectionError}
    selectionStatus={selectionStatus}
    exporting={exporting}
  />;
}

export function liveStatisticsExportColumns(storage?: StorageLike): LiveColumn[] {
  const defaults = tablePreferencesFromDefinitions(liveColumnPreferenceDefinitions);
  const preferences = loadTablePreferences(storage, liveTableSettingsKey, defaults);
  return preferences.order.filter((id) => preferences.visible.includes(id));
}

export function LiveStatisticsViewContent(props: {
  locale: Locale;
  page: LiveDeliveryPage;
  newRowIDs?: ReadonlySet<string>;
  onDeleteTrigger(id: string): void;
  onExport?(): void;
  query?: LiveQuery;
  error?: string;
  loading?: boolean;
  deletingTriggerID?: string;
  exporting?: boolean;
  onRangeChange?(range: Pick<LiveQuery, "from" | "to">): void;
  onSortChange?(sort: LiveQuery["sort"]): void;
  onPageChange?(page: number): void;
  onPageSizeChange?(pageSize: number): void;
  onSelectionAction?(): void;
  onSelectionClickCapture?(event: ReactMouseEvent<HTMLElement>): void;
  onSelectionClose?(): void;
  onSelectionMouseUp?(event: ReactMouseEvent<HTMLElement>): void;
  selection?: LiveSelectionSnapshot;
  selectionBusy?: boolean;
  selectionError?: string;
  selectionStatus?: string;
  timeZone?: string;
}) {
  const labels = copy[props.locale];
  const query = props.query ?? initialQuery;
  const pageCount = Math.max(1, Math.ceil(props.page.total / query.pageSize));
  const timeZone = props.timeZone ?? liveDeliveryTimeZone(props.locale);
  const definitions = useMemo(() => liveColumnDefinitions(labels, props.locale, timeZone, props.onDeleteTrigger, props.deletingTriggerID), [labels, props.deletingTriggerID, props.locale, props.onDeleteTrigger, timeZone]);
  const updateRange = (next: Partial<Pick<LiveQuery, "from" | "to">>) => props.onRangeChange?.({ from: next.from ?? query.from, to: next.to ?? query.to });

  return <section
    className="liveStatsWorkspace"
    aria-busy={props.loading}
    aria-label={labels.table}
    data-table-settings-key="stats.live.table.v1"
    onClickCapture={props.onSelectionClickCapture}
    onMouseUp={props.onSelectionMouseUp}
  >
    {props.error && <div className="errorBanner liveStatsError">{props.error}</div>}
    {props.selectionStatus && <div className="liveStatsSelectionFeedback" role="status">{props.selectionStatus}</div>}
    <div className="liveStatsToolbar">
      <div className="liveStatsDateRange" aria-label={labels.table}>
        <DateTimeField label={labels.from} locale={props.locale} onChange={(from) => updateRange({ from })} value={query.from} />
        <DateTimeField label={labels.to} locale={props.locale} onChange={(to) => updateRange({ to })} value={query.to} />
        {props.onExport && <button className="secondaryButton liveStatsExportButton" disabled={props.exporting} onClick={props.onExport} type="button"><Download size={15} />{props.exporting ? labels.exporting : labels.exportFile}</button>}
      </div>
      <dl className="liveStatsMetrics">
        <div><dt>{labels.total}</dt><dd>{props.page.total.toLocaleString()}</dd></div>
        <div><dt>{labels.database}</dt><dd>{formatMegabytes(props.page.databaseBytes)}</dd></div>
        <div><dt>{labels.refreshed}</dt><dd>{formatLiveDeliveryDate(props.page.refreshedAt, props.locale, timeZone)} {formatLiveDeliveryTime(props.page.refreshedAt, props.locale, timeZone)}</dd></div>
      </dl>
      <label className="liveStatsPageSize">{labels.pageSize}<select aria-label={labels.pageSize} onChange={(event) => props.onPageSizeChange?.(Number(event.target.value))} value={query.pageSize}>{livePageSizes.map((pageSize) => <option key={pageSize} value={pageSize}>{pageSize}</option>)}</select></label>
    </div>
    <ConfigurableStatsTable
      columnMenuLabel={labels.columns}
      definitions={definitions}
      emptyState={labels.empty}
      onSortChange={(sort) => props.onSortChange?.(sort)}
      pagination={{
        label: labels.table,
        pageLabel: `${labels.page} ${query.page} ${labels.of} ${pageCount}`,
        previousLabel: labels.previous,
        nextLabel: labels.next,
        previousDisabled: query.page <= 1,
        nextDisabled: query.page >= pageCount,
        onPrevious: () => props.onPageChange?.(Math.max(1, query.page - 1)),
        onNext: () => props.onPageChange?.(Math.min(pageCount, query.page + 1))
      }}
      rowKey={(row) => row.id}
      rowClassName={(row) => props.newRowIDs?.has(row.id) ? "configurableStatsTable__row--new" : undefined}
      rows={props.page.rows}
      settingsKey={liveTableSettingsKey}
      sort={query.sort}
      tableLabel={labels.table}
    />
    {props.selection && props.onSelectionAction && props.onSelectionClose && <SelectionActionMenu
      busy={props.selectionBusy}
      error={props.selectionError}
      locale={props.locale}
      onAction={props.onSelectionAction}
      onClose={props.onSelectionClose}
      position={props.selection.position}
    />}
  </section>;
}

function liveColumnDefinitions(labels: typeof copy.en | typeof copy.ru, locale: Locale, timeZone: string, onDeleteTrigger: (id: string) => void, deletingTriggerID = "") {
  return [
    { id: "sourceMessage" as const, label: labels.sourceMessage, width: 280, initialSortDirection: "asc" as const, cell: (row: LiveDeliveryRow) => <strong data-message-id={row.id} title={row.sourceMessage}>{row.sourceMessage || "-"}</strong> },
    { id: "trigger" as const, label: labels.trigger, width: 190, initialSortDirection: "asc" as const, cell: (row: LiveDeliveryRow) => <span className="liveStatsTrigger">{row.triggerCanonicalID ? <button aria-label={labels.deleteTrigger} disabled={deletingTriggerID === row.triggerCanonicalID} onClick={() => onDeleteTrigger(row.triggerCanonicalID!)} title={labels.deleteTrigger} type="button"><X size={14} /></button> : null}<span title={row.triggerSnapshot}>{row.triggerSnapshot || "-"}</span>{row.triggerCanonicalID ? null : <small>{labels.deleted}</small>}</span> },
    { id: "type" as const, label: labels.type, width: 86, initialSortDirection: "asc" as const, cell: (row: LiveDeliveryRow) => formatLiveDeliveryType(row.deliveryType) },
    { id: "date" as const, label: labels.date, width: 104, initialSortDirection: "desc" as const, cell: (row: LiveDeliveryRow) => <time dateTime={row.triggeredAt}>{formatLiveDeliveryDate(row.triggeredAt, locale, timeZone)}</time> },
    { id: "time" as const, label: labels.time, width: 90, initialSortDirection: "desc" as const, cell: (row: LiveDeliveryRow) => <time dateTime={row.triggeredAt}>{formatLiveDeliveryTime(row.triggeredAt, locale, timeZone)}</time> },
    { id: "account" as const, label: labels.account, width: 180, initialSortDirection: "asc" as const, cell: (row: LiveDeliveryRow) => <span title={row.accountTitleSnapshot}>{row.accountTitleSnapshot || "-"}</span> },
    { id: "status" as const, label: labels.status, width: 130, initialSortDirection: "asc" as const, cell: (row: LiveDeliveryRow) => <span className={`liveStatsStatus${row.errorCode === "private_message_closed" || row.finalStatus === "not_delivered" ? " liveStatsStatus--error" : row.finalStatus === "successful" ? " liveStatsStatus--success" : ""}`} title={row.errorCode}>{formatLiveDeliveryStatus(row.finalStatus, locale, row.errorCode)}</span> }
  ];
}

function liveDeliveryQueryDTO(query: LiveQuery): models.wails.LiveDeliveryQueryDTO {
  return new models.wails.LiveDeliveryQueryDTO({
    from: datetimeLocalToRFC3339(query.from), to: datetimeLocalToRFC3339(query.to), page: query.page, pageSize: query.pageSize,
    sortBy: serverSortColumn(query.sort.column), sortDirection: query.sort.direction === "asc" ? "ascending" : "descending"
  });
}

function serverSortColumn(column: LiveColumn): string {
  return column === "trigger" ? "triggerSnapshot" : column === "type" ? "deliveryType" : column === "date" || column === "time" ? "triggeredAt" : column === "account" ? "accountTitleSnapshot" : column === "status" ? "finalStatus" : "sourceMessage";
}

function messageOf(cause: unknown): string {
  return cause instanceof Error ? cause.message : String(cause);
}
