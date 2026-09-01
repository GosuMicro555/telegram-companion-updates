export type CatalogKind = "outbound" | "scout";

export type CatalogEntry = {
  id: string;
  title: string;
  link: string;
  topic: string;
  status: string;
  messageCount: number;
  sentCount: number;
  lastActivity: string;
  active: boolean;
  member?: number;
  pendingApproval?: number;
  joining?: number;
  leaving?: number;
  failed?: number;
};

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

type DeleteCatalogEntries = (
  catalog: CatalogKind,
  channelIDs: string[]
) => Promise<CatalogEntry[]>;

export function normalizeCatalogEntry(row: CatalogEntryInput): CatalogEntry {
  return {
    ...row,
    active: row.active ?? false,
    member: row.member ?? 0,
    pendingApproval: row.pendingApproval ?? 0,
    joining: row.joining ?? 0,
    leaving: row.leaving ?? 0,
    failed: row.failed ?? 0
  };
}

export async function requestCatalogEntryToggle(input: {
  catalog: CatalogKind;
  channel: Pick<CatalogEntry, "id" | "active">;
  toggleCatalogEntry: ToggleCatalogEntry;
}): Promise<CatalogEntry[] | null> {
  return input.toggleCatalogEntry(input.catalog, input.channel.id, !input.channel.active);
}

export function requestCatalogEntryJoin(input: {
  catalog: CatalogKind;
  channelID: string;
  confirmationMessage: string;
  confirm: (message: string) => boolean | Promise<boolean>;
  joinCatalogEntry: CatalogMembershipAction;
}): Promise<CatalogEntry[] | null> {
  return requestConfirmedMembershipAction(input, input.joinCatalogEntry);
}

export function requestCatalogEntryLeave(input: {
  catalog: CatalogKind;
  channelID: string;
  confirmationMessage: string;
  confirm: (message: string) => boolean | Promise<boolean>;
  leaveCatalogEntry: CatalogMembershipAction;
}): Promise<CatalogEntry[] | null> {
  return requestConfirmedMembershipAction(input, input.leaveCatalogEntry);
}

export async function requestCatalogEntriesDeletion(input: {
  catalog: CatalogKind;
  channelIDs: string[];
  confirmationMessage: string;
  confirm: (message: string) => boolean | Promise<boolean>;
  deleteCatalogEntries: DeleteCatalogEntries;
}): Promise<CatalogEntry[] | null> {
  if (!(await input.confirm(input.confirmationMessage))) return null;
  return input.deleteCatalogEntries(input.catalog, input.channelIDs);
}

export function createCatalogRemovalPolling(input: {
  rows: readonly Pick<CatalogEntry, "status">[];
  load: () => Promise<CatalogEntry[]>;
  onRows: (rows: CatalogEntry[]) => void;
  onError: (message: string) => void;
}): () => void {
  if (!input.rows.some((row) => row.status === "removing")) return () => undefined;

  let canceled = false;
  let timer: ReturnType<typeof globalThis.setTimeout> | undefined;
  const schedule = () => {
    if (canceled) return;
    timer = globalThis.setTimeout(() => {
      timer = undefined;
      void refresh();
    }, 2_000);
  };
  const refresh = async () => {
    try {
      const rows = await input.load();
      if (!canceled) input.onRows(rows);
    } catch (cause) {
      if (!canceled) input.onError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      schedule();
    }
  };
  schedule();
  return () => {
    canceled = true;
    if (timer !== undefined) globalThis.clearTimeout(timer);
  };
}

export function uniqueTelegramLinks(links: string[]): string[] {
  const seen = new Set<string>();
  const result: string[] = [];
  for (const raw of links) {
    const link = normalizeTelegramLink(raw);
    if (!link) continue;
    const key = link.toLocaleLowerCase("ru-RU");
    if (seen.has(key)) continue;
    seen.add(key);
    result.push(link);
  }
  return result;
}

export function applyBulkTopic(
  links: string[],
  topic: string
): { links: string[]; topic: string; error?: "topic_required" } {
  const cleaned = topic.trim();
  if (!cleaned) return { links: [], topic: "", error: "topic_required" };
  return { links: uniqueTelegramLinks(links), topic: cleaned };
}

export function existingTopics(topics: string[]): string[] {
  const seen = new Set<string>();
  const result: string[] = [];
  for (const raw of topics) {
    const topic = raw.trim();
    const key = topic.toLocaleLowerCase("ru-RU");
    if (!topic || seen.has(key)) continue;
    seen.add(key);
    result.push(topic);
  }
  return result;
}

export function toggleSelected(selected: ReadonlySet<string>, id: string): Set<string> {
  const next = new Set(selected);
  if (next.has(id)) next.delete(id);
  else next.add(id);
  return next;
}

async function requestConfirmedMembershipAction(
  input: {
    catalog: CatalogKind;
    channelID: string;
    confirmationMessage: string;
    confirm: (message: string) => boolean | Promise<boolean>;
  },
  action: CatalogMembershipAction
): Promise<CatalogEntry[] | null> {
  if (!(await input.confirm(input.confirmationMessage))) return null;
  return action(input.catalog, input.channelID);
}

function normalizeTelegramLink(raw: string): string | null {
  let value = raw.trim().replace(/^['"]|['"]$/g, "").replace(/\/+$/, "");
  if (!value) return null;
  if (value.startsWith("@")) value = `https://t.me/${value.slice(1)}`;
  else if (!value.includes("://")) value = `https://${value}`;
  try {
    const parsed = new URL(value);
    if (parsed.hostname.toLocaleLowerCase("en-US").replace(/^www\./, "") !== "t.me") return null;
    const path = parsed.pathname.replace(/^\/+|\/+$/g, "");
    if (!path) return null;
    const discussion = /^tc-discussion=([0-9]+)$/.exec(parsed.hash.slice(1));
    const fragment = discussion ? `#tc-discussion=${discussion[1]}` : "";
    return `https://t.me/${path}${fragment}`;
  } catch {
    return null;
  }
}
