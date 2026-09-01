import { ChevronDown, ChevronRight, Search } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { GetChannelModeration, RetryCatalogJoin } from "../wailsjs/go/wails/Bindings";
import { ConfigurableStatsTable } from "./ConfigurableStatsTableComponent";
import {
  filterChannelModeration,
  formatModerationDate,
  formatModerationDuration,
  normalizeChannelModeration,
  sortChannelModeration,
  type ChannelModerationFilter,
  type ChannelModerationRow,
  type ChannelModerationSort
} from "./channelModeration";
import { t, type Locale } from "./i18n";

type ChannelModerationViewContentProps = {
  rows: readonly ChannelModerationRow[];
  locale: Locale;
  filter: ChannelModerationFilter;
  search: string;
  sort: ChannelModerationSort;
  expanded: ReadonlySet<string>;
  retrying?: ReadonlySet<string>;
  error: string;
  onFilter(filter: ChannelModerationFilter): void;
  onSearch(search: string): void;
  onSort(sort: ChannelModerationSort): void;
  onToggle(key: string): void;
  onRetry?(row: ChannelModerationRow): void;
};

export function ChannelModerationView({ locale }: { locale: Locale }) {
  const [rows, setRows] = useState<ChannelModerationRow[]>([]);
  const [filter, setFilter] = useState<ChannelModerationFilter>("all");
  const [search, setSearch] = useState("");
  const [sort, setSort] = useState<ChannelModerationSort>({ column: "firstRequestAt", direction: "desc" });
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(new Set());
  const [retrying, setRetrying] = useState<ReadonlySet<string>>(new Set());
  const [error, setError] = useState("");

  useEffect(() => createChannelModerationPolling(GetChannelModeration, setRows, setError), []);

  return <ChannelModerationViewContent
    error={error}
    expanded={expanded}
    filter={filter}
    locale={locale}
    onFilter={setFilter}
    onSearch={setSearch}
    onSort={setSort}
    onToggle={(key) => setExpanded((current) => toggleExpanded(current, key))}
    onRetry={(row) => {
      setRetrying((current) => withValue(current, row.key, true));
      void retryChannelModerationRow(row).then((nextRows) => {
        setRows(nextRows);
        setError("");
      }).catch((cause) => setError(errorMessage(cause))).finally(() => {
        setRetrying((current) => withValue(current, row.key, false));
      });
    }}
    retrying={retrying}
    rows={rows}
    search={search}
    sort={sort}
  />;
}

export async function retryChannelModerationRow(
  row: ChannelModerationRow,
  retry: (catalog: string, channelID: string) => Promise<unknown> = RetryCatalogJoin
): Promise<ChannelModerationRow[]> {
  if (!row.channelID) throw new Error("channel ID is required to retry joining");
  return normalizeChannelModeration(await retry(row.catalog, row.channelID));
}

export function createChannelModerationPolling(
  load: () => Promise<unknown>,
  onRows: (rows: ChannelModerationRow[]) => void,
  onError: (message: string) => void
): () => void {
  let canceled = false;
  const refresh = async () => {
    try {
      const value = await load();
      if (!canceled) {
        onRows(normalizeChannelModeration(value));
        onError("");
      }
    } catch (cause) {
      if (!canceled) onError(errorMessage(cause));
    }
  };
  void refresh();
  const timer = globalThis.setInterval(() => void refresh(), 15_000);
  return () => {
    canceled = true;
    globalThis.clearInterval(timer);
  };
}

export function ChannelModerationViewContent(props: ChannelModerationViewContentProps) {
  const visibleRows = useMemo(() => sortChannelModeration(filterChannelModeration(props.rows, props.filter, props.search), props.sort), [props.filter, props.rows, props.search, props.sort]);
  const filters: Array<{ value: ChannelModerationFilter; label: string; count: number }> = [
    { value: "all", label: t(props.locale, "channelModerationAll"), count: props.rows.length },
    { value: "joining", label: t(props.locale, "channelModerationJoining"), count: props.rows.filter((row) => row.status === "joining").length },
    { value: "pending_approval", label: t(props.locale, "channelModerationPending"), count: props.rows.filter((row) => row.status === "pending_approval").length },
    { value: "partial", label: t(props.locale, "channelModerationPartial"), count: props.rows.filter((row) => row.status === "partial").length },
    { value: "member", label: t(props.locale, "channelModerationMember"), count: props.rows.filter((row) => row.status === "member").length }
  ];
  const definitions = useMemo(() => [
    { id: "title" as const, label: t(props.locale, "channelModerationChannel"), width: 270, initialSortDirection: "asc" as const, cell: (row: ChannelModerationRow) => <div className="channelModeration__channel"><button aria-expanded={props.expanded.has(row.key)} aria-label={t(props.locale, "channelModerationToggle")} className="channelModeration__toggle" onClick={() => props.onToggle(row.key)} title={t(props.locale, "channelModerationToggle")} type="button">{props.expanded.has(row.key) ? <ChevronDown size={15} /> : <ChevronRight size={15} />}</button><a href={row.link || undefined} rel="noreferrer" target="_blank">{row.title || row.link || "-"}</a></div> },
    { id: "topic" as const, label: t(props.locale, "channelModerationTopic"), width: 160, initialSortDirection: "asc" as const, cell: (row: ChannelModerationRow) => row.topic || "-" },
    { id: "applications" as const, label: t(props.locale, "channelModerationApplications"), width: 104, initialSortDirection: "desc" as const, cell: (row: ChannelModerationRow) => row.applications.toLocaleString(localeTag(props.locale)) },
    { id: "joined" as const, label: t(props.locale, "channelModerationJoined"), width: 104, initialSortDirection: "desc" as const, cell: (row: ChannelModerationRow) => row.joined.toLocaleString(localeTag(props.locale)) },
    { id: "pending" as const, label: t(props.locale, "channelModerationWaiting"), width: 112, initialSortDirection: "desc" as const, cell: (row: ChannelModerationRow) => row.pending.toLocaleString(localeTag(props.locale)) },
    { id: "status" as const, label: t(props.locale, "channelModerationStatus"), width: 160, initialSortDirection: "asc" as const, cell: (row: ChannelModerationRow) => <span className={`channelModeration__status channelModeration__status--${row.status}`}>{statusLabel(row.status, props.locale)}</span> },
    { id: "firstRequestAt" as const, label: t(props.locale, "channelModerationFirstRequest"), width: 176, initialSortDirection: "desc" as const, cell: (row: ChannelModerationRow) => moderationDateCell(row.firstRequestAt, props.locale) },
    { id: "durationSeconds" as const, label: t(props.locale, "channelModerationDuration"), width: 132, initialSortDirection: "desc" as const, cell: (row: ChannelModerationRow) => formatModerationDuration(row.firstRequestAt ? row.durationSeconds : undefined, props.locale) },
    { id: "actions" as const, label: t(props.locale, "channelModerationActions"), width: 150, sortable: false, cell: (row: ChannelModerationRow) => row.status === "pending_approval" ? <button className="channelModeration__retry" disabled={props.retrying?.has(row.key) || !row.channelID} onClick={() => props.onRetry?.(row)} type="button">{props.retrying?.has(row.key) ? t(props.locale, "channelModerationRetrying") : t(props.locale, "channelModerationRetry")}</button> : null }
  ], [props.expanded, props.locale, props.onRetry, props.onToggle, props.retrying]);

  return <section className="channelModerationWorkspace" aria-label={t(props.locale, "channelModeration")}>
    {props.error && <div className="errorBanner" role="alert">{props.error}</div>}
    <div className="channelModeration__controls">
      <div className="channelModeration__filters" role="group" aria-label={t(props.locale, "channelModerationFilters")}>
        {filters.map((item) => <button aria-pressed={props.filter === item.value} className={props.filter === item.value ? "active" : ""} key={item.value} onClick={() => props.onFilter(item.value)} type="button">{item.label} ({item.count})</button>)}
      </div>
      <div className="channelModeration__search" role="search"><Search size={15} /><input aria-label={t(props.locale, "channelModerationSearch")} onChange={(event) => props.onSearch(event.target.value)} placeholder={t(props.locale, "channelModerationSearch")} value={props.search} /></div>
    </div>
    <ConfigurableStatsTable
      columnMenuLabel={t(props.locale, "channelModerationColumns")}
      definitions={definitions}
      emptyState={t(props.locale, "channelModerationEmpty")}
      onSortChange={(next) => props.onSort(next)}
      renderAfterRow={(row) => props.expanded.has(row.key) ? <AccountDetails locale={props.locale} row={row} /> : null}
      rowKey={(row) => row.key}
      rows={visibleRows}
      settingsKey="channel-moderation.table.v1"
      sort={props.sort}
      tableLabel={t(props.locale, "channelModerationTable")}
    />
  </section>;
}

function AccountDetails({ locale, row }: { locale: Locale; row: ChannelModerationRow }) {
  return <div className="channelModeration__accounts" role="table" aria-label={t(locale, "channelModerationAccounts")}>
    <div className="channelModeration__accountHeader" role="row"><span>{t(locale, "channelModerationAccount")}</span><span>{t(locale, "channelModerationRole")}</span><span>{t(locale, "channelModerationRequestSubmitted")}</span><span>{t(locale, "channelModerationJoinedAt")}</span><span>{t(locale, "channelModerationWaitingTime")}</span><span>{t(locale, "channelModerationStatus")}</span></div>
    {row.accounts.map((account, index) => <div className="channelModeration__accountRow" key={`${account.title}:${account.requestSubmittedAt}:${index}`} role="row"><span>{account.title || "-"}</span><span>{account.role || "-"}</span>{moderationDateCell(account.requestSubmittedAt, locale)}{moderationDateCell(account.joinedAt, locale)}<span>{formatModerationDuration(account.requestSubmittedAt ? account.durationSeconds : undefined, locale)}</span><span className={`channelModeration__status channelModeration__status--${account.status}`}>{statusLabel(account.status, locale)}</span></div>)}
  </div>;
}

function toggleExpanded(current: ReadonlySet<string>, key: string): ReadonlySet<string> {
  const next = new Set(current);
  if (next.has(key)) next.delete(key);
  else next.add(key);
  return next;
}

function withValue(current: ReadonlySet<string>, value: string, present: boolean): ReadonlySet<string> {
  const next = new Set(current);
  if (present) next.add(value);
  else next.delete(value);
  return next;
}

function statusLabel(status: string, locale: Locale): string {
  if (status === "joining") return t(locale, "channelModerationJoining");
  if (status === "pending_approval") return t(locale, "channelModerationPending");
  if (status === "partial") return t(locale, "channelModerationPartial");
  if (status === "member") return t(locale, "channelModerationMember");
  return status || "-";
}

function moderationDateCell(value: string, locale: Locale) {
  return value ? <time dateTime={value}>{formatModerationDate(value, locale)}</time> : <span>-</span>;
}

function localeTag(locale: Locale): string { return locale === "ru" ? "ru-RU" : "en-US"; }
function errorMessage(cause: unknown): string { return cause instanceof Error ? cause.message : String(cause); }
