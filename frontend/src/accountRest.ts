import type { Locale } from "./i18n";

export type AccountRestStatus = "resting" | "ready";

export type AccountRestRow = {
  key: string;
  accountID: string;
  accountTitle: string;
  channelID: string;
  channelTitle: string;
  catalog: string;
  startedAt: string;
  until: string;
  durationHours: number;
  status: AccountRestStatus;
};

export function normalizeAccountRests(value: unknown, now = new Date()): AccountRestRow[] {
  if (!Array.isArray(value)) return [];
  return value.map((item, index) => normalizeAccountRest(item, index, now));
}

function normalizeAccountRest(value: unknown, index: number, now: Date): AccountRestRow {
  const row = value && typeof value === "object" ? value as Record<string, unknown> : {};
  const accountID = text(row, "accountID", "accountId");
  const channelID = text(row, "channelID", "channelId");
  const until = text(row, "until");
  return {
    key: `${accountID}:${channelID}:${until}:${index}`,
    accountID,
    accountTitle: text(row, "accountTitle"),
    channelID,
    channelTitle: text(row, "channelTitle"),
    catalog: text(row, "catalog"),
    startedAt: text(row, "startedAt"),
    until,
    durationHours: number(row.durationHours),
    status: isResting(until, now) ? "resting" : "ready"
  };
}

export function isResting(until: string, now: Date): boolean {
  const untilTime = Date.parse(until);
  return Number.isFinite(untilTime) && untilTime > now.getTime();
}

export function formatAccountRestCountdown(until: string, now: Date, locale: Locale): string {
  const milliseconds = Math.max(0, Date.parse(until) - now.getTime());
  const totalMinutes = Number.isFinite(milliseconds) ? Math.floor(milliseconds / 60_000) : 0;
  const hours = Math.floor(totalMinutes / 60);
  const minutes = totalMinutes % 60;
  if (locale === "ru") return hours > 0 ? `${hours} ч ${minutes} мин` : `${minutes} мин`;
  return hours > 0 ? `${hours}h ${minutes}m` : `${minutes}m`;
}

export function formatAccountRestDate(value: string, locale: Locale): string {
  const time = new Date(value);
  return Number.isNaN(time.getTime()) ? "-" : time.toLocaleString(locale === "ru" ? "ru-RU" : "en-US");
}

function text(row: Record<string, unknown>, ...keys: string[]): string {
  const value = keys.map((key) => row[key]).find((item) => typeof item === "string");
  return typeof value === "string" ? value : "";
}

function number(value: unknown): number {
  const result = typeof value === "number" ? value : Number(value);
  return Number.isFinite(result) ? result : 0;
}
