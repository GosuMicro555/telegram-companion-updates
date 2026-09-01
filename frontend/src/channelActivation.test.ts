import { describe, expect, test, vi } from "vitest";
import * as catalogsModule from "./catalogs";
import type { CatalogEntry, CatalogKind } from "./catalogs";
import { canActivateChannel } from "./App";
import * as i18nModule from "./i18n";
import type { Locale } from "./i18n";

const activationConfirmation =
  "\u0412\u044b \u0442\u043e\u0447\u043d\u043e \u0445\u043e\u0442\u0438\u0442\u0435 \u0434\u043e\u0431\u0430\u0432\u0438\u0442\u044c \u0432\u0441\u0435 \u0430\u043a\u043a\u0430\u0443\u043d\u0442\u044b \u0432 \u044d\u0442\u043e\u0442 \u043a\u0430\u043d\u0430\u043b?";

type CatalogEntryInput = Omit<CatalogEntry, "active"> & { active?: boolean };
type ToggleCatalogEntry = (
  catalog: CatalogKind,
  channelID: string,
  active: boolean
) => Promise<CatalogEntry[]>;
type CatalogMembershipAction = (
  catalog: CatalogKind,
  channelID: string
) => Promise<CatalogEntry[]>;
type MembershipCounts = {
  member: number;
  pendingApproval: number;
  joining: number;
  leaving?: number;
  failed: number;
};

type CatalogActivationApi = {
  normalizeCatalogEntry: (row: CatalogEntryInput) => CatalogEntry;
  requestCatalogEntryToggle: (input: {
    catalog: CatalogKind;
    channel: Pick<CatalogEntry, "id" | "active">;
    toggleCatalogEntry: ToggleCatalogEntry;
  }) => Promise<CatalogEntry[] | null>;
  requestCatalogEntryJoin: (input: {
    catalog: CatalogKind;
    channelID: string;
    confirmationMessage: string;
    confirm: (message: string) => boolean | Promise<boolean>;
    joinCatalogEntry: CatalogMembershipAction;
  }) => Promise<CatalogEntry[] | null>;
  requestCatalogEntryLeave: (input: {
    catalog: CatalogKind;
    channelID: string;
    confirmationMessage: string;
    confirm: (message: string) => boolean | Promise<boolean>;
    leaveCatalogEntry: CatalogMembershipAction;
  }) => Promise<CatalogEntry[] | null>;
};

type CatalogI18nApi = {
  formatCatalogMembershipSummary: (locale: Locale, counts: MembershipCounts) => string;
  tCatalogStatus: (locale: Locale, status: string) => string;
};

const catalogApi = catalogsModule as unknown as CatalogActivationApi;
const catalogI18n = i18nModule as unknown as CatalogI18nApi;

function newCatalogEntry(active?: boolean): CatalogEntryInput {
  return {
    id: "new-channel",
    title: "New channel",
    link: "https://t.me/new-channel",
    topic: "General",
    status: "paused",
    messageCount: 0,
    sentCount: 0,
    lastActivity: "-",
    ...(active === undefined ? {} : { active })
  };
}

describe("channel activation", () => {
  test("normalizes a newly added channel as disabled", () => {
    expect(catalogApi.normalizeCatalogEntry).toBeTypeOf("function");

    expect(catalogApi.normalizeCatalogEntry(newCatalogEntry()).active).toBe(false);
  });

  test("uses the approved Russian activation confirmation", () => {
    const ru = i18nModule.messages.ru as Record<string, string>;

    expect(ru.confirmChannelActivation).toBe(activationConfirmation);
  });

  test("toggles processing without asking to join", async () => {
    expect(catalogApi.requestCatalogEntryToggle).toBeTypeOf("function");
    const updated = [{ ...newCatalogEntry(true), active: true }] as CatalogEntry[];
    const toggleCatalogEntry = vi.fn<ToggleCatalogEntry>().mockResolvedValue(updated);

    const result = await catalogApi.requestCatalogEntryToggle({
      catalog: "outbound",
      channel: { id: "new-channel", active: false },
      toggleCatalogEntry
    });

    expect(toggleCatalogEntry).toHaveBeenCalledWith("outbound", "new-channel", true);
    expect(result).toEqual(updated);
  });

  test("joins every account only after confirmation", async () => {
    expect(catalogApi.requestCatalogEntryJoin).toBeTypeOf("function");
    const updated = [{ ...newCatalogEntry(true), active: true }] as CatalogEntry[];
    const confirm = vi.fn(() => true);
    const joinCatalogEntry = vi.fn<CatalogMembershipAction>().mockResolvedValue(updated);

    const result = await catalogApi.requestCatalogEntryJoin({
      catalog: "scout",
      channelID: "new-channel",
      confirmationMessage: activationConfirmation,
      confirm,
      joinCatalogEntry
    });

    expect(confirm).toHaveBeenCalledWith(activationConfirmation);
    expect(joinCatalogEntry).toHaveBeenCalledWith("scout", "new-channel");
    expect(result).toEqual(updated);
  });

  test("does not join when confirmation is cancelled", async () => {
    const joinCatalogEntry = vi.fn<CatalogMembershipAction>();

    const result = await catalogApi.requestCatalogEntryJoin({
      catalog: "outbound",
      channelID: "new-channel",
      confirmationMessage: activationConfirmation,
      confirm: () => false,
      joinCatalogEntry
    });

    expect(joinCatalogEntry).not.toHaveBeenCalled();
    expect(result).toBeNull();
  });

  test("leaves every account only after confirmation", async () => {
    expect(catalogApi.requestCatalogEntryLeave).toBeTypeOf("function");
    const updated = [newCatalogEntry(false) as CatalogEntry];
    const leaveCatalogEntry = vi.fn<CatalogMembershipAction>().mockResolvedValue(updated);

    const result = await catalogApi.requestCatalogEntryLeave({
      catalog: "outbound",
      channelID: "new-channel",
      confirmationMessage: "leave",
      confirm: () => true,
      leaveCatalogEntry
    });

    expect(leaveCatalogEntry).toHaveBeenCalledWith("outbound", "new-channel");
    expect(result).toEqual(updated);
  });

  test("active processing remains available when join interval is invalid", () => {
    expect(canActivateChannel).toBeTypeOf("function");

    expect(canActivateChannel({ active: false }, false)).toBe(true);
    expect(canActivateChannel({ active: false }, true)).toBe(true);
    expect(canActivateChannel({ active: true }, false)).toBe(true);
  });
});

describe("channel membership presentation", () => {
  test.each([
    ["pending_approval", "\u041e\u0436\u0438\u0434\u0430\u0435\u0442 \u043e\u0434\u043e\u0431\u0440\u0435\u043d\u0438\u044f"],
    ["moderation", "\u041d\u0430 \u043c\u043e\u0434\u0435\u0440\u0430\u0446\u0438\u0438"],
    ["partial", "\u0427\u0430\u0441\u0442\u0438\u0447\u043d\u043e"]
  ])("localizes %s in Russian", (status, expected) => {
    expect(catalogI18n.tCatalogStatus).toBeTypeOf("function");

    expect(catalogI18n.tCatalogStatus("ru", status)).toBe(expected);
  });

  test("shows membership counts in a mixed summary", () => {
    expect(catalogI18n.formatCatalogMembershipSummary).toBeTypeOf("function");

    const summary = catalogI18n.formatCatalogMembershipSummary("ru", {
      member: 7,
      pendingApproval: 3,
      joining: 0,
      failed: 0
    });

    expect(summary).toBe("7 \u0430\u043a\u0442\u0438\u0432\u043d\u044b \u00b7 3 \u043d\u0430 \u043c\u043e\u0434\u0435\u0440\u0430\u0446\u0438\u0438");
  });
});
