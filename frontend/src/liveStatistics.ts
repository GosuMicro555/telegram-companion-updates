export type LiveDeliveryRow = {
  id: string;
  sourceMessage: string;
  triggerCanonicalID: string | null;
  triggerSnapshot: string;
  triggeredAt: string;
  deliveryType: string;
  accountTitleSnapshot: string;
  finalStatus: string;
  errorCode: string;
  finalizedAt: string;
};

export type LiveDeliveryPage = {
  rows: LiveDeliveryRow[];
  total: number;
  databaseBytes: number;
  refreshedAt: string;
};

export const livePageSizes = [50, 100, 200, 500, 1000] as const;

export const emptyLiveDeliveryPage: LiveDeliveryPage = {
  rows: [],
  total: 0,
  databaseBytes: 0,
  refreshedAt: ""
};

const liveStatisticsReadTotalKey = "stats.live.readTotal.v2";

type LiveStatisticsStorage = Pick<Storage, "getItem" | "setItem">;

export function createLiveStatisticsUnreadTracker(storage?: LiveStatisticsStorage) {
  let readTotal = readStoredNonNegativeInteger(storage, liveStatisticsReadTotalKey) ?? 0;
  let latestTotal = readTotal;

  const persistReadTotal = (total: number) => {
    readTotal = total;
    try {
      storage?.setItem(liveStatisticsReadTotalKey, String(total));
    } catch {
      // Unavailable browser storage must not stop live-statistics polling.
    }
  };

  return {
    observe(value: number): number {
      const total = nonNegativeInteger(value);
      latestTotal = total;
      if (total < readTotal) {
        persistReadTotal(total);
        return 0;
      }
      return total - readTotal;
    },
    markRead(): number {
      persistReadTotal(latestTotal);
      return 0;
    }
  };
}

export function createLiveStatisticsArrivalTracker() {
  let previousTotal: number | null = null;
  let knownIDs = new Set<string>();

  return {
    observe(page: LiveDeliveryPage): string[] {
      const total = nonNegativeInteger(page.total);
      const rowIDs = page.rows.map((row) => row.id).filter(Boolean);
      if (previousTotal === null || total < previousTotal) {
        knownIDs = new Set(rowIDs);
        previousTotal = total;
        return [];
      }
      const arrivals = total > previousTotal ? rowIDs.filter((id) => !knownIDs.has(id)) : [];
      rowIDs.forEach((id) => knownIDs.add(id));
      previousTotal = total;
      return arrivals;
    },
    reset(): void {
      previousTotal = null;
      knownIDs = new Set<string>();
    }
  };
}

export function formatLiveStatisticsNavLabel(label: string, unread: number): string {
  const count = nonNegativeInteger(unread);
  return count > 0 ? `${label} (${count})` : label;
}

type LocalDateTimeParts = {
  year: number;
  month: number;
  day: number;
  hour: number;
  minute: number;
  second: number;
};

type LocalOffsetResolver = (parts: LocalDateTimeParts) => number;

export function normalizeLiveDeliveryPage(value: unknown): LiveDeliveryPage {
  if (!value || typeof value !== "object") return emptyLiveDeliveryPage;
  const source = value as Record<string, unknown>;
  return {
    rows: Array.isArray(source.rows) ? source.rows.map(normalizeLiveDeliveryRow) : [],
    total: nonNegativeNumber(source.total),
    databaseBytes: nonNegativeNumber(source.databaseBytes),
    refreshedAt: stringValue(source.refreshedAt)
  };
}

export function datetimeLocalToRFC3339(value: string, offsetResolver: LocalOffsetResolver = browserLocalOffset): string {
  if (!value) return "";
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})(?::(\d{2})(\.\d{1,9})?)?$/.exec(value);
  if (!match) return "";
  const parts: LocalDateTimeParts = {
    year: Number(match[1]), month: Number(match[2]), day: Number(match[3]),
    hour: Number(match[4]), minute: Number(match[5]), second: Number(match[6] ?? "0")
  };
  const probe = new Date(Date.UTC(parts.year, parts.month - 1, parts.day, parts.hour, parts.minute, parts.second));
  if (probe.getUTCFullYear() !== parts.year || probe.getUTCMonth() !== parts.month - 1 || probe.getUTCDate() !== parts.day ||
      probe.getUTCHours() !== parts.hour || probe.getUTCMinutes() !== parts.minute || probe.getUTCSeconds() !== parts.second) return "";
  const offsetMinutes = offsetResolver(parts);
  if (!Number.isFinite(offsetMinutes)) return "";
  const offset = Math.trunc(offsetMinutes);
  const sign = offset > 0 ? "-" : "+";
  const absoluteOffset = Math.abs(offset);
  const offsetHours = String(Math.floor(absoluteOffset / 60)).padStart(2, "0");
  const offsetRemainder = String(absoluteOffset % 60).padStart(2, "0");
  const seconds = String(parts.second).padStart(2, "0");
  return `${match[1]}-${match[2]}-${match[3]}T${match[4]}:${match[5]}:${seconds}${match[7] ?? ""}${sign}${offsetHours}:${offsetRemainder}`;
}

export function liveDeliveryTimeZone(locale: "en" | "ru", browserTimeZone = Intl.DateTimeFormat().resolvedOptions().timeZone): string {
  return locale === "ru" ? "Europe/Moscow" : browserTimeZone || "UTC";
}

export function formatLiveDeliveryDate(value: string, locale: "en" | "ru" = "ru", timeZone = liveDeliveryTimeZone(locale)): string {
  return dateParts(value, locale, timeZone, { day: "2-digit", month: "2-digit", year: "numeric" });
}

export function formatLiveDeliveryTime(value: string, locale: "en" | "ru" = "ru", timeZone = liveDeliveryTimeZone(locale)): string {
  return dateParts(value, locale, timeZone, { hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23" });
}

export function formatLiveDeliveryType(value: string): string {
  return value === "private_message" ? "ЛС" : value === "public_reply" ? "Ответ" : "-";
}

export function formatLiveDeliveryStatus(value: string, locale: "en" | "ru" = "en", errorCode = ""): string {
  if (errorCode === "private_message_closed") return locale === "ru" ? "Закрыта личка" : "DM closed";
  if (value === "successful") return locale === "ru" ? "Успешно" : "Delivered";
  if (value === "not_delivered") return locale === "ru" ? "Не доставлено" : "Not delivered";
  return "-";
}

export function formatMegabytes(bytes: number): string {
  const normalized = Math.max(0, Math.trunc(bytes));
  if (normalized < 1024) return `${normalized} B`;
  if (normalized < 1024 * 1024) return `${(normalized / 1024).toLocaleString("en-US", { maximumFractionDigits: 1 })} KB`;
  return `${(normalized / (1024 * 1024)).toLocaleString("en-US", { maximumFractionDigits: 1 })} MB`;
}

function normalizeLiveDeliveryRow(value: unknown): LiveDeliveryRow {
  const source = value && typeof value === "object" ? value as Record<string, unknown> : {};
  return {
    id: stringValue(source.id),
    sourceMessage: stringValue(source.sourceMessage),
    triggerCanonicalID: stringValue(source.triggerCanonicalID) || null,
    triggerSnapshot: stringValue(source.triggerSnapshot),
    triggeredAt: stringValue(source.triggeredAt),
    deliveryType: stringValue(source.deliveryType),
    accountTitleSnapshot: stringValue(source.accountTitleSnapshot),
    finalStatus: stringValue(source.finalStatus),
    errorCode: stringValue(source.errorCode),
    finalizedAt: stringValue(source.finalizedAt)
  };
}

function browserLocalOffset(parts: LocalDateTimeParts): number {
  const local = new Date(parts.year, parts.month - 1, parts.day, parts.hour, parts.minute, parts.second);
  if (local.getFullYear() !== parts.year || local.getMonth() !== parts.month - 1 || local.getDate() !== parts.day ||
      local.getHours() !== parts.hour || local.getMinutes() !== parts.minute || local.getSeconds() !== parts.second) return Number.NaN;
  return local.getTimezoneOffset();
}

function dateParts(value: string, locale: "en" | "ru", timeZone: string, options: Intl.DateTimeFormatOptions): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "-";
  return new Intl.DateTimeFormat(locale === "ru" ? "ru-RU" : "en-US", { ...options, timeZone }).format(date);
}

function nonNegativeNumber(value: unknown): number {
  const numeric = typeof value === "number" ? value : Number(value);
  return Number.isFinite(numeric) && numeric >= 0 ? numeric : 0;
}

function nonNegativeInteger(value: unknown): number {
  return Math.trunc(nonNegativeNumber(value));
}

function readStoredNonNegativeInteger(storage: LiveStatisticsStorage | undefined, key: string): number | null {
  try {
    const raw = storage?.getItem(key);
    if (raw == null || raw.trim() === "") return null;
    const value = Number(raw);
    return Number.isInteger(value) && value >= 0 ? value : null;
  } catch {
    return null;
  }
}

function stringValue(value: unknown): string {
  return typeof value === "string" ? value : "";
}
