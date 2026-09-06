import { Bot, Telescope, type LucideIcon } from "lucide-react";

export type AccountRole = "spammer" | "scout_analyst";
export type DeliveryTarget = "private" | "public";

export type AccountRow = {
  id: string;
  displayName: string;
  username: string;
  phoneMasked: string;
  role: AccountRole;
  status: string;
  errorCode?: string;
  proxy: string;
  proxyWarning?: string;
  proxyProfileId?: string;
  proxyRouteName?: string;
  proxyRouteState?: string;
  proxyRouteUsage?: number;
  proxyRouteCapacity?: number;
  publicRepliesSent: number;
  privateMessagesSent: number;
  nextDelivery: DeliveryTarget;
  privateMessagesClosed: number;
  lastActivity: string;
  legacyPauseReviewRequired: boolean;
};

export function toAccount(account: {
  id: string; displayName: string; username: string; phoneMasked: string; role: string; status: string; errorCode?: unknown;
  proxy: string; proxyWarning?: unknown; publicRepliesSent: number; privateMessagesSent: number; lastActivity: string;
  proxyProfileId?: unknown; proxyRouteName?: unknown; proxyRouteState?: unknown; proxyRouteUsage?: unknown; proxyRouteCapacity?: unknown;
  legacyPauseReviewRequired: boolean; nextDelivery?: unknown; privateMessagesClosed?: unknown;
}): AccountRow {
  const proxyProfileID = typeof account.proxyProfileId === "string" ? account.proxyProfileId.trim() : "";
  const proxyRouteState = typeof account.proxyRouteState === "string" ? account.proxyRouteState : "";
  return {
    ...account,
    errorCode: account.errorCode === "rpc_auth_key_duplicated" ? account.errorCode : "",
    proxyWarning: typeof account.proxyWarning === "string" ? account.proxyWarning : "",
    proxyProfileId: proxyProfileID || (proxyRouteState === "unassigned" ? "" : "system"),
    proxyRouteName: typeof account.proxyRouteName === "string" ? account.proxyRouteName : account.proxy,
    proxyRouteState,
    proxyRouteUsage: finiteNumber(account.proxyRouteUsage),
    proxyRouteCapacity: typeof account.proxyRouteCapacity === "number" && Number.isFinite(account.proxyRouteCapacity)
      ? account.proxyRouteCapacity
      : 10,
    role: account.role as AccountRole,
    nextDelivery: account.nextDelivery === "public" ? "public" : "private",
    privateMessagesClosed: typeof account.privateMessagesClosed === "number" && Number.isFinite(account.privateMessagesClosed)
      ? account.privateMessagesClosed
      : 0
  };
}

function finiteNumber(value: unknown): number {
  const number = typeof value === "number" ? value : Number(value);
  return Number.isFinite(number) ? number : 0;
}

export const roleOptions: Array<{ value: AccountRole; icon: LucideIcon }> = [
  { value: "spammer", icon: Bot },
  { value: "scout_analyst", icon: Telescope }
];

export function applyAccountRole(rows: AccountRow[], id: string, role: AccountRole): AccountRow[] {
  return rows.map((row) => row.id === id ? { ...row, role } : row);
}

export function applyAccountSnapshot(rows: AccountRow[], updated: AccountRow): AccountRow[] {
  return rows.map((row) => row.id === updated.id ? updated : row);
}

export function createLatestAccountRefresh<T>(
  request: () => Promise<T>,
  apply: (snapshot: T) => void,
  onError: (error: unknown) => void = () => undefined
) {
  let latestRequestID = 0;

  return async (): Promise<void> => {
    const requestID = ++latestRequestID;
    try {
      const snapshot = await request();
      if (requestID === latestRequestID) apply(snapshot);
    } catch (error) {
      if (requestID === latestRequestID) onError(error);
    }
  };
}

export function createQueuedRoleChange(
  request: (id: string, role: AccountRole) => Promise<AccountRole>,
  apply: (id: string, role: AccountRole) => void,
  onError: (error: unknown) => void = () => undefined
) {
  let sequence = 0;
  const queues = new Map<string, {
    tail: Promise<void>;
    latestRequestID: number;
    committedRole: AccountRole;
  }>();

  return (id: string, role: AccountRole, previousRole: AccountRole): Promise<void> => {
    const requestID = ++sequence;
    let queue = queues.get(id);
    if (!queue) {
      queue = { tail: Promise.resolve(), latestRequestID: requestID, committedRole: previousRole };
      queues.set(id, queue);
    }
    queue.latestRequestID = requestID;
    apply(id, role);

    const execute = async () => {
      try {
        const committedRole = await request(id, role);
        queue.committedRole = committedRole;
        if (queue.latestRequestID === requestID) apply(id, committedRole);
      } catch (error) {
        if (queue.latestRequestID !== requestID) return;
        apply(id, queue.committedRole);
        onError(error);
      }
    };
    const operation = queue.tail.then(execute, execute);
    queue.tail = operation;
    void operation.then(() => {
      if (queues.get(id) === queue && queue.tail === operation) queues.delete(id);
    });
    return operation;
  };
}

export function accountReplyTotal(rows: ReadonlyArray<AccountRow>): number {
  return rows.reduce((sum, row) => sum + row.publicRepliesSent + row.privateMessagesSent, 0);
}
