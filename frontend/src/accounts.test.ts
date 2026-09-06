import { describe, expect, it } from "vitest";
import { applyAccountRole, applyAccountSnapshot, createLatestAccountRefresh, createQueuedRoleChange, roleOptions, toAccount, type AccountRole, type AccountRow } from "./accounts";

const rows: AccountRow[] = [
  { id: "one", displayName: "", username: "", phoneMasked: "+7 1", role: "spammer", status: "active", proxy: "direct", publicRepliesSent: 1, privateMessagesSent: 2, nextDelivery: "public", privateMessagesClosed: 3, lastActivity: "—", legacyPauseReviewRequired: false },
  { id: "two", displayName: "", username: "", phoneMasked: "+7 2", role: "scout_analyst", status: "paused", proxy: "direct", publicRepliesSent: 0, privateMessagesSent: 0, nextDelivery: "private", privateMessagesClosed: 0, lastActivity: "—", legacyPauseReviewRequired: true }
];

describe("account role reducer", () => {
  it("changes only the selected account role", () => {
    expect(applyAccountRole(rows, "one", "scout_analyst")).toEqual([
      { ...rows[0], role: "scout_analyst" },
      rows[1]
    ]);
  });

  it("provides both role segments with icons", () => {
    expect(roleOptions.map(({ value, icon }) => [value, Boolean(icon)])).toEqual([
      ["spammer", true],
      ["scout_analyst", true]
    ]);
  });

  it("normalizes the next delivery target and closed private count", () => {
    expect(toAccount({ ...rows[0], role: "spammer" })).toMatchObject({ nextDelivery: "public", privateMessagesClosed: 3 });
    expect(toAccount({ ...rows[0], role: "spammer", nextDelivery: "unexpected", privateMessagesClosed: "bad" })).toMatchObject({
      nextDelivery: "private",
      privateMessagesClosed: 0
    });
  });

  it("keeps only the allowlisted permanent session error code", () => {
    expect(toAccount({ ...rows[0], role: "spammer", status: "error", errorCode: "rpc_auth_key_duplicated" }).errorCode)
      .toBe("rpc_auth_key_duplicated");
    expect(toAccount({ ...rows[0], role: "spammer", status: "error", errorCode: "/private/raw backend detail" }).errorCode)
      .toBe("");
  });

  it("keeps an explicitly unassigned proxy route unassigned", () => {
    expect(toAccount({ ...rows[0], role: "spammer", proxyRouteState: "unassigned" }).proxyProfileId).toBe("");
  });

  it("maps an empty ready system route to the assignable system option", () => {
    expect(toAccount({ ...rows[0], role: "spammer", proxyProfileId: "", proxyRouteState: "ready" }).proxyProfileId).toBe("system");
  });

  it("preserves zero capacity for an unlimited system route", () => {
    expect(toAccount({ ...rows[0], role: "spammer", proxyRouteCapacity: 0 }).proxyRouteCapacity).toBe(0);
  });

  it("replaces only the explicitly resumed legacy account", () => {
    const resumed = { ...rows[1], status: "stopped", legacyPauseReviewRequired: false };
    expect(applyAccountSnapshot(rows, resumed)).toEqual([rows[0], resumed]);
  });

  it("serializes same-account requests and completes them in click order", async () => {
    let current = rows;
    let active = 0;
    let maxActive = 0;
    const started: AccountRole[] = [];
    const completed: AccountRole[] = [];
    const requests: Array<ReturnType<typeof deferred<AccountRole>>> = [];
    const changeRole = createQueuedRoleChange(
      async (_id, role) => {
        started.push(role);
        active += 1;
        maxActive = Math.max(maxActive, active);
        const request = deferred<AccountRole>();
        requests.push(request);
        try {
          const committed = await request.promise;
          completed.push(committed);
          return committed;
        } finally {
          active -= 1;
        }
      },
      (id, role) => {
        current = applyAccountRole(current, id, role);
      }
    );

    const first = changeRole("one", "scout_analyst", "spammer");
    const second = changeRole("one", "spammer", "scout_analyst");
    await flushPromises();
    expect(started).toEqual(["scout_analyst"]);

    requests[0].resolve("scout_analyst");
    await first;
    expect(started).toEqual(["scout_analyst", "spammer"]);
    requests[1].resolve("spammer");
    await Promise.all([first, second]);

    expect(maxActive).toBe(1);
    expect(completed).toEqual(["scout_analyst", "spammer"]);
    expect(current[0].role).toBe("spammer");
  });

  it("continues a same-account queue after an earlier request fails", async () => {
    let current = rows;
    const started: AccountRole[] = [];
    const requests: Array<ReturnType<typeof deferred<AccountRole>>> = [];
    const changeRole = createQueuedRoleChange(
      (_id, role) => {
        started.push(role);
        const request = deferred<AccountRole>();
        requests.push(request);
        return request.promise;
      },
      (id, role) => {
        current = applyAccountRole(current, id, role);
      }
    );

    const first = changeRole("one", "scout_analyst", "spammer");
    const second = changeRole("one", "spammer", "scout_analyst");
    await flushPromises();
    requests[0].reject(new Error("first failed"));
    await first;

    expect(started).toEqual(["scout_analyst", "spammer"]);
    requests[1].resolve("spammer");
    await Promise.all([first, second]);
    expect(current[0].role).toBe("spammer");
  });

  it("allows different accounts to mutate concurrently", async () => {
    let active = 0;
    let maxActive = 0;
    const requests = new Map<string, ReturnType<typeof deferred<AccountRole>>>();
    const changeRole = createQueuedRoleChange(async (id) => {
      active += 1;
      maxActive = Math.max(maxActive, active);
      const request = deferred<AccountRole>();
      requests.set(id, request);
      try {
        return await request.promise;
      } finally {
        active -= 1;
      }
    }, () => undefined);

    const first = changeRole("one", "scout_analyst", "spammer");
    const second = changeRole("two", "spammer", "scout_analyst");
    await flushPromises();

    expect(maxActive).toBe(2);
    requests.get("one")!.resolve("scout_analyst");
    requests.get("two")!.resolve("spammer");
    await Promise.all([first, second]);
  });

  it("keeps the newest account snapshot when refreshes resolve out of order", async () => {
    const requests: Array<ReturnType<typeof deferred<AccountRow[]>>> = [];
    const applied: string[] = [];
    const refresh = createLatestAccountRefresh(
      () => {
        const request = deferred<AccountRow[]>();
        requests.push(request);
        return request.promise;
      },
      (snapshot) => applied.push(snapshot[0].status)
    );

    const first = refresh();
    const second = refresh();
    requests[1].resolve([{ ...rows[0], status: "ready" }]);
    await second;
    requests[0].resolve([{ ...rows[0], status: "connecting" }]);
    await first;

    expect(applied).toEqual(["ready"]);
  });
});

async function flushPromises() {
  await Promise.resolve();
  await Promise.resolve();
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
}
