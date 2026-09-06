import { describe, expect, test } from "vitest";
import {
  clampScheduledDMRuns,
  eligibleScheduledDMAccounts,
  filterScheduledDMSpammerAccounts,
  isScheduledDMAccountSelectable,
  normalizeScheduledDMRecipients,
  recurrenceOptions,
  recipientAssignments,
  recipientStatusKey,
  validateScheduledDMDraft
} from "./scheduledDM";

describe("scheduled DM model", () => {
  test("normalizes unique @username and Telegram profile URLs while reporting invalid contacts", () => {
    expect(normalizeScheduledDMRecipients("@Alice\nhttps://t.me/bobby\nalice\nhttps://example.com/not-telegram\n@bad-name")).toEqual({
      recipients: ["alice", "bobby"],
      invalid: ["https://example.com/not-telegram", "@bad-name"]
    });
  });

  test("accepts only one 5 to 32 character contact per line in supported formats", () => {
    expect(normalizeScheduledDMRecipients("@Alice\nalice\nhttps://t.me/Bobby\nalice bobby\n@four\n@aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\nhttp://t.me/carol\nhttps://www.t.me/dorothy\nhttps://t.me/erin/\nhttps://t.me/frank?start=1")).toEqual({
      recipients: ["alice", "bobby"],
      invalid: ["alice bobby", "@four", "@aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "http://t.me/carol", "https://www.t.me/dorothy", "https://t.me/erin/", "https://t.me/frank?start=1"]
    });
  });

  test("exposes every finite recurrence accepted by the backend", () => {
    expect(recurrenceOptions.map((option) => option.value)).toEqual([
      "once", "5m", "10m", "30m", "1h", "2h", "3h", "4h", "5h", "6h", "7h", "8h", "9h", "10h", "11h", "12h", "daily", "weekly"
    ]);
  });

  test("requires one to three contacts and accounts, a message, future date, and finite runs", () => {
    expect(validateScheduledDMDraft({
      recipients: ["alice", "bob", "carol", "dora"],
      accountIds: ["one"],
      messageText: "Hello",
      startAt: "2026-07-18T08:00",
      recurrence: "once",
      maxRuns: 13
    }, new Date("2026-07-18T07:00:00"))).toEqual(["contactsLimit", "runsLimit"]);

    expect(validateScheduledDMDraft({
      recipients: ["alice"],
      accountIds: ["one"],
      messageText: "Hello",
      startAt: "2026-07-18T08:00",
      recurrence: "once",
      maxRuns: 1
    }, new Date("2026-07-18T07:00:00"))).toEqual([]);

    expect(validateScheduledDMDraft({
      recipients: ["alice"],
      accountIds: ["one"],
      messageText: "Hello",
      startAt: "2026-07-18T08:00",
      recurrence: "once",
      maxRuns: 2
    }, new Date("2026-07-18T07:00:00"))).toEqual(["runsLimit"]);
  });

  test("keeps account assignment stable and displays the selected connected spammer identity", () => {
    const accounts = eligibleScheduledDMAccounts([
      { id: "a", displayName: "Anna", phoneMasked: "+7 900 ***-11-22", role: "spammer", status: "ready" },
      { id: "b", displayName: "Boris", phoneMasked: "+7 900 ***-33-44", role: "spammer", status: "ready" },
      { id: "c", displayName: "Scout", phoneMasked: "+7 900 ***-55-66", role: "scout_analyst", status: "ready" },
      { id: "d", displayName: "Offline", phoneMasked: "+7 900 ***-77-88", role: "spammer", status: "error" }
    ]);

    expect(accounts.map((account) => account.id)).toEqual(["a", "b"]);
    expect(recipientAssignments(["alice", "bob", "carol"], ["a", "b"], accounts)).toEqual([
      { username: "alice", accountId: "a", accountLabel: "Anna, +7 900 ***-11-22" },
      { username: "bob", accountId: "b", accountLabel: "Boris, +7 900 ***-33-44" },
      { username: "carol", accountId: "a", accountLabel: "Anna, +7 900 ***-11-22" }
    ]);
  });

  test("keeps every spammer visible for search while only connected accounts are selectable", () => {
    const accounts = [
      { id: "a", displayName: "Anna", phoneMasked: "+7 900 ***-11-22", role: "spammer", status: "ready" },
      { id: "b", displayName: "Boris", phoneMasked: "+7 900 ***-33-44", role: "spammer", status: "error" },
      { id: "c", displayName: "Scout", phoneMasked: "+7 900 ***-55-66", role: "scout_analyst", status: "ready" }
    ];

    expect(filterScheduledDMSpammerAccounts(accounts, "").map((account) => account.id)).toEqual(["a", "b"]);
    expect(filterScheduledDMSpammerAccounts(accounts, "33-44").map((account) => account.id)).toEqual(["b"]);
    expect(filterScheduledDMSpammerAccounts(accounts, "anna").map((account) => account.id)).toEqual(["a"]);
    expect(isScheduledDMAccountSelectable(accounts[0])).toBe(true);
    expect(isScheduledDMAccountSelectable(accounts[1])).toBe(false);
  });

  test("clamps the run count to the supported one through twelve range", () => {
    expect(clampScheduledDMRuns(0)).toBe(1);
    expect(clampScheduledDMRuns(Number.NaN)).toBe(1);
    expect(clampScheduledDMRuns(7)).toBe(7);
    expect(clampScheduledDMRuns(20)).toBe(12);
  });

  test("only labels resolved, invalid, and error during preflight; delivery outcomes add open and closed", () => {
    expect(recipientStatusKey("resolved")).toBe("scheduledDMContactFound");
    expect(recipientStatusKey("invalid")).toBe("scheduledDMRecipientInvalid");
    expect(recipientStatusKey("error")).toBe("scheduledDMRecipientError");
    expect(recipientStatusKey("open")).toBe("scheduledDMOpen");
    expect(recipientStatusKey("closed")).toBe("scheduledDMClosed");
    expect(recipientStatusKey("unchecked")).toBe("scheduledDMUnchecked");
  });
});
