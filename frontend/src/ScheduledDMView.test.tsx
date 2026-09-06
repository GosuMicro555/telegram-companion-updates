import { readFileSync } from "node:fs";
import { renderToStaticMarkup } from "react-dom/server";
import { expect, test, vi } from "vitest";
import { shouldShowGlobalAutomationControls } from "./App";
import { ScheduledDMViewContent, createScheduledDMTaskController, reconcilePreflightRecipients } from "./ScheduledDMView";

const accounts = [
  { id: "account-1", displayName: "Anna", phoneMasked: "+7 900 ***-11-22", role: "spammer", status: "ready" },
  { id: "account-2", displayName: "Boris", phoneMasked: "+7 900 ***-33-44", role: "spammer", status: "ready" },
  { id: "account-3", displayName: "Offline", phoneMasked: "+7 900 ***-55-66", role: "spammer", status: "error" }
];

const task = {
  id: "task-1",
  messageText: "Hello there",
  status: "paused",
  startAt: "2026-07-18T10:00:00Z",
  recurrence: "10m",
  maxRuns: 3,
  completedRuns: 0,
  nextRunAt: "2026-07-18T10:00:00Z",
  recipients: [
    { username: "alice", accountId: "account-2", status: "resolved", lastError: "", lastCheckedAt: "2026-07-18T08:00:00Z" },
    { username: "bob", accountId: "account-1", status: "unchecked", lastError: "", lastCheckedAt: "" }
  ],
  accountIds: ["account-1", "account-2"]
};

test("controller calls only the scheduled-DM APIs and refreshes after each task action", async () => {
  const list = vi.fn().mockResolvedValue([task]);
  const save = vi.fn().mockResolvedValue(task);
  const resolve = vi.fn().mockResolvedValue(task.recipients);
  const start = vi.fn().mockResolvedValue(undefined);
  const stop = vi.fn().mockResolvedValue(undefined);
  const cancel = vi.fn().mockResolvedValue(undefined);
  const controller = createScheduledDMTaskController({ list, save, resolve, start, stop, cancel });

  expect(await controller.load()).toEqual([task]);
  await controller.save({ id: "", messageText: "Hello there", recipients: ["alice"], accountIds: ["account-1"], startAt: "2026-07-18T10:00:00Z", recurrence: "once", maxRuns: 1 });
  await controller.resolve("task-1");
  await controller.start("task-1");
  await controller.stop("task-1");
  await controller.cancel("task-1");

  expect(save).toHaveBeenCalledTimes(1);
  expect(resolve).toHaveBeenCalledWith("task-1");
  expect(start).toHaveBeenCalledWith("task-1");
  expect(stop).toHaveBeenCalledWith("task-1");
  expect(cancel).toHaveBeenCalledWith("task-1");
  expect(list).toHaveBeenCalledTimes(6);
});

test("renders the compact editor, selected account identities, preflight state, and task-specific controls", () => {
  const markup = renderToStaticMarkup(<ScheduledDMViewContent
    accounts={accounts}
    draft={{ id: "", contactsText: "@alice\nhttps://t.me/bob", accountIds: ["account-1", "account-2"], messageText: "Hello there", startAt: "2026-07-18T10:00", recurrence: "10m", maxRuns: 3 }}
    error=""
    loading={false}
    locale="en"
    onCancel={() => undefined}
    onDraftChange={() => undefined}
    onResolve={() => undefined}
    onSave={() => undefined}
    onStart={() => undefined}
    onStop={() => undefined}
    tasks={[task]}
  />);

  expect(markup).toContain("Contacts");
  expect(markup).toContain("Anna, +7 900 ***-11-22");
  expect(markup).toContain("Boris, +7 900 ***-33-44");
  expect(markup).toContain("Offline, +7 900 ***-55-66");
  expect(markup).toContain('type="search"');
  expect(markup).toContain("1-12");
  expect(markup).toContain("Message");
  expect(markup).toContain("Scheduled date and time");
  expect(markup).toContain("10 minutes");
  expect(markup).toContain('value="1h">1 hour</option>');
  expect(markup).toContain("Runs");
  expect(markup).toContain("Contact found");
  expect(markup).toContain("Not checked");
  expect(markup).toMatch(/@alice<\/span><span>Boris, \+7 900 \*\*\*-33-44/);
  expect(markup).toContain("Save");
  expect(markup).toContain("Resolve contacts");
  expect(markup).toContain(">START<");
  expect(markup).toContain(">STOP<");
  expect(markup).toContain("Cancel");
  expect(markup).not.toContain("DM open");
  expect(markup).not.toContain("DM closed");
});

test("keeps a selected unavailable spammer account enabled only for unchecking", () => {
  const markup = renderToStaticMarkup(<ScheduledDMViewContent accounts={accounts} draft={{ id: "", contactsText: "", accountIds: ["account-3"], messageText: "", startAt: "", recurrence: "once", maxRuns: 1 }} error="" loading={false} locale="en" onCancel={() => undefined} onDraftChange={() => undefined} onResolve={() => undefined} onSave={() => undefined} onStart={() => undefined} onStop={() => undefined} tasks={[]} />);
  const unselectedMarkup = renderToStaticMarkup(<ScheduledDMViewContent accounts={accounts} draft={{ id: "", contactsText: "", accountIds: [], messageText: "", startAt: "", recurrence: "once", maxRuns: 1 }} error="" loading={false} locale="en" onCancel={() => undefined} onDraftChange={() => undefined} onResolve={() => undefined} onSave={() => undefined} onStart={() => undefined} onStop={() => undefined} tasks={[]} />);

  expect(markup).toMatch(/scheduledDMAccountOption--unavailable"><input(?=[^>]*checked="")(?!(?:[^>]*disabled=""))[^>]*><span>Offline, \+7 900 \*\*\*-55-66<\/span>/);
  expect(unselectedMarkup).toMatch(/scheduledDMAccountOption--unavailable"><input(?=[^>]*disabled="")[^>]*><span>Offline, \+7 900 \*\*\*-55-66<\/span>/);
});

test("disables START for cancelled and completed tasks and pins one-time runs to one", () => {
  for (const status of ["cancelled", "completed"]) {
    const markup = renderToStaticMarkup(<ScheduledDMViewContent
      accounts={accounts}
      draft={{ id: "", contactsText: "@alice", accountIds: ["account-1"], messageText: "Hello there", startAt: "2026-07-18T10:00", recurrence: "once", maxRuns: 3 }}
      error=""
      loading={false}
      locale="en"
      onCancel={() => undefined}
      onDraftChange={() => undefined}
      onResolve={() => undefined}
      onSave={() => undefined}
      onStart={() => undefined}
      onStop={() => undefined}
      tasks={[{ ...task, status }]}
    />);

    expect(markup).toMatch(/<button disabled=""[^>]*>START<\/button>/);
    expect(markup).toMatch(/aria-label="Runs"[^>]*disabled=""[^>]*value="1"/);
  }
});

test("retains preflight recipients across unchanged polling only before real delivery state", () => {
  const uncheckedTask = {
    ...task,
    completedRuns: 0,
    recipients: task.recipients.map((recipient) => ({ ...recipient, status: "unchecked", lastError: "", lastCheckedAt: "" }))
  };
  const preflightRecipients = [
    { ...uncheckedTask.recipients[0], status: "resolved", lastCheckedAt: "2026-07-18T08:00:00Z" },
    { ...uncheckedTask.recipients[1], status: "invalid", lastError: "not found", lastCheckedAt: "2026-07-18T08:00:00Z" }
  ];
  const retained = reconcilePreflightRecipients([uncheckedTask], { [task.id]: preflightRecipients });

  expect(retained).toEqual({
    tasks: [{ ...uncheckedTask, recipients: preflightRecipients }],
    preflight: { [task.id]: preflightRecipients }
  });

  expect(reconcilePreflightRecipients([{ ...uncheckedTask, recipients: uncheckedTask.recipients.slice(0, 1) }], retained.preflight)).toEqual({
    tasks: [{ ...uncheckedTask, recipients: uncheckedTask.recipients.slice(0, 1) }],
    preflight: {}
  });

  expect(reconcilePreflightRecipients([{ ...uncheckedTask, status: "active" }], retained.preflight)).toEqual({
    tasks: [{ ...uncheckedTask, status: "active" }],
    preflight: {}
  });

  expect(reconcilePreflightRecipients([{ ...uncheckedTask, completedRuns: 1 }], retained.preflight)).toEqual({
    tasks: [{ ...uncheckedTask, completedRuns: 1 }],
    preflight: {}
  });
});

test("replaces preflight recipients when polling returns authoritative recipient state", () => {
  const uncheckedTask = {
    ...task,
    recipients: task.recipients.map((recipient) => ({ ...recipient, status: "unchecked", lastError: "", lastCheckedAt: "" }))
  };
  const preflightRecipients = uncheckedTask.recipients.map((recipient) => ({ ...recipient, status: "resolved", lastCheckedAt: "2026-07-18T08:00:00Z" }));

  for (const status of ["open", "closed", "invalid", "error"]) {
    const polled = {
      ...uncheckedTask,
      recipients: uncheckedTask.recipients.map((recipient) => ({ ...recipient, accountId: "account-2", status, lastError: `${status}_from_backend`, lastCheckedAt: "2026-07-18T09:00:00Z" }))
    };
    expect(reconcilePreflightRecipients([polled], { [task.id]: preflightRecipients })).toEqual({ tasks: [polled], preflight: {} });
  }
});

test("renders safe loading, error, and empty task states", () => {
  const loading = renderToStaticMarkup(<ScheduledDMViewContent accounts={accounts} draft={{ id: "", contactsText: "", accountIds: [], messageText: "", startAt: "", recurrence: "once", maxRuns: 1 }} error="" loading locale="en" onCancel={() => undefined} onDraftChange={() => undefined} onResolve={() => undefined} onSave={() => undefined} onStart={() => undefined} onStop={() => undefined} tasks={[]} />);
  const empty = renderToStaticMarkup(<ScheduledDMViewContent accounts={[]} draft={{ id: "", contactsText: "", accountIds: [], messageText: "", startAt: "", recurrence: "once", maxRuns: 1 }} error="" loading={false} locale="en" onCancel={() => undefined} onDraftChange={() => undefined} onResolve={() => undefined} onSave={() => undefined} onStart={() => undefined} onStop={() => undefined} tasks={[]} />);
  const errored = renderToStaticMarkup(<ScheduledDMViewContent accounts={[]} draft={{ id: "", contactsText: "", accountIds: [], messageText: "", startAt: "", recurrence: "once", maxRuns: 1 }} error="Local database unavailable" loading={false} locale="en" onCancel={() => undefined} onDraftChange={() => undefined} onResolve={() => undefined} onSave={() => undefined} onStart={() => undefined} onStop={() => undefined} tasks={[]} />);

  expect(loading).toContain("Loading scheduled messages");
  expect(empty).toContain("No scheduled messages yet.");
  expect(empty).toContain("No eligible connected spammer accounts.");
  expect(errored).toContain('role="alert"');
  expect(errored).toContain("Local database unavailable");
});

test("keeps global automation controls out of the scheduled DM section", () => {
  expect(shouldShowGlobalAutomationControls("scheduledDM")).toBe(false);
  expect(shouldShowGlobalAutomationControls("releaseNotes")).toBe(false);
  expect(shouldShowGlobalAutomationControls("channels")).toBe(true);
});

test("switches the scheduled DM editor to the compact grid before its wide layout can clip", () => {
  const css = readFileSync(new URL("./styles.css", import.meta.url), "utf8");

  expect(css).toMatch(/@media\s*\(max-width:\s*1304px\)[\s\S]*?\.scheduledDMEditor\s*\{[\s\S]*?grid-template-areas:/);
});
