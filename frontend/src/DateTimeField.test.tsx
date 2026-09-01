import { renderToStaticMarkup } from "react-dom/server";
import { expect, test } from "vitest";
import {
  DateTimeField,
  formatDateTimeValue,
  isDateTimeValueWithinBounds,
  monthGrid,
  parseDateTimeValue,
  shouldDismissDateTimePopover
} from "./DateTimeField";

test("parses and formats the controlled local datetime value without changing its wire format", () => {
  const draft = parseDateTimeValue("2026-07-15T09:05", new Date(2026, 0, 1, 14, 30));

  expect(draft).toEqual({ year: 2026, month: 7, day: 15, hour: 9, minute: 5 });
  expect(formatDateTimeValue(draft)).toBe("2026-07-15T09:05");
});

test("pads calendar years to four digits in values and month grids", () => {
  expect(formatDateTimeValue({ year: 1, month: 2, day: 3, hour: 4, minute: 5 })).toBe("0001-02-03T04:05");
  expect(monthGrid(1, 2).some((day) => day.date === "0001-02-01")).toBe(true);
});

test("uses the supplied local current time when the controlled value is empty", () => {
  expect(parseDateTimeValue("", new Date(2026, 6, 15, 9, 5))).toEqual({
    year: 2026, month: 7, day: 15, hour: 9, minute: 5
  });
});

test("builds a Monday-first six-week grid that includes adjacent-month dates", () => {
  const grid = monthGrid(2026, 2);

  expect(grid).toHaveLength(42);
  expect(grid[0]).toEqual({ date: "2026-01-26", day: 26, inCurrentMonth: false });
  expect(grid[6]).toEqual({ date: "2026-02-01", day: 1, inCurrentMonth: true });
  expect(grid[41]).toEqual({ date: "2026-03-08", day: 8, inCurrentMonth: false });
});

test("recognizes Escape and outside pointer events as popover dismissal", () => {
  expect(shouldDismissDateTimePopover({ key: "Escape", targetIsInside: true })).toBe(true);
  expect(shouldDismissDateTimePopover({ key: "Enter", targetIsInside: false })).toBe(true);
  expect(shouldDismissDateTimePopover({ key: "Enter", targetIsInside: true })).toBe(false);
});

test("rejects date-time values outside optional Apply bounds", () => {
  expect(isDateTimeValueWithinBounds("2026-07-15T10:00", "2026-07-15T10:00", "2026-07-15T11:00")).toBe(true);
  expect(isDateTimeValueWithinBounds("2026-07-15T09:59", "2026-07-15T10:00", "2026-07-15T11:00")).toBe(false);
  expect(isDateTimeValueWithinBounds("2026-07-15T11:01", "2026-07-15T10:00", "2026-07-15T11:00")).toBe(false);
});

test("renders a labelled custom trigger without a native datetime-local control", () => {
  const markup = renderToStaticMarkup(<DateTimeField label="Select start date" locale="en" onChange={() => undefined} value="2026-07-15T09:05" />);

  expect(markup).toContain("Select start date");
  expect(markup).toContain('aria-haspopup="dialog"');
  expect(markup).toContain("2026-07-15 09:05");
  expect(markup).not.toContain('type="datetime-local"');
});
