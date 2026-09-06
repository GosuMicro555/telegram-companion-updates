export type ReplyStatisticsBucket = {
  date: string;
  replies: number;
};

export type ReplyStatisticsRow = {
  id: string;
  title: string;
  replies: number;
  publicReplies: number;
  privateMessages: number;
  privateMessagesClosed: number;
  lastActivityAt: string;
};

export type ReplyStatistics = {
  replies: number;
  publicReplies: number;
  privateMessages: number;
  privateMessagesClosed: number;
  channels: number;
  accounts: number;
  averageRepliesPerMinute: number;
  timeSeries: ReplyStatisticsBucket[];
  accountRows: ReplyStatisticsRow[];
  channelRows: ReplyStatisticsRow[];
};

export type ReplyStatisticRow = ReplyStatisticsRow & {
  kind: "account" | "channel";
};

export type ReplyStatisticSort = "title" | "replies" | "publicReplies" | "privateMessages" | "privateMessagesClosed" | "lastActivityAt";
export type SortDirection = "asc" | "desc";

export const emptyReplyStatistics: ReplyStatistics = {
  replies: 0,
  publicReplies: 0,
  privateMessages: 0,
  privateMessagesClosed: 0,
  channels: 0,
  accounts: 0,
  averageRepliesPerMinute: 0,
  timeSeries: [],
  accountRows: [],
  channelRows: []
};

export function normalizeReplyStatistics(value: unknown): ReplyStatistics {
  if (!value || typeof value !== "object") return emptyReplyStatistics;
  const source = value as Record<string, unknown>;
  return {
    replies: numberValue(source.replies),
    publicReplies: numberValue(source.publicReplies),
    privateMessages: numberValue(source.privateMessages),
    privateMessagesClosed: numberValue(source.privateMessagesClosed),
    channels: numberValue(source.channels),
    accounts: numberValue(source.accounts),
    averageRepliesPerMinute: numberValue(source.averageRepliesPerMinute),
    timeSeries: statisticBuckets(source.timeSeries),
    accountRows: statisticRows(source.accountRows),
    channelRows: statisticRows(source.channelRows)
  };
}

export function replyStatisticRows(statistics: ReplyStatistics): ReplyStatisticRow[] {
  return [
    ...statistics.accountRows.map((row) => ({ ...row, kind: "account" as const })),
    ...statistics.channelRows.map((row) => ({ ...row, kind: "channel" as const }))
  ];
}

export function sortReplyStatisticRows(rows: ReplyStatisticRow[], sort: ReplyStatisticSort, direction: SortDirection): ReplyStatisticRow[] {
  const multiplier = direction === "asc" ? 1 : -1;
  return [...rows].sort((left, right) => {
    if (sort === "publicReplies" || sort === "privateMessages" || sort === "privateMessagesClosed") {
      if (left.kind !== right.kind) return left.kind === "account" ? -1 : 1;
      if (left.kind === "channel") return left.title.localeCompare(right.title);
      return (left[sort] - right[sort]) * multiplier || left.title.localeCompare(right.title);
    }
    if (sort === "replies") return (left.replies - right.replies) * multiplier || left.title.localeCompare(right.title);
    if (sort === "lastActivityAt") return (Date.parse(left.lastActivityAt) - Date.parse(right.lastActivityAt)) * multiplier || left.title.localeCompare(right.title);
    return left.title.localeCompare(right.title) * multiplier;
  });
}

export function lineChartPoints(series: ReplyStatisticsBucket[], width: number, height: number): string {
  if (!series.length) return "";
  const values = series.map((bucket) => bucket.replies);
  const minimum = Math.min(...values);
  const maximum = Math.max(...values);
  const span = maximum - minimum || 1;
  return series.map((bucket, index) => {
    const x = series.length === 1 ? width / 2 : (index / (series.length - 1)) * width;
    const y = height - ((bucket.replies - minimum) / span) * height;
    return `${numberLabel(x)},${numberLabel(y)}`;
  }).join(" ");
}

export function replyStatisticsDateRange(now = new Date()): { fromISO: string; toISO: string } {
  const to = localISODate(now);
  const from = new Date(now);
  from.setDate(from.getDate() - 29);
  return { fromISO: localISODate(from), toISO: to };
}

function statisticBuckets(value: unknown): ReplyStatisticsBucket[] {
  if (!Array.isArray(value)) return [];
  return value.flatMap((item) => {
    if (!item || typeof item !== "object") return [];
    const source = item as Record<string, unknown>;
    return typeof source.date === "string" ? [{ date: source.date, replies: numberValue(source.replies) }] : [];
  });
}

function statisticRows(value: unknown): ReplyStatisticsRow[] {
  if (!Array.isArray(value)) return [];
  return value.flatMap((item) => {
    if (!item || typeof item !== "object") return [];
    const source = item as Record<string, unknown>;
    if (typeof source.id !== "string" || typeof source.title !== "string") return [];
    return [{
      id: source.id,
      title: source.title,
      replies: numberValue(source.replies),
      publicReplies: numberValue(source.publicReplies),
      privateMessages: numberValue(source.privateMessages),
      privateMessagesClosed: numberValue(source.privateMessagesClosed),
      lastActivityAt: typeof source.lastActivityAt === "string" ? source.lastActivityAt : ""
    }];
  });
}

function numberValue(value: unknown): number {
  const numeric = typeof value === "number" ? value : Number(value);
  return Number.isFinite(numeric) ? numeric : 0;
}

function localISODate(value: Date): string {
  const year = value.getFullYear();
  const month = String(value.getMonth() + 1).padStart(2, "0");
  const day = String(value.getDate()).padStart(2, "0");
  return `${year}-${month}-${day}`;
}

function numberLabel(value: number): string {
  return Number.isInteger(value) ? String(value) : String(Number(value.toFixed(2)));
}
