import { describe, expect, test } from "vitest";
import {
  createLiveStatisticsArrivalTracker,
  createLiveStatisticsUnreadTracker,
  datetimeLocalToRFC3339,
  emptyLiveDeliveryPage,
  formatLiveStatisticsNavLabel,
  formatLiveDeliveryDate,
  formatLiveDeliveryTime,
  formatLiveDeliveryType,
  formatLiveDeliveryStatus,
  formatMegabytes,
  liveDeliveryTimeZone,
  normalizeLiveDeliveryPage
} from "./liveStatistics";

describe("live delivery statistics data", () => {
  test("keeps unread records visible until they are explicitly marked as read", () => {
    const values = new Map<string, string>([["stats.live.readTotal.v1", "17"]]);
    const storage = {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => values.set(key, value)
    };
    const tracker = createLiveStatisticsUnreadTracker(storage);

    expect(tracker.observe(17)).toBe(17);
    expect(tracker.observe(20)).toBe(20);
    expect(tracker.markRead()).toBe(0);
    expect(tracker.observe(20)).toBe(0);
    expect(tracker.observe(23)).toBe(3);

    const restored = createLiveStatisticsUnreadTracker(storage);
    expect(restored.observe(24)).toBe(4);
  });

  test("identifies appended live rows without animating initial, repeated, reset, or shrinking pages", () => {
    const tracker = createLiveStatisticsArrivalTracker();
    const page = (total: number, ids: string[]) => ({
      ...emptyLiveDeliveryPage,
      total,
      rows: ids.map((id) => ({
        id,
        sourceMessage: "",
        triggerCanonicalID: null,
        triggerSnapshot: "",
        triggeredAt: "",
        deliveryType: "",
        accountTitleSnapshot: "",
        finalStatus: "",
        errorCode: "",
        finalizedAt: ""
      }))
    });

    expect(tracker.observe(page(2, ["b", "a"]))).toEqual([]);
    expect(tracker.observe(page(4, ["d", "c", "b", "a"]))).toEqual(["d", "c"]);
    expect(tracker.observe(page(4, ["d", "c", "b", "a"]))).toEqual([]);
    expect(tracker.observe(page(3, ["d", "c", "b"]))).toEqual([]);
    tracker.reset();
    expect(tracker.observe(page(7, ["g", "f", "e"]))).toEqual([]);
  });

  test("adds the unread count to the live statistics navigation label", () => {
    expect(formatLiveStatisticsNavLabel("Live-statistics", 49)).toBe("Live-statistics (49)");
    expect(formatLiveStatisticsNavLabel("Live-statistics", 0)).toBe("Live-statistics");
  });

  test("normalizes a nullable generated DTO into safe table data", () => {
    expect(normalizeLiveDeliveryPage({
      rows: [{
        id: "audit-1", sourceMessage: "Need help", triggerCanonicalID: null, triggerSnapshot: "credit",
        triggeredAt: "2026-07-15T07:00:00Z", deliveryType: "private_message", accountTitleSnapshot: "Operator",
        finalStatus: "successful", errorCode: "", finalizedAt: "2026-07-15T07:00:04Z"
      }],
      total: 1,
      databaseBytes: 2_621_440,
      refreshedAt: "2026-07-15T07:01:00Z"
    })).toEqual({
      rows: [{
        id: "audit-1", sourceMessage: "Need help", triggerCanonicalID: null, triggerSnapshot: "credit",
        triggeredAt: "2026-07-15T07:00:00Z", deliveryType: "private_message", accountTitleSnapshot: "Operator",
        finalStatus: "successful", errorCode: "", finalizedAt: "2026-07-15T07:00:04Z"
      }],
      total: 1,
      databaseBytes: 2_621_440,
      refreshedAt: "2026-07-15T07:01:00Z"
    });
  });

  test("uses an empty page for null and malformed DTO values", () => {
    expect(normalizeLiveDeliveryPage(null)).toEqual(emptyLiveDeliveryPage);
    expect(normalizeLiveDeliveryPage({ rows: [{}], total: "bad", databaseBytes: null })).toEqual({
      ...emptyLiveDeliveryPage,
      rows: [{
        id: "", sourceMessage: "", triggerCanonicalID: null, triggerSnapshot: "", triggeredAt: "", deliveryType: "",
        accountTitleSnapshot: "", finalStatus: "", errorCode: "", finalizedAt: ""
      }]
    });
  });

  test("formats Moscow dates, times, types, statuses, and database megabytes", () => {
    expect(formatLiveDeliveryDate("2026-07-15T07:00:04Z", "ru", "Europe/Moscow")).toBe("15.07.2026");
    expect(formatLiveDeliveryTime("2026-07-15T07:00:04Z", "ru", "Europe/Moscow")).toBe("10:00:04");
    expect(formatLiveDeliveryDate("not-a-date")).toBe("-");
    expect(formatLiveDeliveryTime("not-a-date")).toBe("-");
    expect(formatLiveDeliveryType("private_message")).toBe("ЛС");
    expect(formatLiveDeliveryType("public_reply")).toBe("Ответ");
    expect(formatLiveDeliveryStatus("successful")).toBe("Delivered");
    expect(formatLiveDeliveryStatus("not_delivered")).toBe("Not delivered");
    expect(formatLiveDeliveryStatus("successful", "ru")).toBe("Успешно");
    expect(formatLiveDeliveryStatus("not_delivered", "ru")).toBe("Не доставлено");
    expect(formatLiveDeliveryStatus("successful", "ru", "private_message_closed")).toBe("Закрыта личка");
    expect(formatLiveDeliveryStatus("successful", "en", "private_message_closed")).toBe("DM closed");
    expect(formatMegabytes(54)).toBe("54 B");
    expect(formatMegabytes(2_621_440)).toBe("2.5 MB");
  });

  test("converts datetime-local values with the date-specific browser offset", () => {
    const newYorkOffset = (parts: { month: number }) => parts.month === 1 ? 300 : 240;

    expect(datetimeLocalToRFC3339("2026-01-15T10:30", newYorkOffset)).toBe("2026-01-15T10:30:00-05:00");
    expect(datetimeLocalToRFC3339("2026-07-15T10:30", newYorkOffset)).toBe("2026-07-15T10:30:00-04:00");
    expect(datetimeLocalToRFC3339("", newYorkOffset)).toBe("");
    expect(datetimeLocalToRFC3339("not-a-date", newYorkOffset)).toBe("");
  });

  test("uses Moscow for Russian and the explicit browser zone for English presentation", () => {
    expect(liveDeliveryTimeZone("ru", "America/New_York")).toBe("Europe/Moscow");
    expect(liveDeliveryTimeZone("en", "America/New_York")).toBe("America/New_York");
    expect(liveDeliveryTimeZone("en", "")).toBe("UTC");
    expect(formatLiveDeliveryDate("2026-07-15T03:30:00Z", "en", "America/New_York")).toBe("07/14/2026");
    expect(formatLiveDeliveryTime("2026-07-15T03:30:00Z", "en", "America/New_York")).toBe("23:30:00");
  });
});
