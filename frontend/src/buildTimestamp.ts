const MOSCOW_TIME_ZONE = "Europe/Moscow";

export function formatBuildTimestamp(value: string): string {
  const timestamp = Date.parse(value);
  if (!value.trim() || Number.isNaN(timestamp)) return "-";

  const parts = new Intl.DateTimeFormat("ru-RU", {
    day: "2-digit",
    hour: "2-digit",
    hourCycle: "h23",
    minute: "2-digit",
    month: "2-digit",
    timeZone: MOSCOW_TIME_ZONE,
    year: "numeric"
  }).formatToParts(timestamp);
  const part = (type: Intl.DateTimeFormatPartTypes) => parts.find((item) => item.type === type)?.value ?? "";

  return `${part("day")}.${part("month")}.${part("year")} | ${part("hour")}:${part("minute")}`;
}

export const BUILD_TIMESTAMP_LABEL = formatBuildTimestamp(import.meta.env.VITE_BUILD_TIMESTAMP ?? "");
