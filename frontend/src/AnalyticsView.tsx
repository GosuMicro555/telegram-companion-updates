import { useEffect, useMemo, useRef, useState, type Dispatch, type DragEvent as ReactDragEvent, type PointerEvent as ReactPointerEvent, type ReactNode, type SetStateAction } from "react";
import { ArrowDown, ArrowUp, Check, ChevronDown, ChevronLeft, ChevronRight, CircleMinus, CirclePlus, Clock3, Columns3, GripVertical, Link, LoaderCircle, Minus, Play, Plus, Search, Square, Trash2, Undo2 } from "lucide-react";
import { BulkImportCanonicalKeywords } from "../wailsjs/go/wails/Bindings";
import { t, type Locale } from "./i18n";
import { mergeKeywords, type KeywordRow } from "./keywords";
import {
  createCanonicalAnalyticsController,
  filterCanonicalKeywords,
  type CanonicalClass,
  type CanonicalKeyword
} from "./analyticsController";
import {
  createAnalyticsTablePreferenceSaveQueue,
  defaultAnalyticsTablePreferences,
  normalizeAnalyticsTablePreferences,
  reorderAnalyticsColumns,
  resolveDraggedAnalyticsColumn,
  toAnalyticsTablePreference,
  withAnalyticsTableSort,
  type AnalyticsColumnId,
  type AnalyticsTablePreferences,
  type AnalyticsTablePreferencesDependencies,
  type SortDirection
} from "./analyticsTablePreferences";
import {
  handleCanonicalClearAction,
  handleCanonicalClassificationAction,
  handleCanonicalDeleteAction,
  removeCanonicalTriggerKeyword
} from "./analyticsKeywordActions";
import { createColumnMenuDismissal } from "./ConfigurableStatsTableComponent";
import "./analyticsRedesign.css";

type AnalyticsViewProps = {
  locale: Locale;
  topics: string[];
  existingKeywords: KeywordRow[];
  onKeywordsChange: Dispatch<SetStateAction<KeywordRow[]>>;
};

type WailsBindings = Record<string, (...args: unknown[]) => Promise<unknown>>;
type SortState = { column: AnalyticsColumnId; direction: SortDirection };
export type CanonicalImportEntry = { canonical: string; forms: string[] };
export type CanonicalImportResult = { added: number; skipped: number; updated: number };
type AnalyticsRunSnapshot = { newMessages: number; extractedWords: number; processedGroups: number };
export type AnalyticsRuntimeStatus = {
  running: boolean;
  collecting: boolean;
  intervalMinutes: number;
  lastRunAt: string;
  nextRunAt: string;
  allTimeMessages: number;
  allTimeKeywords: number;
  allTimeGroups: number;
  latestCollection: AnalyticsRunSnapshot;
};

const intervals = [1, 5, 10, 30, 60, 120];
const tabValues: CanonicalClass[] = ["neutral", "positive", "negative", "service"];
const emptyRuntime: AnalyticsRuntimeStatus = {
  running: false, collecting: false, intervalMinutes: 10, lastRunAt: "", nextRunAt: "",
  allTimeMessages: 0, allTimeKeywords: 0, allTimeGroups: 0,
  latestCollection: { newMessages: 0, extractedWords: 0, processedGroups: 0 }
};

export function AnalyticsView({ locale, topics: _topics, existingKeywords, onKeywordsChange }: AnalyticsViewProps) {
  const [keywordClass, setKeywordClass] = useState<CanonicalClass>("neutral");
  const [rows, setRows] = useState<CanonicalKeyword[]>([]);
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(new Set());
  const [query, setQuery] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [interval, setIntervalMinutes] = useState(10);
  const [runtime, setRuntime] = useState<AnalyticsRuntimeStatus>(emptyRuntime);
  const [preferences, setPreferences] = useState<Record<CanonicalClass, AnalyticsTablePreferences>>(() => preferenceDefaults());
  const [serviceLanguage, setServiceLanguage] = useState<"ru" | "en">("ru");
  const [serviceWord, setServiceWord] = useState("");
  const [page, setPage] = useState(1);
  const [showColumns, setShowColumns] = useState(false);
  const [showBulkImport, setShowBulkImport] = useState(false);
  const [bulkInput, setBulkInput] = useState("");
  const [bulkFeedback, setBulkFeedback] = useState("");
  const controllerRef = useRef<ReturnType<typeof createCanonicalAnalyticsController> | null>(null);
  const preferenceSaveQueueRef = useRef<ReturnType<typeof createAnalyticsTablePreferenceSaveQueue> | null>(null);
  const columnMenuRef = useRef<HTMLDivElement | null>(null);
  const applyRuntime = (status: AnalyticsRuntimeStatus) => {
    setRuntime(status);
    if (intervals.includes(status.intervalMinutes)) setIntervalMinutes(status.intervalMinutes);
  };

  useEffect(() => {
    const controller = createCanonicalAnalyticsController(canonicalDependencies(), {
      onRows: setRows,
      onError: (message) => setError(message)
    });
    controllerRef.current = controller;
    void controller.load(keywordClass);
    void refreshRuntime(applyRuntime, setError, true);
    const dependencies = preferenceDependencies();
    for (const tab of tabValues) {
      void dependencies.load(tab)
        .then((value) => setPreferences((current) => ({ ...current, [tab]: normalizeAnalyticsTablePreferences(value) })))
        .catch((cause) => setError(messageOf(cause)));
    }
    return () => { controllerRef.current = null; };
  }, []);

  useEffect(() => {
    setExpanded(new Set());
    setPage(1);
    void controllerRef.current?.load(keywordClass);
  }, [keywordClass]);

  useEffect(() => {
    if (!runtime.running) return;
    const timer = window.setInterval(() => void refreshRuntime(applyRuntime, setError, true), 10_000);
    return () => window.clearInterval(timer);
  }, [runtime.running]);

  useEffect(() => {
    if (!showColumns || typeof document === "undefined" || columnMenuRef.current === null) return;
    return createColumnMenuDismissal(document, columnMenuRef.current, () => setShowColumns(false));
  }, [showColumns]);

  const currentPreferences = preferences[keywordClass];
  const keywordBackedRows = useMemo(() => {
    if (keywordClass !== "positive") return rows;
    const existing = normalizedKeywordSet(existingKeywords);
    return rows.map((row) => ({ ...row, triggerActive: existing.has(normalizeKeyword(row.canonical)) }));
  }, [rows, keywordClass, existingKeywords]);
  const visibleRows = useMemo(() => sortCanonicalRows(filterCanonicalKeywords(keywordBackedRows, query), { column: currentPreferences.sortBy, direction: currentPreferences.sortDirection }), [keywordBackedRows, query, currentPreferences]);
  const pageCount = Math.max(1, Math.ceil(visibleRows.length / currentPreferences.pageSize));
  const currentPage = Math.min(page, pageCount);
  const pageStart = (currentPage - 1) * currentPreferences.pageSize;
  const pageRows = visibleRows.slice(pageStart, pageStart + currentPreferences.pageSize);
  const positiveCandidates = useMemo(() => positiveKeywordCandidates(rows, existingKeywords), [rows, existingKeywords]);

  useEffect(() => {
    if (page > pageCount) setPage(pageCount);
  }, [page, pageCount]);
  const action = (operation: () => Promise<unknown>) => {
    setError("");
    void operation().catch((cause) => setError(messageOf(cause)));
  };
  const updatePreferences = (next: AnalyticsTablePreferences, persist = true) => {
    setPreferences((current) => ({ ...current, [keywordClass]: next }));
    if (!persist) return;
    if (!preferenceSaveQueueRef.current) {
      preferenceSaveQueueRef.current = createAnalyticsTablePreferenceSaveQueue(preferenceDependencies(), (cause) => setError(messageOf(cause)));
    }
    void preferenceSaveQueueRef.current.save(keywordClass, toAnalyticsTablePreference(keywordClass, next));
  };
  const updateSort = (sort: SortState) => updatePreferences(withAnalyticsTableSort(currentPreferences, sort));
  const changeInterval = (next: number) => {
    setIntervalMinutes(next);
    if (!runtime.running || busy) return;
    action(async () => {
      await call("StartCanonicalAnalytics", next);
      await refreshRuntime(applyRuntime, setError, true);
    });
  };
  const runNow = async () => {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      const ran = await controllerRef.current?.analyze(keywordClass);
      if (ran) await refreshRuntime(applyRuntime, setError, true);
    } finally {
      setBusy(false);
    }
  };
  const start = async () => {
    if (busy) return;
    setBusy(true);
    try {
      await call("StartCanonicalAnalytics", interval);
      setRuntime((current) => ({ ...current, running: true }));
      await refreshRuntime(applyRuntime, setError, true);
    } catch (cause) { setError(messageOf(cause)); } finally { setBusy(false); }
  };
  const stop = async () => {
    if (busy) return;
    setBusy(true);
    try {
      await call("StopCanonicalAnalytics");
      setRuntime((current) => ({ ...current, running: false }));
    } catch (cause) { setError(messageOf(cause)); } finally { setBusy(false); }
  };
  const addToKeywords = async (row: CanonicalKeyword) => {
    onKeywordsChange((current) => mergeKeywords(current, canonicalTriggerValues([row])));
    await controllerRef.current?.setTrigger(row.id, true, keywordClass);
  };
  const addAllToKeywords = async () => {
    onKeywordsChange((current) => mergeKeywords(current, canonicalTriggerValues(positiveCandidates)));
    for (const row of positiveCandidates) {
      if (!row.triggerActive) await controllerRef.current?.setTrigger(row.id, true, keywordClass);
    }
  };
  const removeFromKeywords = (row: CanonicalKeyword) => {
    onKeywordsChange((current) => removeCanonicalTriggerKeyword(current, row.canonical));
  };
  const classify = (row: CanonicalKeyword, next: CanonicalClass) => handleCanonicalClassificationAction(
    row,
    next,
    onKeywordsChange,
    (id, nextClass) => controllerRef.current?.classify(id, nextClass, keywordClass) ?? Promise.resolve(false)
  );
  const remove = (row: CanonicalKeyword) => handleCanonicalDeleteAction(
    row,
    onKeywordsChange,
    (id) => controllerRef.current?.remove(id, keywordClass) ?? Promise.resolve(false)
  );
  const clear = () => handleCanonicalClearAction(
    keywordBackedRows,
    keywordClass,
    onKeywordsChange,
    () => controllerRef.current?.clear(keywordClass) ?? Promise.resolve(false)
  );
  const addServiceWord = async () => {
    if (!serviceWord.trim()) return;
    await call("AddAnalyticsServiceWord", serviceLanguage, serviceWord.trim());
    setServiceWord("");
    await controllerRef.current?.load("service");
  };
  const preview = useMemo(() => parseBulkCanonicalValues(bulkInput), [bulkInput]);
  const importCanonicalValues = async () => {
    if (!preview.values.length) {
      setError(t(locale, "analyticsBulkNoValid"));
      return;
    }
    setBusy(true);
    setError("");
    try {
      const result = await (BulkImportCanonicalKeywords as unknown as (
        entries: CanonicalImportEntry[],
        keywordClass: CanonicalClass
      ) => Promise<CanonicalImportResult>)(preview.values, keywordClass);
      const outcome = canonicalImportOutcome(result, preview.skipped);
      if (!outcome.accepted) {
        setError(t(locale, "analyticsBulkNoValid"));
        return;
      }
      await controllerRef.current?.load(keywordClass);
      setBulkFeedback(outcome.skipped ? `${t(locale, "analyticsBulkSkipped")} ${outcome.skipped}` : "");
      setBulkInput("");
      setShowBulkImport(false);
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setBusy(false);
    }
  };
  const readBulkFile = async (file: File | undefined) => {
    if (!file) return;
    try {
      setBulkInput(await file.text());
    } catch (cause) {
      setError(messageOf(cause));
    }
  };
  const copy = analyticsCopy(locale);
  const intervalIndex = Math.max(0, intervals.indexOf(interval));

  return <section className="analyticsRedesign" aria-label={t(locale, "analytics")}>
    {error && <div className="analyticsRedesign__error" role="alert">{error}</div>}
    {bulkFeedback && <div className="analyticsRedesign__feedback" role="status">{bulkFeedback}</div>}
    <header className="analyticsRedesign__controlDeck">
      <div className="analyticsRedesign__collectionControl">
        <span className="analyticsRedesign__eyebrow">{copy.collection}</span>
        <div className="analyticsRedesign__runtime" role="group" aria-label={copy.collection}>
          <button aria-label={runtime.running ? t(locale, "analyticsRunning") : t(locale, "analyticsStart")} className="analyticsRedesign__startButton" disabled={busy || runtime.running} onClick={() => action(start)} title={t(locale, "analyticsStart")} type="button"><Play size={14} />{t(locale, "analyticsStart")}</button>
          <button aria-label={t(locale, "analyticsStop")} className="analyticsRedesign__stopButton" disabled={busy || !runtime.running} onClick={() => action(stop)} title={t(locale, "analyticsStop")} type="button"><Square size={13} />{t(locale, "analyticsStop")}</button>
          <button aria-label={t(locale, "analyticsAnalyzeNow")} className="analyticsRedesign__runButton" disabled={busy} onClick={() => action(runNow)} title={t(locale, "analyticsAnalyzeNow")} type="button">{busy || runtime.collecting ? <LoaderCircle className="analyticsRedesign__spin" size={14} /> : <Check size={14} />}{t(locale, "analyticsAnalyzeNow")}</button>
        </div>
      </div>
      <div className="analyticsRedesign__intervalControl">
        <div className="analyticsRedesign__intervalCaption"><span>{t(locale, "analyticsInterval")}</span><strong>{intervalText(locale, interval)}</strong></div>
        <input aria-label={t(locale, "analyticsInterval")} aria-valuetext={intervalText(locale, interval)} className="analyticsRedesign__intervalRange" disabled={busy} list="analytics-interval-stops" max={intervals.length - 1} min="0" onChange={(event) => changeInterval(intervals[Number(event.target.value)] ?? 10)} step="1" type="range" value={intervalIndex} />
        <datalist id="analytics-interval-stops">{intervals.map((value, index) => <option key={value} label={String(value)} value={index} />)}</datalist>
        <div className="analyticsRedesign__intervalTicks" aria-hidden="true">{intervals.map((value) => <span key={value}>{value}</span>)}</div>
      </div>
      <dl className="analyticsRedesign__metricGrid" aria-label={copy.metrics} aria-live="polite">
        <Metric className="analyticsRedesign__metric--next" hint={`${copy.lastRun}: ${formatLastRun(runtime.lastRunAt, locale)}`} label={copy.nextRun} value={formatNextRun(runtime.nextRunAt, locale)} icon={<Clock3 size={15} />} />
        <Metric label={copy.uniqueKeywords} value={runtime.allTimeKeywords.toLocaleString(locale)} hint={copy.allTime} />
        <Metric label={copy.groups} value={runtime.allTimeGroups.toLocaleString(locale)} hint={copy.processedGroups(runtime.latestCollection.processedGroups)} />
        <Metric label={copy.messages} value={runtime.allTimeMessages.toLocaleString(locale)} hint={copy.newMessages(runtime.latestCollection.newMessages)} />
      </dl>
    </header>
    <div className="analyticsRedesign__tabs" role="tablist" aria-label={t(locale, "analyticsKeywordClasses")}>
      {tabs(locale).map((tab) => <button aria-selected={keywordClass === tab.value} className={keywordClass === tab.value ? "active" : ""} key={tab.value} onClick={() => setKeywordClass(tab.value)} role="tab" type="button">{tab.label}</button>)}
    </div>
    <div className="analyticsRedesign__tableControls">
      <div className="analyticsRedesign__search" role="search"><Search size={15} /><input aria-label={t(locale, "analyticsSearch")} onChange={(event) => { setQuery(event.target.value); setPage(1); }} placeholder={t(locale, "analyticsSearchPlaceholder")} value={query} /></div>
      <div className="analyticsRedesign__commands" ref={columnMenuRef} role="toolbar" aria-label={copy.tableTools}>
        <button aria-expanded={showColumns} aria-label={t(locale, "analyticsChooseColumns")} className="analyticsRedesign__columnsButton" onClick={() => setShowColumns((current) => !current)} title={t(locale, "analyticsColumns")} type="button"><Columns3 size={15} /></button>
        {(keywordClass === "positive" || keywordClass === "negative") && <button onClick={() => { setBulkFeedback(""); setShowBulkImport(true); }} type="button"><Plus size={14} />{t(locale, "analyticsBulkAdd")}</button>}
        {keywordClass === "positive" && <button disabled={!positiveCandidates.length || busy} onClick={() => action(addAllToKeywords)} type="button"><CirclePlus size={14} />{t(locale, "analyticsAddAllToKeywords")}</button>}
        {keywordClass === "service" && <><input aria-label={t(locale, "analyticsServiceWord")} onChange={(event) => setServiceWord(event.target.value)} placeholder={t(locale, "analyticsServiceWord")} value={serviceWord} /><select aria-label={t(locale, "analyticsServiceLanguage")} onChange={(event) => setServiceLanguage(event.target.value as "ru" | "en")} value={serviceLanguage}><option value="ru">{t(locale, "analyticsRussian")}</option><option value="en">{t(locale, "analyticsEnglish")}</option></select><IconButton disabled={!serviceWord.trim() || busy} label={t(locale, "analyticsAddServiceWord")} onClick={() => action(addServiceWord)}><Plus size={14} /></IconButton></>}
        <button className="analyticsRedesign__clear" disabled={!rows.length || busy} onClick={() => action(clear)} type="button"><Trash2 size={14} />{t(locale, "analyticsClearAll")}</button>
        {showColumns && <div className="analyticsRedesign__columnMenu">{currentPreferences.order.map((column) => <label key={column}><input checked={currentPreferences.visible.includes(column)} disabled={column === "canonical" && currentPreferences.visible.length === 1} onChange={() => updatePreferences({ ...currentPreferences, visible: currentPreferences.visible.includes(column) ? currentPreferences.visible.filter((item) => item !== column) : [...currentPreferences.visible, column] })} type="checkbox" />{columnLabel(column, locale)}</label>)}</div>}
      </div>
    </div>
    <CanonicalTable
      expanded={expanded}
      keywordClass={keywordClass}
      locale={locale}
      onAddToKeywords={(row) => action(() => addToKeywords(row))}
      onRemoveFromKeywords={removeFromKeywords}
      onAdd={(targetId, form) => action(() => controllerRef.current?.addForm(targetId, form, keywordClass) ?? Promise.resolve())}
      onClassify={(row, next) => action(() => classify(row, next))}
      onDelete={(row) => action(() => remove(row))}
      onDetach={(id, form) => action(() => controllerRef.current?.detachForm(id, form, keywordClass) ?? Promise.resolve())}
      onMove={(targetId, form) => action(() => controllerRef.current?.moveForm(targetId, form, keywordClass) ?? Promise.resolve())}
      onToggle={(id) => setExpanded((current) => toggle(current, id))}
      onTrigger={(id, active) => action(() => controllerRef.current?.setTrigger(id, active, keywordClass) ?? Promise.resolve())}
      preferences={currentPreferences}
      query={query}
      rows={pageRows}
      targets={visibleRows}
      rowOffset={pageStart}
      page={currentPage}
      pageCount={pageCount}
      total={visibleRows.length}
      onPageChange={setPage}
      onPageSizeChange={(pageSize) => { setPage(1); updatePreferences({ ...currentPreferences, pageSize }); }}
      sort={{ column: currentPreferences.sortBy, direction: currentPreferences.sortDirection }}
      onPreferencesChange={updatePreferences}
      onSortChange={updateSort}
    />
    {showBulkImport && <div aria-modal="true" className="analyticsRedesign__modalBackdrop" role="dialog" aria-label={t(locale, "analyticsBulkTitle")}>
      <div className="analyticsRedesign__bulkModal">
        <h2>{t(locale, "analyticsBulkTitle")}</h2>
        <label>{t(locale, "analyticsBulkInput")}<textarea autoFocus onChange={(event) => setBulkInput(event.target.value)} placeholder={t(locale, "analyticsBulkPlaceholder")} value={bulkInput} /></label>
        <label className="analyticsRedesign__filePicker"><input accept=".txt,text/plain" aria-label={t(locale, "analyticsBulkChooseFile")} onChange={(event) => { void readBulkFile(event.currentTarget.files?.[0]); event.currentTarget.value = ""; }} type="file" />{t(locale, "analyticsBulkChooseFile")}</label>
        <div className="analyticsRedesign__bulkPreview" role="status"><span>{t(locale, "analyticsBulkPreview")} {preview.values.length}</span>{preview.skipped > 0 && <span>{t(locale, "analyticsBulkSkipped")} {preview.skipped}</span>}</div>
        <div className="analyticsRedesign__bulkActions"><button disabled={busy || !preview.values.length} onClick={() => void importCanonicalValues()} type="button">{t(locale, "analyticsBulkSave")}</button><button disabled={busy} onClick={() => { setBulkInput(""); setShowBulkImport(false); }} type="button">{t(locale, "analyticsBulkCancel")}</button></div>
      </div>
    </div>}
  </section>;
}

export function canonicalImportOutcome(result: CanonicalImportResult, parserSkipped: number) {
  return {
    accepted: result.added + result.updated > 0,
    skipped: parserSkipped + result.skipped
  };
}

function CanonicalTable(props: {
  keywordClass: CanonicalClass; locale: Locale; rows: CanonicalKeyword[]; targets: CanonicalKeyword[]; rowOffset: number; page: number; pageCount: number; total: number; expanded: ReadonlySet<string>; query: string; preferences: AnalyticsTablePreferences; sort: SortState;
  onToggle(id: string): void; onClassify(row: CanonicalKeyword, keywordClass: CanonicalClass): void; onDelete(row: CanonicalKeyword): void; onTrigger(id: string, active: boolean): void;
  onAddToKeywords(row: CanonicalKeyword): void; onRemoveFromKeywords(row: CanonicalKeyword): void; onDetach(id: string, form: string): void; onMove(targetId: string, form: string): void; onAdd(targetId: string, form: string): void;
  onPreferencesChange(preferences: AnalyticsTablePreferences, persist?: boolean): void; onSortChange(sort: SortState): void; onPageChange(page: number): void; onPageSizeChange(pageSize: number): void;
}) {
  const draggedRef = useRef<AnalyticsColumnId | null>(null);
  const columns = props.preferences.order.filter((column) => props.preferences.visible.includes(column));
  const copy = analyticsCopy(props.locale);
  const cycleSort = (column: AnalyticsColumnId) => {
    const direction: SortDirection = props.sort.column !== column || props.sort.direction === "none" ? "asc" : props.sort.direction === "asc" ? "desc" : "none";
    props.onSortChange({ column, direction });
  };
  const startDrag = (event: ReactDragEvent<HTMLDivElement>, column: AnalyticsColumnId) => {
    draggedRef.current = column;
    event.dataTransfer?.setData("text/plain", column);
    if (event.dataTransfer) event.dataTransfer.effectAllowed = "move";
  };
  const reorder = (event: ReactDragEvent<HTMLDivElement>, target: AnalyticsColumnId) => {
    event.preventDefault();
    const source = resolveDraggedAnalyticsColumn(draggedRef.current, event.dataTransfer?.getData("text/plain") ?? "");
    if (!source || source === target) return;
    props.onPreferencesChange({ ...props.preferences, order: reorderAnalyticsColumns(props.preferences.order, source, target) });
    draggedRef.current = null;
  };
  const endDrag = () => { draggedRef.current = null; };
  const beginResize = (event: ReactPointerEvent<HTMLSpanElement>, column: AnalyticsColumnId) => {
    event.preventDefault();
    const startX = event.clientX;
    const startWidth = props.preferences.widths[column] ?? 120;
    let latest = props.preferences;
    const move = (moveEvent: PointerEvent) => {
      latest = { ...latest, widths: { ...latest.widths, [column]: Math.max(48, Math.min(900, startWidth + moveEvent.clientX - startX)) } };
      props.onPreferencesChange(latest, false);
    };
    const end = () => { props.onPreferencesChange(latest); window.removeEventListener("pointermove", move); window.removeEventListener("pointerup", end); };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", end);
  };
  const firstRow = props.total ? props.rowOffset + 1 : 0;
  const lastRow = props.rowOffset + props.rows.length;
  return <section className="analyticsRedesign__tableFrame" aria-label={copy.keywordTable}>
    <div className="analyticsRedesign__tableWrap" aria-label={copy.keywordTable} role="grid">
      <div className="analyticsRedesign__grid analyticsRedesign__header" role="row" style={gridStyle(columns, props.preferences)}>{columns.map((column) => <div draggable key={column} onDragEnd={endDrag} onDragOver={(event) => { event.preventDefault(); if (event.dataTransfer) event.dataTransfer.dropEffect = "move"; }} onDragStart={(event) => startDrag(event, column)} onDrop={(event) => reorder(event, column)} role="columnheader"><button aria-sort={props.sort.column === column && props.sort.direction !== "none" ? (props.sort.direction === "asc" ? "ascending" : "descending") : "none"} onClick={() => cycleSort(column)} type="button"><GripVertical size={13} />{columnLabel(column, props.locale)}{props.sort.column === column && props.sort.direction !== "none" ? ` ${props.sort.direction === "asc" ? "↑" : "↓"}` : ""}</button><span className="analyticsRedesign__resize" onPointerDown={(event) => beginResize(event, column)} /></div>)}</div>
      {props.rows.map((row, index) => <CanonicalRow columns={columns} expanded={props.expanded.has(row.id)} key={row.id} keywordClass={props.keywordClass} locale={props.locale} onAdd={props.onAdd} onAddToKeywords={props.onAddToKeywords} onClassify={props.onClassify} onDelete={props.onDelete} onDetach={props.onDetach} onMove={props.onMove} onRemoveFromKeywords={props.onRemoveFromKeywords} onToggle={props.onToggle} onTrigger={props.onTrigger} preferences={props.preferences} query={props.query} row={row} rowIndex={props.rowOffset + index} targets={props.targets.filter((target) => target.id !== row.id)} />)}
      {!props.rows.length && <div className="analyticsRedesign__empty" role="status">{t(props.locale, "analyticsEmpty")}</div>}
    </div>
    <footer className="analyticsRedesign__tableFooter">
      <span>{copy.rowsSummary(firstRow, lastRow, props.total)}</span>
      <label>{copy.rowsPerPage}<select aria-label={copy.rowsPerPage} onChange={(event) => props.onPageSizeChange(Number(event.target.value))} value={props.preferences.pageSize}>{pageSizes(props.preferences.pageSize).map((size) => <option key={size} value={size}>{size}</option>)}</select></label>
      <nav aria-label={copy.pagination}><button aria-label={copy.previousPage} disabled={props.page <= 1} onClick={() => props.onPageChange(props.page - 1)} type="button"><ChevronLeft size={15} /></button><span>{props.page} / {props.pageCount}</span><button aria-label={copy.nextPage} disabled={props.page >= props.pageCount} onClick={() => props.onPageChange(props.page + 1)} type="button"><ChevronRight size={15} /></button></nav>
    </footer>
  </section>;
}

function CanonicalRow(props: {
  row: CanonicalKeyword; rowIndex: number; keywordClass: CanonicalClass; locale: Locale; columns: AnalyticsColumnId[]; preferences: AnalyticsTablePreferences; expanded: boolean; query: string; targets: CanonicalKeyword[];
  onToggle(id: string): void; onClassify(row: CanonicalKeyword, keywordClass: CanonicalClass): void; onDelete(row: CanonicalKeyword): void; onTrigger(id: string, active: boolean): void; onAddToKeywords(row: CanonicalKeyword): void; onRemoveFromKeywords(row: CanonicalKeyword): void; onDetach(id: string, form: string): void; onMove(targetId: string, form: string): void; onAdd(targetId: string, form: string): void;
}) {
  const [newForm, setNewForm] = useState("");
  const formsMatch = props.keywordClass !== "service" && Boolean(props.query.trim()) && props.row.forms.some((form) => form.value.toLocaleLowerCase("ru-RU").includes(props.query.trim().toLocaleLowerCase("ru-RU")));
  const expanded = props.keywordClass !== "service" && (props.expanded || formsMatch);
  return <>
    <div aria-rowindex={props.rowIndex + 2} className="analyticsRedesign__grid analyticsRedesign__row" role="row" style={gridStyle(props.columns, props.preferences)}>{props.columns.map((column) => <div key={column} role="gridcell">{cellFor(column, { ...props, expanded })}</div>)}</div>
    {expanded && <div className="analyticsRedesign__forms">{props.row.forms.map((form) => <FormEditor form={form.value} frequency={form.frequency} key={form.value} locale={props.locale} onDetach={props.onDetach} onMove={props.onMove} query={props.query} row={props.row} targets={props.targets} />)}<div className="analyticsRedesign__formAdd"><input aria-label={t(props.locale, "analyticsNewForm")} onChange={(event) => setNewForm(event.target.value)} placeholder={t(props.locale, "analyticsNewForm")} value={newForm} /><IconButton disabled={!newForm.trim()} label={t(props.locale, "analyticsAddForm")} onClick={() => { props.onAdd(props.row.id, newForm.trim()); setNewForm(""); }}><Plus size={14} /></IconButton></div></div>}
  </>;
}

function cellFor(column: AnalyticsColumnId, props: Parameters<typeof CanonicalRow>[0]) {
  switch (column) {
    case "canonical": return <strong><Highlight text={props.row.canonical} query={props.query} /></strong>;
    case "class": return <span className={`analyticsRedesign__class analyticsRedesign__class--${props.row.class}`}>{classLabel(props.locale, props.row.class)}</span>;
    case "language": return props.row.language.toUpperCase();
    case "frequency": return props.row.frequency.toLocaleString(props.locale);
    case "messages": return props.row.messageCount.toLocaleString(props.locale);
    case "trend": return <TrendCell delta={props.row.frequencyDelta} locale={props.locale} />;
    case "lastSeen": return formatLastRun(props.row.lastSeen, props.locale);
    case "forms":
      if (props.keywordClass === "service") return "—";
      return <button className="analyticsRedesign__formsButton" onClick={() => props.onToggle(props.row.id)} type="button">{props.expanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}{props.row.forms.length}</button>;
    case "actions": return <div className="analyticsRedesign__actions">{actionsFor(props)}</div>;
  }
}

function actionsFor(props: Pick<Parameters<typeof CanonicalRow>[0], "row" | "keywordClass" | "locale" | "onClassify" | "onDelete" | "onTrigger" | "onAddToKeywords" | "onRemoveFromKeywords">) {
  if (props.keywordClass === "service") return <IconButton danger label={t(props.locale, "analyticsDelete")} onClick={() => props.onDelete(props.row)}><Trash2 size={14} /></IconButton>;
  if (props.keywordClass === "positive") return <><IconButton label={props.row.triggerActive ? t(props.locale, "analyticsRemoveFromKeywords") : t(props.locale, "analyticsAddToKeywords")} onClick={() => handlePositiveTriggerAction(props.row, props.onAddToKeywords, props.onRemoveFromKeywords, props.onTrigger)}>{props.row.triggerActive ? <CircleMinus size={14} /> : <CirclePlus size={14} />}</IconButton><IconButton label={t(props.locale, "analyticsMoveToNegative")} onClick={() => props.onClassify(props.row, "negative")}><CircleMinus size={14} /></IconButton><IconButton label={t(props.locale, "analyticsReturnToAll")} onClick={() => props.onClassify(props.row, "neutral")}><Undo2 size={14} /></IconButton><IconButton danger label={t(props.locale, "analyticsDelete")} onClick={() => props.onDelete(props.row)}><Trash2 size={14} /></IconButton></>;
  if (props.keywordClass === "negative") return <><IconButton label={t(props.locale, "analyticsMoveToPositive")} onClick={() => props.onClassify(props.row, "positive")}><CirclePlus size={14} /></IconButton><IconButton label={t(props.locale, "analyticsReturnToAll")} onClick={() => props.onClassify(props.row, "neutral")}><Undo2 size={14} /></IconButton><IconButton danger label={t(props.locale, "analyticsDelete")} onClick={() => props.onDelete(props.row)}><Trash2 size={14} /></IconButton></>;
  return <><IconButton label={t(props.locale, "analyticsMoveToPositive")} onClick={() => props.onClassify(props.row, "positive")}><CirclePlus size={14} /></IconButton><IconButton label={t(props.locale, "analyticsMoveToNegative")} onClick={() => props.onClassify(props.row, "negative")}><CircleMinus size={14} /></IconButton><IconButton danger label={t(props.locale, "analyticsDelete")} onClick={() => props.onDelete(props.row)}><Trash2 size={14} /></IconButton></>;
}

export function canonicalTriggerValues(rows: CanonicalKeyword[]): string[] {
  return rows.map((row) => row.canonical);
}

export function positiveKeywordCandidates(rows: CanonicalKeyword[], existingKeywords: KeywordRow[]): CanonicalKeyword[] {
  const existing = normalizedKeywordSet(existingKeywords);
  return rows.filter((row) => !existing.has(normalizeKeyword(row.canonical)));
}

function normalizedKeywordSet(rows: KeywordRow[]): Set<string> {
  return new Set(rows.map((row) => normalizeKeyword(row.keyword)).filter(Boolean));
}

function normalizeKeyword(value: string): string {
  return value.normalize("NFC").trim().toLocaleLowerCase("ru-RU");
}

export { removeCanonicalTriggerKeyword } from "./analyticsKeywordActions";

export function handlePositiveTriggerAction(
  row: CanonicalKeyword,
  addToKeywords: (row: CanonicalKeyword) => void,
  removeFromKeywords: (row: CanonicalKeyword) => void,
  setTrigger: (id: string, active: boolean) => void
): void {
  if (row.triggerActive) {
    removeFromKeywords(row);
    setTrigger(row.id, false);
    return;
  }
  addToKeywords(row);
}

function FormEditor({ form, frequency, locale, row, targets, query, onDetach, onMove }: { form: string; frequency: number; locale: Locale; row: CanonicalKeyword; targets: CanonicalKeyword[]; query: string; onDetach(id: string, form: string): void; onMove(targetId: string, form: string): void }) {
  const [target, setTarget] = useState("");
  return <div className="analyticsRedesign__form"><span><Highlight text={form} query={query} /></span><small>{frequency.toLocaleString(locale)}</small><select aria-label={`${t(locale, "analyticsCanonicalFor")} ${form}`} onChange={(event) => setTarget(event.target.value)} value={target}><option value="">{t(locale, "analyticsMoveToCanonical")}</option>{targets.map((item) => <option key={item.id} value={item.id}>{item.canonical}</option>)}</select><IconButton disabled={!target} label={t(locale, "analyticsMoveForm")} onClick={() => onMove(target, form)}><Link size={13} /></IconButton><IconButton label={t(locale, "analyticsDetachForm")} onClick={() => onDetach(row.id, form)}><Undo2 size={13} /></IconButton></div>;
}

function IconButton({ children, danger = false, disabled = false, label, onClick }: { children: ReactNode; danger?: boolean; disabled?: boolean; label: string; onClick(): void }) {
  return <button aria-label={label} className={danger ? "danger" : ""} disabled={disabled} onClick={onClick} title={label} type="button">{children}</button>;
}

function Highlight({ text, query }: { text: string; query: string }) {
  const needle = query.trim();
  if (!needle) return text;
  const parts = text.split(new RegExp(`(${escapeRegExp(needle)})`, "ig"));
  return <>{parts.map((part, index) => part.toLocaleLowerCase("ru-RU") === needle.toLocaleLowerCase("ru-RU") ? <mark key={index}>{part}</mark> : part)}</>;
}

function sortCanonicalRows(rows: CanonicalKeyword[], sort: SortState): CanonicalKeyword[] {
  if (sort.direction === "none") return rows;
  const multiplier = sort.direction === "asc" ? 1 : -1;
  return [...rows].sort((left, right) => compareColumn(left, right, sort.column) * multiplier);
}

function compareColumn(left: CanonicalKeyword, right: CanonicalKeyword, column: AnalyticsColumnId): number {
  const leftValue = sortableColumnValue(left, column);
  const rightValue = sortableColumnValue(right, column);
  return typeof leftValue === "string" && typeof rightValue === "string" ? leftValue.localeCompare(rightValue, "ru") : Number(leftValue) - Number(rightValue);
}

function gridStyle(columns: AnalyticsColumnId[], preferences: AnalyticsTablePreferences) {
  return { gridTemplateColumns: columns.map((column) => column === "canonical" ? `minmax(${preferences.widths[column] ?? 260}px, 1fr)` : `${preferences.widths[column] ?? 120}px`).join(" ") };
}

function columnLabel(column: AnalyticsColumnId, locale: Locale): string {
  const copy = analyticsCopy(locale);
  const keys = { canonical: "analyticsCanonical", language: "analyticsLanguage", messages: "analyticsMessagesColumn", forms: "analyticsForms", actions: "analyticsActions" } as const;
  if (column === "class") return copy.category;
  if (column === "frequency") return copy.mentions;
  if (column === "trend") return copy.trend;
  if (column === "lastSeen") return copy.lastSeen;
  return t(locale, keys[column]);
}

function Metric({ className, icon, label, value, hint }: { className?: string; icon?: ReactNode; label: string; value: ReactNode; hint?: string }) {
  return <div className={className}><dt>{label}</dt><dd>{icon}{value}</dd>{hint && <small>{hint}</small>}</div>;
}

function TrendCell({ delta, locale }: { delta: number; locale: Locale }) {
  const copy = analyticsCopy(locale);
  if (!delta) return <span className="analyticsRedesign__trend analyticsRedesign__trend--steady" title={copy.trendHint}><Minus size={13} />{copy.noChange}</span>;
  const rising = delta > 0;
  return <span className={`analyticsRedesign__trend ${rising ? "analyticsRedesign__trend--up" : "analyticsRedesign__trend--down"}`} title={copy.trendHint}>{rising ? <ArrowUp size={13} /> : <ArrowDown size={13} />}{`${rising ? "+" : ""}${delta.toLocaleString(locale)}`}</span>;
}

function classLabel(locale: Locale, keywordClass: CanonicalClass): string {
  return keywordClass === "positive" ? t(locale, "analyticsPositive") : keywordClass === "negative" ? t(locale, "analyticsNegative") : keywordClass === "service" ? t(locale, "analyticsService") : t(locale, "analyticsAllKeywords");
}

function sortableColumnValue(row: CanonicalKeyword, column: AnalyticsColumnId): string | number {
  if (column === "canonical") return row.canonical;
  if (column === "class") return row.class;
  if (column === "language") return row.language;
  if (column === "frequency") return row.frequency;
  if (column === "messages") return row.messageCount;
  if (column === "trend") return row.frequencyDelta;
  if (column === "lastSeen") return Date.parse(row.lastSeen) || 0;
  if (column === "forms") return row.forms.length;
  return row.canonical;
}

function intervalText(locale: Locale, value: number): string { return locale === "ru" ? `${value} мин.` : `${value} minutes`; }
function pageSizes(current: number): number[] { return [...new Set([20, 50, 100, current])].sort((left, right) => left - right); }

export function parseBulkCanonicalValues(value: string): { values: CanonicalImportEntry[]; skipped: number } {
  const entries = new Map<string, CanonicalImportEntry>();
  let skipped = 0;

  const addEntry = (canonicalValue: string, formValues: string[] = []) => {
    const canonical = normalizeImportCanonical(canonicalValue);
    if (!canonical) { skipped++; return; }
    const language = importWordLanguage(canonical.split(" ")[0]);
    const current = entries.get(canonical) ?? { canonical, forms: [] };
    const forms = new Set(current.forms);
    for (const formValue of formValues) {
      const form = normalizeImportWord(formValue);
      if (!form || importWordLanguage(form) !== language) { skipped++; continue; }
      if (form !== canonical) forms.add(form);
    }
    current.forms = [...forms];
    entries.set(canonical, current);
  };

  for (const line of value.split(/\r\n?|\n/u)) {
    for (const item of splitImportLine(line)) {
      if (!item.trim()) continue;
      const forms: string[] = [];
      const canonical = item.replace(/\(([^()]*)\)/gu, (_group, additions: string) => {
        forms.push(...additions.match(/[\p{L}]+/gu) ?? []);
        return " ";
      });
      addEntry(canonical, forms);
    }
  }
  return { values: [...entries.values()], skipped };
}

function splitImportLine(value: string): string[] {
  const entries: string[] = [];
  let depth = 0;
  let start = 0;
  for (let index = 0; index < value.length; index++) {
    const character = value[index];
    if (character === "(") {
      depth++;
    } else if (character === ")") {
      depth = Math.max(0, depth - 1);
    } else if (depth === 0 && (character === "," || character === ";")) {
      entries.push(value.slice(start, index));
      start = index + 1;
    }
  }
  entries.push(value.slice(start));
  return entries;
}

function normalizeImportCanonical(value: string): string {
  const normalized = value.trim().replace(/\s+/gu, " ").toLocaleLowerCase("ru-RU");
  const words = normalized.split(" ");
  const language = importWordLanguage(words[0] ?? "");
  return language && words.every((word) => importWordLanguage(word) === language) ? normalized : "";
}

function normalizeImportWord(value: string): string {
  const normalized = normalizeKeyword(value);
  return importWordLanguage(normalized) ? normalized : "";
}

function importWordLanguage(value: string): "ru" | "en" | "" {
  if (/^[a-z]+$/iu.test(value)) return "en";
  if (/^[а-яё]+$/iu.test(value)) return "ru";
  return "";
}

function analyticsCopy(locale: Locale) {
  if (locale === "ru") return {
    collection: "Сбор keyword",
    metrics: "Метрики аналитики",
    nextRun: "Следующий сбор",
    lastRun: "Последний сбор",
    uniqueKeywords: "Слова (уникальные)",
    groups: "Группы",
    messages: "Сообщения",
    allTime: "за всё время",
    processedGroups: (value: number) => `обработано ${value}`,
    newMessages: (value: number) => `новых ${value}`,
    tableTools: "Инструменты таблицы",
    keywordTable: "Таблица keyword",
    category: "Категория",
    mentions: "Упоминания",
    trend: "Изменение",
    trendHint: "Изменение с предыдущего сбора",
    lastSeen: "Последнее упоминание",
    noChange: "Без изменений",
    rowsSummary: (first: number, last: number, total: number) => total ? `Показано: ${first}-${last} из ${total}` : "Показано: 0 из 0",
    rowsPerPage: "Строк на странице",
    pagination: "Пагинация таблицы keyword",
    previousPage: "Предыдущая страница",
    nextPage: "Следующая страница"
  };
  return {
    collection: "Keyword collection",
    metrics: "Analytics metrics",
    nextRun: "Next collection",
    lastRun: "Last run",
    uniqueKeywords: "Keywords (unique)",
    groups: "Groups",
    messages: "Messages",
    allTime: "All time",
    processedGroups: (value: number) => `${value} processed`,
    newMessages: (value: number) => `${value} new`,
    tableTools: "Keyword table tools",
    keywordTable: "Keyword table",
    category: "Category",
    mentions: "Mentions",
    trend: "Change",
    trendHint: "Change since the previous collection",
    lastSeen: "Last mention",
    noChange: "No change",
    rowsSummary: (first: number, last: number, total: number) => total ? `Showing ${first}-${last} of ${total}` : "Showing 0 of 0",
    rowsPerPage: "Rows per page",
    pagination: "Keyword table pagination",
    previousPage: "Previous page",
    nextPage: "Next page"
  };
}

function tabs(locale: Locale): Array<{ value: CanonicalClass; label: string }> {
  return [
    { value: "neutral", label: t(locale, "analyticsAllKeywords") },
    { value: "positive", label: t(locale, "analyticsPositive") },
    { value: "negative", label: t(locale, "analyticsNegative") },
    { value: "service", label: t(locale, "analyticsService") }
  ];
}

function preferenceDefaults(): Record<CanonicalClass, AnalyticsTablePreferences> {
  return { neutral: defaultAnalyticsTablePreferences("neutral"), positive: defaultAnalyticsTablePreferences("positive"), negative: defaultAnalyticsTablePreferences("negative"), service: defaultAnalyticsTablePreferences("service") };
}

function preferenceDependencies(): AnalyticsTablePreferencesDependencies {
  return { load: (tab) => call("GetAnalyticsTablePreferences", tab), save: (_tab, preference) => call("SaveAnalyticsTablePreferences", preference).then(voidResult) };
}

function canonicalDependencies() {
  return {
    list: (keywordClass: CanonicalClass) => keywordClass === "service" ? call("ListServiceCanonicalKeywords") : call("ListCanonicalKeywords", keywordClass),
    classify: (id: string, keywordClass: CanonicalClass) => call("ClassifyCanonicalKeyword", id, keywordClass).then(voidResult),
    setTrigger: (id: string, active: boolean) => call("SetCanonicalKeywordTrigger", id, active).then(voidResult),
    remove: (id: string) => call("DeleteCanonicalKeyword", id).then(voidResult),
    clear: (keywordClass: CanonicalClass) => keywordClass === "service" ? call("ClearServiceCanonicalKeywords").then(voidResult) : call("ClearCanonicalKeywords", keywordClass).then(voidResult),
    detachForm: (id: string, form: string) => call("RemoveCanonicalForm", id, form).then(voidResult),
    moveForm: (targetId: string, form: string) => call("MoveCanonicalForm", targetId, form).then(voidResult),
    addForm: (targetId: string, form: string) => call("AddCanonicalForm", targetId, form).then(voidResult),
    analyze: () => call("AnalyzeCanonicalKeywords").then(voidResult),
    syncTriggers: () => call("SyncCanonicalTriggers").then(voidResult)
  };
}

async function refreshRuntime(setRuntime: (status: AnalyticsRuntimeStatus) => void, setError: (message: string) => void, quiet: boolean) {
  try { setRuntime(normalizeAnalyticsRuntime(await call("GetCanonicalAnalyticsStatus"))); } catch (cause) { if (!quiet) setError(messageOf(cause)); }
}

export function normalizeAnalyticsRuntime(value: unknown): AnalyticsRuntimeStatus {
  if (!value || typeof value !== "object") return emptyRuntime;
  const row = value as Record<string, unknown>;
  const intervalMinutes = numberValue(row.intervalMinutes ?? row.IntervalMinutes);
  const latest = objectValue(row.latestCollection ?? row.LatestCollection);
  return {
    running: Boolean(row.running ?? row.Running),
    collecting: Boolean(row.collecting ?? row.Collecting),
    intervalMinutes: intervals.includes(intervalMinutes) ? intervalMinutes : 10,
    lastRunAt: stringValue(row.lastRunAt ?? row.LastRunAt),
    nextRunAt: stringValue(row.nextRunAt ?? row.NextRunAt),
    allTimeMessages: numberValue(row.allTimeMessages ?? row.AllTimeMessages),
    allTimeKeywords: numberValue(row.allTimeKeywords ?? row.AllTimeKeywords),
    allTimeGroups: numberValue(row.allTimeGroups ?? row.AllTimeGroups),
    latestCollection: {
      newMessages: numberValue(latest.newMessages ?? latest.NewMessages),
      extractedWords: numberValue(latest.extractedWords ?? latest.ExtractedWords),
      processedGroups: numberValue(latest.processedGroups ?? latest.ProcessedGroups)
    }
  };
}

function call(method: string, ...args: unknown[]): Promise<unknown> {
  const bindings = (window as unknown as { go?: { wails?: { Bindings?: WailsBindings } } }).go?.wails?.Bindings;
  const invoke = bindings?.[method];
  if (!invoke) return Promise.reject(new Error(`Analytics method "${method}" is unavailable.`));
  return invoke(...args);
}

function toggle(current: ReadonlySet<string>, value: string): ReadonlySet<string> { const next = new Set(current); if (next.has(value)) next.delete(value); else next.add(value); return next; }
function voidResult(): void { /* Mutating Wails calls may return a payload or no value. */ }
function messageOf(cause: unknown): string { return cause instanceof Error ? cause.message : String(cause); }
function numberValue(value: unknown): number { return typeof value === "number" ? value : 0; }
function stringValue(value: unknown): string { return typeof value === "string" ? value : ""; }
function objectValue(value: unknown): Record<string, unknown> { return value && typeof value === "object" ? value as Record<string, unknown> : {}; }
function formatLastRun(value: string, locale: Locale): string { return value ? new Date(value).toLocaleString(locale) : t(locale, "analyticsNever"); }
export function formatNextRun(value: string, locale: Locale): string { return value ? new Date(value).toLocaleTimeString(locale, { hour: "2-digit", minute: "2-digit", second: "2-digit" }) : "—"; }
function escapeRegExp(value: string): string { return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"); }
