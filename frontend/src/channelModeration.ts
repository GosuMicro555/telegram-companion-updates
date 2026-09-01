import type { Locale } from "./i18n";

export type ChannelModerationAccount = {
  title: string;
  role: string;
  requestSubmittedAt: string;
  joinedAt: string;
  durationSeconds: number;
  status: string;
};

export type ChannelModerationRow = {
  key: string;
  catalog: string;
  channelID: string;
  title: string;
  link: string;
  topic: string;
  applications: number;
  joined: number;
  pending: number;
  status: string;
  firstRequestAt: string;
  durationSeconds: number;
  accounts: ChannelModerationAccount[];
};

export type ChannelModerationFilter = "all" | "joining" | "pending_approval" | "partial" | "member";
export type ChannelModerationColumn = "title" | "topic" | "applications" | "joined" | "pending" | "status" | "firstRequestAt" | "durationSeconds" | "actions";
export type ChannelModerationSort = { column: ChannelModerationColumn; direction: "asc" | "desc" };

export function normalizeChannelModeration(value: unknown): ChannelModerationRow[] {
  if (!Array.isArray(value)) return [];
  return value.map(normalizeRow).filter((row): row is ChannelModerationRow => row !== null);
}

export function filterChannelModeration(rows: readonly ChannelModerationRow[], filter: ChannelModerationFilter, search: string): ChannelModerationRow[] {
  const query = search.trim().toLocaleLowerCase();
  return rows.filter((row) => (filter === "all" || row.status === filter) && (!query || [row.title, row.link, row.topic].some((value) => value.toLocaleLowerCase().includes(query))));
}

export function sortChannelModeration(rows: readonly ChannelModerationRow[], sort: ChannelModerationSort): ChannelModerationRow[] {
  const direction = sort.direction === "asc" ? 1 : -1;
  return [...rows].sort((left, right) => {
    const value = compareColumn(left, right, sort.column);
    if (value !== 0) return value * direction;
    return left.title.localeCompare(right.title) || left.link.localeCompare(right.link) || left.key.localeCompare(right.key);
  });
}

export function formatModerationDuration(seconds: number | null | undefined, locale: Locale): string {
  if (seconds == null) return "-";
  const minutes = Math.floor(Math.max(0, Number.isFinite(seconds) ? seconds : 0) / 60);
  const labels = locale === "ru" ? { minute: "мин.", hour: "ч.", day: "д." } : { minute: "min", hour: "h", day: "d" };
  if (minutes < 60) return `${minutes} ${labels.minute}`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} ${labels.hour} ${minutes % 60} ${labels.minute}`;
  return `${Math.floor(hours / 24)} ${labels.day} ${hours % 24} ${labels.hour}`;
}

export function formatModerationDate(value: string, locale: Locale): string {
  const date = new Date(value);
  if (!value || Number.isNaN(date.getTime())) return "-";
  if (locale !== "ru") return date.toLocaleString("en-US", { dateStyle: "medium", timeStyle: "short" });
  const part = (number: number) => String(number).padStart(2, "0");
  return `${part(date.getDate())}.${part(date.getMonth() + 1)}.${date.getFullYear()} | ${part(date.getHours())}:${part(date.getMinutes())}`;
}

function normalizeRow(value: unknown): ChannelModerationRow | null {
  const row = asRecord(value);
  if (!row) return null;
  const catalog = stringValue(firstValue(row, "catalog", "Catalog"));
  const title = stringValue(firstValue(row, "title", "Title"));
  const link = stringValue(firstValue(row, "link", "Link"));
  const firstRequestAt = stringValue(firstValue(row, "firstRequestAt", "FirstRequestAt"));
  const channelID = stringValue(firstValue(row, "channelID", "ChannelID", "id", "ID"));
  return {
    key: channelID || `${catalog}:${link || title}:${firstRequestAt}`,
    catalog,
    channelID,
    title,
    link,
    topic: stringValue(firstValue(row, "topic", "Topic")),
    applications: numberValue(firstValue(row, "applications", "Applications")),
    joined: numberValue(firstValue(row, "joined", "Joined")),
    pending: numberValue(firstValue(row, "pending", "Pending")),
    status: stringValue(firstValue(row, "status", "Status")),
    firstRequestAt,
    durationSeconds: numberValue(firstValue(row, "durationSeconds", "DurationSeconds")),
    accounts: normalizeAccounts(firstValue(row, "accounts", "Accounts"))
  };
}

function normalizeAccounts(value: unknown): ChannelModerationAccount[] {
  if (!Array.isArray(value)) return [];
  return value.flatMap((entry) => {
    const account = asRecord(entry);
    if (!account) return [];
    return [{
      title: stringValue(firstValue(account, "title", "Title")),
      role: stringValue(firstValue(account, "role", "Role")),
      requestSubmittedAt: stringValue(firstValue(account, "requestSubmittedAt", "RequestSubmittedAt")),
      joinedAt: stringValue(firstValue(account, "joinedAt", "JoinedAt")),
      durationSeconds: numberValue(firstValue(account, "durationSeconds", "DurationSeconds")),
      status: stringValue(firstValue(account, "status", "Status"))
    }];
  });
}

function compareColumn(left: ChannelModerationRow, right: ChannelModerationRow, column: ChannelModerationColumn): number {
  switch (column) {
    case "applications": return left.applications - right.applications;
    case "joined": return left.joined - right.joined;
    case "pending": return left.pending - right.pending;
    case "durationSeconds": return left.durationSeconds - right.durationSeconds;
    case "firstRequestAt": return dateValue(left.firstRequestAt) - dateValue(right.firstRequestAt);
    case "title": return left.title.localeCompare(right.title);
    case "topic": return left.topic.localeCompare(right.topic);
    case "status": return left.status.localeCompare(right.status);
    case "actions": return 0;
  }
}

function asRecord(value: unknown): Record<string, unknown> | null {
  return value && typeof value === "object" ? value as Record<string, unknown> : null;
}

function firstValue(row: Record<string, unknown>, ...keys: string[]): unknown {
  return keys.map((key) => row[key]).find((value) => value !== undefined && value !== null);
}

function stringValue(value: unknown): string { return typeof value === "string" ? value : ""; }
function numberValue(value: unknown): number { const number = typeof value === "number" ? value : Number(value); return Number.isFinite(number) ? number : 0; }
function dateValue(value: string): number { const timestamp = new Date(value).getTime(); return Number.isNaN(timestamp) ? 0 : timestamp; }
