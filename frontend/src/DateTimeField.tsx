import { CalendarDays, ChevronLeft, ChevronRight } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { t, type Locale } from "./i18n";

export type DateTimeFieldProps = { value: string; onChange(value: string): void; label: string; locale: Locale; min?: string; max?: string };
export type DateTimeDraft = { year: number; month: number; day: number; hour: number; minute: number };
export type MonthGridDay = { date: string; day: number; inCurrentMonth: boolean };

const dateTimeValuePattern = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/;
const weekdayLabels = {
  en: ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"],
  ru: ["\u041f\u043d", "\u0412\u0442", "\u0421\u0440", "\u0427\u0442", "\u041f\u0442", "\u0421\u0431", "\u0412\u0441"]
} as const;

export function parseDateTimeValue(value: string, now = new Date()): DateTimeDraft {
  const match = dateTimeValuePattern.exec(value);
  if (match) {
    const draft = { year: Number(match[1]), month: Number(match[2]), day: Number(match[3]), hour: Number(match[4]), minute: Number(match[5]) };
    if (isValidDateTimeDraft(draft)) return draft;
  }
  return { year: now.getFullYear(), month: now.getMonth() + 1, day: now.getDate(), hour: now.getHours(), minute: now.getMinutes() };
}

export function formatDateTimeValue(value: DateTimeDraft): string {
  const normalized = normalizeDateTimeDraft(value);
  return `${padYear(normalized.year)}-${pad(normalized.month)}-${pad(normalized.day)}T${pad(normalized.hour)}:${pad(normalized.minute)}`;
}

export function monthGrid(year: number, month: number): MonthGridDay[] {
  const firstDay = calendarDate(year, month - 1, 1);
  const mondayOffset = (firstDay.getDay() + 6) % 7;
  return Array.from({ length: 42 }, (_, index) => {
    const value = new Date(firstDay);
    value.setDate(firstDay.getDate() + index - mondayOffset);
    return { date: localDate(value), day: value.getDate(), inCurrentMonth: value.getFullYear() === year && value.getMonth() === month - 1 };
  });
}

export function isDateTimeValueWithinBounds(value: string, min?: string, max?: string): boolean {
  return (!min || value >= min) && (!max || value <= max);
}

export function shouldDismissDateTimePopover(event: { key?: string; targetIsInside: boolean }): boolean {
  return event.key === "Escape" || !event.targetIsInside;
}

export function DateTimeField({ value, onChange, label, locale, min, max }: DateTimeFieldProps) {
  const rootRef = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState(() => parseDateTimeValue(value));
  const normalizedDraft = normalizeDateTimeDraft(draft);
  const days = monthGrid(normalizedDraft.year, normalizedDraft.month);

  useEffect(() => {
    if (!open) return;
    const dismissOnPointerDown = (event: PointerEvent) => {
      if (shouldDismissDateTimePopover({ targetIsInside: Boolean(rootRef.current?.contains(event.target as Node)) })) setOpen(false);
    };
    const dismissOnKeyDown = (event: KeyboardEvent) => {
      if (shouldDismissDateTimePopover({ key: event.key, targetIsInside: true })) setOpen(false);
    };
    document.addEventListener("pointerdown", dismissOnPointerDown);
    document.addEventListener("keydown", dismissOnKeyDown);
    return () => { document.removeEventListener("pointerdown", dismissOnPointerDown); document.removeEventListener("keydown", dismissOnKeyDown); };
  }, [open]);

  const openPopover = () => { setDraft(parseDateTimeValue(value)); setOpen(true); };
  const changeMonth = (offset: number) => {
    const next = new Date(normalizedDraft.year, normalizedDraft.month - 1 + offset, 1);
    setDraft((current) => ({ ...current, year: next.getFullYear(), month: next.getMonth() + 1, day: Math.min(current.day, daysInMonth(next.getFullYear(), next.getMonth() + 1)) }));
  };
  const selectDay = (date: string) => {
    const selected = parseDateTimeValue(`${date}T00:00`);
    setDraft((current) => ({ ...current, year: selected.year, month: selected.month, day: selected.day }));
  };
  const updateTime = (part: "hour" | "minute", raw: string) => {
    const value = Number(raw);
    setDraft((current) => ({ ...current, [part]: Number.isFinite(value) ? value : 0 }));
  };
  const nextValue = formatDateTimeValue(draft);
  const canApply = isDateTimeValueWithinBounds(nextValue, min, max);
  const apply = () => { if (canApply) { onChange(nextValue); setOpen(false); } };
  const clear = () => { onChange(""); setOpen(false); };
  const displayedValue = value ? value.replace("T", " ") : t(locale, "dateTimeEmpty");
  const monthName = new Intl.DateTimeFormat(locale === "ru" ? "ru-RU" : "en-US", { month: "long", year: "numeric" }).format(new Date(normalizedDraft.year, normalizedDraft.month - 1, 1));

  return <div className="dateTimeField" ref={rootRef}>
    <span className="dateTimeField__label">{label}</span>
    <button aria-expanded={open} aria-haspopup="dialog" aria-label={label} className="dateTimeField__trigger" onClick={openPopover} type="button"><CalendarDays aria-hidden="true" size={15} /><span>{displayedValue}</span></button>
    {open && <div aria-label={label} className="dateTimeField__popover" role="dialog">
      <div className="dateTimeField__header">
        <button aria-label={t(locale, "dateTimePreviousMonth")} onClick={() => changeMonth(-1)} title={t(locale, "dateTimePreviousMonth")} type="button"><ChevronLeft aria-hidden="true" size={16} /></button>
        <strong>{monthName}</strong>
        <button aria-label={t(locale, "dateTimeNextMonth")} onClick={() => changeMonth(1)} title={t(locale, "dateTimeNextMonth")} type="button"><ChevronRight aria-hidden="true" size={16} /></button>
      </div>
      <div aria-label={monthName} className="dateTimeField__calendar" role="grid">
        {weekdayLabels[locale].map((weekday) => <span key={weekday}>{weekday}</span>)}
        {days.map((day) => <button aria-label={day.date} aria-pressed={day.date === formatDateTimeValue(normalizedDraft).slice(0, 10)} className={day.inCurrentMonth ? "" : "is-adjacent"} key={day.date} onClick={() => selectDay(day.date)} role="gridcell" type="button">{day.day}</button>)}
      </div>
      <div className="dateTimeField__time">
        <label>{t(locale, "dateTimeHours")}<input aria-label={t(locale, "dateTimeHours")} max="23" min="0" onChange={(event) => updateTime("hour", event.target.value)} type="number" value={normalizedDraft.hour} /></label>
        <span>:</span>
        <label>{t(locale, "dateTimeMinutes")}<input aria-label={t(locale, "dateTimeMinutes")} max="59" min="0" onChange={(event) => updateTime("minute", event.target.value)} type="number" value={normalizedDraft.minute} /></label>
      </div>
      <div className="dateTimeField__actions"><button className="dateTimeField__clear" onClick={clear} type="button">{t(locale, "dateTimeClear")}</button><button className="dateTimeField__apply" disabled={!canApply} onClick={apply} type="button">{t(locale, "dateTimeApply")}</button></div>
    </div>}
  </div>;
}

function normalizeDateTimeDraft(value: DateTimeDraft): DateTimeDraft {
  const year = Math.max(1, Math.trunc(value.year) || 1);
  const month = clamp(Math.trunc(value.month) || 1, 1, 12);
  return { year, month, day: clamp(Math.trunc(value.day) || 1, 1, daysInMonth(year, month)), hour: clamp(Math.trunc(value.hour) || 0, 0, 23), minute: clamp(Math.trunc(value.minute) || 0, 0, 59) };
}

function isValidDateTimeDraft(value: DateTimeDraft): boolean {
  return value.month >= 1 && value.month <= 12 && value.day >= 1 && value.day <= daysInMonth(value.year, value.month) && value.hour >= 0 && value.hour <= 23 && value.minute >= 0 && value.minute <= 59;
}

function daysInMonth(year: number, month: number): number { return calendarDate(year, month, 0).getDate(); }
function clamp(value: number, minimum: number, maximum: number): number { return Math.min(maximum, Math.max(minimum, value)); }
function pad(value: number): string { return String(value).padStart(2, "0"); }
function padYear(value: number): string { return String(value).padStart(4, "0"); }
function localDate(value: Date): string { return `${padYear(value.getFullYear())}-${pad(value.getMonth() + 1)}-${pad(value.getDate())}`; }
function calendarDate(year: number, month: number, day: number): Date { const value = new Date(0); value.setHours(0, 0, 0, 0); value.setFullYear(year, month, day); return value; }
