export type ScheduledDMAccount = {
  id: string;
  displayName: string;
  phoneMasked: string;
  role: string;
  status: string;
};

export type ScheduledDMDraft = {
  recipients: string[];
  accountIds: string[];
  messageText: string;
  startAt: string;
  recurrence: string;
  maxRuns: number;
};

export const recurrenceOptions = [
  { value: "once", labelKey: "scheduledDMOnce" },
  { value: "5m", labelKey: "scheduledDMMinutes" },
  { value: "10m", labelKey: "scheduledDMMinutes" },
  { value: "30m", labelKey: "scheduledDMMinutes" },
  ...Array.from({ length: 12 }, (_, index) => ({ value: `${index + 1}h`, labelKey: "scheduledDMHours" })),
  { value: "daily", labelKey: "scheduledDMDaily" },
  { value: "weekly", labelKey: "scheduledDMWeekly" }
] as const;

const usernamePattern = /^[A-Za-z0-9_]{5,32}$/;

export function normalizeScheduledDMRecipients(value: string): { recipients: string[]; invalid: string[] } {
  const recipients: string[] = [];
  const invalid: string[] = [];
  for (const raw of value.split(/\r?\n/).map((item) => item.trim()).filter(Boolean)) {
    const normalized = normalizeRecipient(raw);
    if (!normalized) {
      invalid.push(raw);
      continue;
    }
    if (!recipients.includes(normalized)) recipients.push(normalized);
  }
  return { recipients, invalid };
}

export function eligibleScheduledDMAccounts(accounts: readonly ScheduledDMAccount[]): ScheduledDMAccount[] {
  return filterScheduledDMSpammerAccounts(accounts, "").filter(isScheduledDMAccountSelectable);
}

export function filterScheduledDMSpammerAccounts(accounts: readonly ScheduledDMAccount[], query: string): ScheduledDMAccount[] {
  const needle = query.trim().toLocaleLowerCase();
  return accounts.filter((account) => {
    if (account.role !== "spammer") return false;
    if (!needle) return true;
    return [account.displayName, account.phoneMasked, account.id, account.status]
      .join(" ")
      .toLocaleLowerCase()
      .includes(needle);
  });
}

export function isScheduledDMAccountSelectable(account: ScheduledDMAccount): boolean {
  return account.role === "spammer" && isConnectedStatus(account.status);
}

export function clampScheduledDMRuns(value: number): number {
  if (!Number.isFinite(value)) return 1;
  return Math.min(12, Math.max(1, Math.trunc(value)));
}

export function recipientAssignments(
  recipients: readonly string[],
  accountIds: readonly string[],
  accounts: readonly ScheduledDMAccount[]
): Array<{ username: string; accountId: string; accountLabel: string }> {
  const byId = new Map(accounts.map((account) => [account.id, account]));
  return recipients.map((username, index) => {
    const accountId = accountIds[index % accountIds.length] ?? "";
    const account = byId.get(accountId);
    return { username, accountId, accountLabel: account ? accountLabel(account) : "-" };
  });
}

export function validateScheduledDMDraft(draft: ScheduledDMDraft, now = new Date()): string[] {
  const errors: string[] = [];
  if (draft.recipients.length < 1 || draft.recipients.length > 3) errors.push("contactsLimit");
  if (draft.accountIds.length < 1 || draft.accountIds.length > 3) errors.push("accountsLimit");
  if (!draft.messageText.trim()) errors.push("messageRequired");
  if (!draft.startAt || Number.isNaN(new Date(draft.startAt).valueOf()) || new Date(draft.startAt).valueOf() <= now.valueOf()) errors.push("futureDateRequired");
  if (!recurrenceOptions.some((option) => option.value === draft.recurrence)) errors.push("recurrenceRequired");
  if (!Number.isInteger(draft.maxRuns) || draft.maxRuns < 1 || draft.maxRuns > 12) errors.push("runsLimit");
  if (draft.recurrence === "once" && draft.maxRuns !== 1 && !errors.includes("runsLimit")) errors.push("runsLimit");
  return errors;
}

export function recipientStatusKey(status: string): string {
  if (status === "resolved") return "scheduledDMContactFound";
  if (status === "invalid") return "scheduledDMRecipientInvalid";
  if (status === "error") return "scheduledDMRecipientError";
  if (status === "open") return "scheduledDMOpen";
  if (status === "closed") return "scheduledDMClosed";
  return "scheduledDMUnchecked";
}

function normalizeRecipient(value: string): string {
  const urlMatch = /^https:\/\/t\.me\/([A-Za-z0-9_]{1,32})$/i.exec(value);
  const candidate = (urlMatch?.[1] ?? value.replace(/^@/, "")).toLowerCase();
  return usernamePattern.test(candidate) ? candidate : "";
}

function isConnectedStatus(status: string): boolean {
  return ["ready", "connected", "active"].includes(status.toLowerCase());
}

function accountLabel(account: ScheduledDMAccount): string {
  return [account.displayName, account.phoneMasked].filter(Boolean).join(", ") || account.id;
}
