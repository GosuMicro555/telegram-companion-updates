import { useEffect, useMemo, useState } from "react";
import { GetReplyStatistics } from "../wailsjs/go/wails/Bindings";
import { ConfigurableStatsTable } from "./ConfigurableStatsTableComponent";
import { DateTimeField } from "./DateTimeField";
import type { Locale } from "./i18n";
import {
  emptyReplyStatistics,
  lineChartPoints,
  normalizeReplyStatistics,
  replyStatisticRows,
  replyStatisticsDateRange,
  sortReplyStatisticRows,
  type ReplyStatistics,
  type ReplyStatisticSort,
  type SortDirection
} from "./stats";

const PAGE_SIZE = 8;

const copy = {
  en: {
    replies: "Replies", publicReplies: "Public replies", privateMessages: "Successful DMs", privateMessagesClosed: "Closed DMs",
    channels: "Channels", accounts: "Accounts", average: "Replies / min",
    activity: "Reply activity", from: "Select start date", to: "Select end date", emptyChart: "No reply activity exists for this date range.",
    type: "Type", name: "Name", lastActivity: "Last activity", account: "Account", channel: "Channel",
    previous: "Previous page", next: "Next page", page: "Page", of: "of", emptyTable: "No account or channel replies exist for this date range."
  },
  ru: {
    replies: "Ответы", publicReplies: "Публичные ответы", privateMessages: "Успешные ЛС", privateMessagesClosed: "Закрытые ЛС",
    channels: "Каналы", accounts: "Аккаунты", average: "Ответов / мин",
    activity: "Активность ответов", from: "Выбор даты начала", to: "Выбор даты окончания", emptyChart: "За выбранный диапазон нет активности ответов.",
    type: "Тип", name: "Название", lastActivity: "Последняя активность", account: "Аккаунт", channel: "Канал",
    previous: "Предыдущая страница", next: "Следующая страница", page: "Страница", of: "из", emptyTable: "За выбранный диапазон нет ответов аккаунтов или каналов."
  }
} as const;

type Copy = typeof copy.en;

export function StatsView({ locale }: { locale: Locale }) {
  const initialRange = useMemo(() => replyStatisticsDateTimeRange(), []);
  const [from, setFrom] = useState(initialRange.from);
  const [to, setTo] = useState(initialRange.to);
  const [statistics, setStatistics] = useState<ReplyStatistics>(emptyReplyStatistics);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let canceled = false;
    setLoading(true);
    getReplyStatistics(from, to)
      .then((next) => {
        if (!canceled) {
          setStatistics(next);
          setError("");
        }
      })
      .catch((cause) => {
        if (!canceled) setError(cause instanceof Error ? cause.message : String(cause));
      })
      .finally(() => { if (!canceled) setLoading(false); });
    return () => { canceled = true; };
  }, [from, to]);

  const labels = copy[locale];
  return <section className="statsWorkspace" aria-busy={loading}>
    {error && <div className="errorBanner">{error}</div>}
    <div className="statsDateRange" aria-label={labels.activity}>
      <DateTimeField label={labels.from} locale={locale} max={to} onChange={setFrom} value={from} />
      <DateTimeField label={labels.to} locale={locale} min={from} onChange={setTo} value={to} />
    </div>
    <StatsViewContent locale={locale} statistics={statistics} />
  </section>;
}

export function StatsViewContent({ locale, statistics }: { locale: Locale; statistics: ReplyStatistics }) {
  const labels = copy[locale];
  const [sort, setSort] = useState<ReplyStatisticSort>("replies");
  const [direction, setDirection] = useState<SortDirection>("desc");
  const [page, setPage] = useState(0);
  const rows = useMemo(() => sortReplyStatisticRows(replyStatisticRows(statistics), sort, direction), [direction, sort, statistics]);
  const pageCount = Math.max(1, Math.ceil(rows.length / PAGE_SIZE));
  const activePage = Math.min(page, pageCount - 1);
  const visibleRows = rows.slice(activePage * PAGE_SIZE, (activePage + 1) * PAGE_SIZE);
  const points = lineChartPoints(statistics.timeSeries, 240, 112);
  const localeTag = locale === "en" ? "en-US" : "ru-RU";
  const tableLabel = locale === "en" ? "Account and channel replies" : "\u041e\u0442\u0432\u0435\u0442\u044b \u0430\u043a\u043a\u0430\u0443\u043d\u0442\u043e\u0432 \u0438 \u043a\u0430\u043d\u0430\u043b\u043e\u0432";
  const columnMenuLabel = locale === "en" ? "Choose columns" : "\u0412\u044b\u0431\u0440\u0430\u0442\u044c \u0441\u0442\u043e\u043b\u0431\u0446\u044b";
  const definitions = useMemo(() => [
    { id: "kind" as const, label: labels.type, width: 86, sortable: false, cell: (row: typeof rows[number]) => row.kind === "account" ? labels.account : labels.channel },
    { id: "title" as const, label: labels.name, width: 260, initialSortDirection: "asc" as const, cell: (row: typeof rows[number]) => <strong title={row.title}>{row.title}</strong> },
    { id: "replies" as const, label: labels.replies, width: 120, initialSortDirection: "desc" as const, cell: (row: typeof rows[number]) => row.replies.toLocaleString(localeTag) },
    { id: "publicReplies" as const, label: labels.publicReplies, width: 132, initialSortDirection: "desc" as const, cell: (row: typeof rows[number]) => row.kind === "account" ? row.publicReplies.toLocaleString(localeTag) : "—" },
    { id: "privateMessages" as const, label: labels.privateMessages, width: 132, initialSortDirection: "desc" as const, cell: (row: typeof rows[number]) => row.kind === "account" ? row.privateMessages.toLocaleString(localeTag) : "—" },
    { id: "privateMessagesClosed" as const, label: labels.privateMessagesClosed, width: 132, initialSortDirection: "desc" as const, cell: (row: typeof rows[number]) => row.kind === "account" ? row.privateMessagesClosed.toLocaleString(localeTag) : "—" },
    { id: "lastActivityAt" as const, label: labels.lastActivity, width: 180, initialSortDirection: "desc" as const, cell: (row: typeof rows[number]) => <time dateTime={row.lastActivityAt}>{formatDate(row.lastActivityAt, localeTag)}</time> }
  ], [labels, localeTag]);

  return <>
    <dl className="statsMetrics" aria-label={labels.activity}>
      <Metric label={labels.replies} value={statistics.replies.toLocaleString(localeTag)} />
      <Metric label={labels.publicReplies} value={statistics.publicReplies.toLocaleString(localeTag)} />
      <Metric label={labels.privateMessages} value={statistics.privateMessages.toLocaleString(localeTag)} />
      <Metric label={labels.privateMessagesClosed} value={statistics.privateMessagesClosed.toLocaleString(localeTag)} />
      <Metric label={labels.channels} value={statistics.channels.toLocaleString(localeTag)} />
      <Metric label={labels.accounts} value={statistics.accounts.toLocaleString(localeTag)} />
      <Metric label={labels.average} value={statistics.averageRepliesPerMinute.toLocaleString(localeTag, { maximumFractionDigits: 2 })} />
    </dl>
    <section className="statsPanel statsChartPanel" aria-label={labels.activity}>
      <div className="statsPanelHeader"><h2>{labels.activity}</h2></div>
      {points ? <svg className="statsChart" aria-label={labels.activity} role="img" viewBox="0 0 240 112" preserveAspectRatio="none">
        <polyline fill="none" points={points} vectorEffect="non-scaling-stroke" />
      </svg> : <div className="statsEmpty">{labels.emptyChart}</div>}
    </section>
    <ConfigurableStatsTable
      columnMenuLabel={columnMenuLabel}
      definitions={definitions}
      emptyState={labels.emptyTable}
      onSortChange={(next) => { if (next.column !== "kind") { setPage(0); setSort(next.column); setDirection(next.direction); } }}
      pagination={{ label: tableLabel, pageLabel: `${labels.page} ${activePage + 1} ${labels.of} ${pageCount}`, previousLabel: labels.previous, nextLabel: labels.next, previousDisabled: activePage === 0, nextDisabled: activePage + 1 >= pageCount, onPrevious: () => setPage((current) => Math.max(0, current - 1)), onNext: () => setPage((current) => Math.min(pageCount - 1, current + 1)) }}
      rowKey={(row) => `${row.kind}:${row.id}`}
      rows={visibleRows}
      settingsKey="stats.accounts.table.v1"
      sort={{ column: sort, direction }}
      tableLabel={tableLabel}
    />
  </>;
}

function Metric({ label, value }: { label: string; value: string }) {
  return <div><dt>{label}</dt><dd>{value}</dd></div>;
}

function formatDate(value: string, locale: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "-" : date.toLocaleString(locale, { dateStyle: "medium", timeStyle: "short" });
}

export function replyStatisticsDateTimeRange(now = new Date()): { from: string; to: string } {
  const range = replyStatisticsDateRange(now);
  return { from: `${range.fromISO}T00:00`, to: `${range.toISO}T23:59` };
}

export function dateTimeLocalToRFC3339(value: string): string {
  return value ? new Date(value).toISOString() : "";
}

export function replyStatisticsRequestRange(from: string, to: string): { from: string; to: string } {
  return { from: dateTimeLocalToRFC3339(from), to: dateTimeLocalToRFC3339(to) };
}

function getReplyStatistics(from: string, to: string): Promise<ReplyStatistics> {
  const range = replyStatisticsRequestRange(from, to);
  return GetReplyStatistics(range.from, range.to).then(normalizeReplyStatistics);
}
