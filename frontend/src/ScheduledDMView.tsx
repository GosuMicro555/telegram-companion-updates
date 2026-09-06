import { useEffect, useMemo, useRef, useState } from "react";
import {
  CancelScheduledDMTask,
  ListScheduledDMTasks,
  ResolveScheduledDMRecipients,
  SaveScheduledDMTask,
  StartScheduledDMTask,
  StopScheduledDMTask
} from "../wailsjs/go/wails/Bindings";
import { DateTimeField } from "./DateTimeField";
import { t, type Locale } from "./i18n";
import {
  clampScheduledDMRuns,
  filterScheduledDMSpammerAccounts,
  isScheduledDMAccountSelectable,
  normalizeScheduledDMRecipients,
  recurrenceOptions,
  recipientAssignments,
  recipientStatusKey,
  validateScheduledDMDraft,
  type ScheduledDMAccount
} from "./scheduledDM";

export type ScheduledDMRecipient = { username: string; accountId: string; status: string; lastError: string; lastCheckedAt: string };
export type ScheduledDMTask = {
  id: string;
  messageText: string;
  status: string;
  startAt: string;
  recurrence: string;
  maxRuns: number;
  completedRuns: number;
  nextRunAt: string;
  recipients: ScheduledDMRecipient[];
  accountIds: string[];
};
export type ScheduledDMEditorDraft = { id: string; contactsText: string; accountIds: string[]; messageText: string; startAt: string; recurrence: string; maxRuns: number };

type ScheduledDMPreflightRecipients = Record<string, ScheduledDMRecipient[]>;

type ScheduledDMTaskControllerDependencies = {
  list(): Promise<ScheduledDMTask[]>;
  save(draft: { id: string; messageText: string; recipients: string[]; accountIds: string[]; startAt: string; recurrence: string; maxRuns: number }): Promise<ScheduledDMTask>;
  resolve(id: string): Promise<ScheduledDMRecipient[]>;
  start(id: string): Promise<void>;
  stop(id: string): Promise<void>;
  cancel(id: string): Promise<void>;
};

export function createScheduledDMTaskController(dependencies: ScheduledDMTaskControllerDependencies) {
  const refresh = () => dependencies.list();
  return {
    load: refresh,
    async save(draft: Parameters<ScheduledDMTaskControllerDependencies["save"]>[0]) { await dependencies.save(draft); return refresh(); },
    async resolve(id: string) { const recipients = await dependencies.resolve(id); const tasks = await refresh(); return { recipients, tasks }; },
    async start(id: string) { await dependencies.start(id); return refresh(); },
    async stop(id: string) { await dependencies.stop(id); return refresh(); },
    async cancel(id: string) { await dependencies.cancel(id); return refresh(); }
  };
}

export function reconcilePreflightRecipients(tasks: readonly ScheduledDMTask[], preflightRecipients: ScheduledDMPreflightRecipients): { tasks: ScheduledDMTask[]; preflight: ScheduledDMPreflightRecipients } {
  const preflight: ScheduledDMPreflightRecipients = {};
  const reconciled = tasks.map((task) => {
    const recipients = preflightRecipients[task.id];
    if (!recipients || !sameRecipientUsernames(task.recipients, recipients) || task.recipients.some((recipient) => recipient.status !== "unchecked") || hasRealDeliveryState(task)) return task;
    preflight[task.id] = recipients;
    return { ...task, recipients };
  });
  return { tasks: reconciled, preflight };
}

export function ScheduledDMView({ accounts, locale }: { accounts: readonly ScheduledDMAccount[]; locale: Locale }) {
  const controller = useMemo(() => createScheduledDMTaskController({
    list: ListScheduledDMTasks,
    save: SaveScheduledDMTask,
    resolve: ResolveScheduledDMRecipients,
    start: StartScheduledDMTask,
    stop: StopScheduledDMTask,
    cancel: CancelScheduledDMTask
  }), []);
  const [tasks, setTasks] = useState<ScheduledDMTask[]>([]);
  const [draft, setDraft] = useState<ScheduledDMEditorDraft>(emptyDraft());
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const preflightRecipients = useRef<ScheduledDMPreflightRecipients>({});

  const applyTaskRefresh = (refreshed: ScheduledDMTask[]) => {
    const reconciled = reconcilePreflightRecipients(refreshed, preflightRecipients.current);
    preflightRecipients.current = reconciled.preflight;
    setTasks(reconciled.tasks);
  };

  const refresh = async () => {
    setLoading(true);
    try {
      applyTaskRefresh(await controller.load());
      setError("");
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void refresh();
    const timer = globalThis.setInterval(() => void refresh(), 5_000);
    return () => globalThis.clearInterval(timer);
  }, []);

  const run = async (action: () => Promise<ScheduledDMTask[]>) => {
    setBusy(true);
    setError("");
    try {
      applyTaskRefresh(await action());
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  };

  const save = () => {
    const normalized = normalizeScheduledDMRecipients(draft.contactsText);
    const validation = [...validateScheduledDMDraft({ ...draft, recipients: normalized.recipients }, new Date()), ...(normalized.invalid.length ? ["contactsInvalid"] : [])];
    if (validation.length) {
      setError(validation.map((key) => t(locale, `scheduledDM${capitalize(key)}` as never)).join(" "));
      return;
    }
    void run(async () => {
      const refreshed = await controller.save({
      id: draft.id,
      messageText: draft.messageText.trim(),
      recipients: normalized.recipients,
      accountIds: draft.accountIds,
      startAt: localDateTimeToRFC3339(draft.startAt),
      recurrence: draft.recurrence,
      maxRuns: draft.recurrence === "once" ? 1 : draft.maxRuns
      });
      preflightRecipients.current = {};
      return refreshed;
    });
  };

  const resolve = (id: string) => {
    setBusy(true);
    setError("");
    void controller.resolve(id).then(({ recipients, tasks: refreshed }) => {
      const reconciled = reconcilePreflightRecipients(refreshed, { ...preflightRecipients.current, [id]: recipients });
      preflightRecipients.current = reconciled.preflight;
      setTasks(reconciled.tasks);
    }).catch((cause) => setError(errorMessage(cause))).finally(() => setBusy(false));
  };

  return <ScheduledDMViewContent
    accounts={accounts}
    busy={busy}
    draft={draft}
    error={error}
    loading={loading}
    locale={locale}
    onCancel={(id) => void run(() => controller.cancel(id))}
    onDraftChange={setDraft}
    onResolve={resolve}
    onSave={save}
    onStart={(id) => void run(() => controller.start(id))}
    onStop={(id) => void run(() => controller.stop(id))}
    tasks={tasks}
  />;
}

type ScheduledDMViewContentProps = {
  accounts: readonly ScheduledDMAccount[];
  busy?: boolean;
  draft: ScheduledDMEditorDraft;
  error: string;
  loading: boolean;
  locale: Locale;
  onCancel(id: string): void;
  onDraftChange(draft: ScheduledDMEditorDraft): void;
  onResolve(id: string): void;
  onSave(): void;
  onStart(id: string): void;
  onStop(id: string): void;
  tasks: readonly ScheduledDMTask[];
};

export function ScheduledDMViewContent(props: ScheduledDMViewContentProps) {
  const [accountSearch, setAccountSearch] = useState("");
  const visibleSpammerAccounts = filterScheduledDMSpammerAccounts(props.accounts, accountSearch);
  const normalized = normalizeScheduledDMRecipients(props.draft.contactsText);
  const update = (change: Partial<ScheduledDMEditorDraft>) => props.onDraftChange({ ...props.draft, ...change });

  return <section aria-label={t(props.locale, "scheduledDM")} className="scheduledDMWorkspace">
    {props.error && <div className="errorBanner" role="alert">{props.error}</div>}
    <div className="scheduledDMEditor">
      <label className="scheduledDMField scheduledDMField--contacts">{t(props.locale, "scheduledDMContacts")}
        <textarea aria-label={t(props.locale, "scheduledDMContacts")} onChange={(event) => update({ contactsText: event.target.value })} placeholder={t(props.locale, "scheduledDMContactsPlaceholder")} rows={3} value={props.draft.contactsText} />
      </label>
      <label className="scheduledDMField scheduledDMField--message">{t(props.locale, "scheduledDMMessage")}
        <textarea aria-label={t(props.locale, "scheduledDMMessage")} onChange={(event) => update({ messageText: event.target.value })} rows={3} value={props.draft.messageText} />
      </label>
      <fieldset className="scheduledDMField scheduledDMField--accounts">
        <legend>{t(props.locale, "scheduledDMAccounts")}</legend>
        <input aria-label={t(props.locale, "scheduledDMAccountSearch")} className="scheduledDMAccountSearch" onChange={(event) => setAccountSearch(event.target.value)} placeholder={t(props.locale, "scheduledDMAccountSearch")} type="search" value={accountSearch} />
        <div className="scheduledDMAccountList">
          {visibleSpammerAccounts.length === 0 && <p className="scheduledDMHint">{t(props.locale, "scheduledDMNoEligibleAccounts")}</p>}
          {visibleSpammerAccounts.map((account) => {
            const selectable = isScheduledDMAccountSelectable(account);
            const selected = props.draft.accountIds.includes(account.id);
            return <label className={`scheduledDMAccountOption${selectable ? "" : " scheduledDMAccountOption--unavailable"}`} key={account.id}>
              <input checked={selected} disabled={!selected && (!selectable || props.draft.accountIds.length >= 3)} onChange={() => update({ accountIds: toggleAccount(props.draft.accountIds, account.id) })} type="checkbox" />
              <span>{accountDisplay(account)}</span>
              <small>{account.status || t(props.locale, "scheduledDMUnavailable")}</small>
            </label>;
          })}
        </div>
        <span className="scheduledDMHint">{t(props.locale, "scheduledDMAccountSelectionHint")}</span>
      </fieldset>
      <div className="scheduledDMField scheduledDMField--date"><DateTimeField label={t(props.locale, "scheduledDMDateTime")} locale={props.locale} min={minimumDateTime()} onChange={(startAt) => update({ startAt })} value={props.draft.startAt} /></div>
      <label className="scheduledDMField scheduledDMField--recurrence">{t(props.locale, "scheduledDMRecurrence")}
        <select aria-label={t(props.locale, "scheduledDMRecurrence")} onChange={(event) => update({ recurrence: event.target.value, ...(event.target.value === "once" ? { maxRuns: 1 } : {}) })} value={props.draft.recurrence}>
          {recurrenceOptions.map((option) => <option key={option.value} value={option.value}>{recurrenceLabel(option.value, props.locale)}</option>)}
        </select>
      </label>
      <label className="scheduledDMField scheduledDMField--runs">{t(props.locale, "scheduledDMRuns")} <small>{t(props.locale, "scheduledDMRunsHint")}</small>
        <input aria-label={t(props.locale, "scheduledDMRuns")} disabled={props.draft.recurrence === "once"} max="12" min="1" onChange={(event) => update({ maxRuns: clampScheduledDMRuns(Number(event.target.value)) })} type="number" value={props.draft.recurrence === "once" ? 1 : clampScheduledDMRuns(props.draft.maxRuns)} />
      </label>
      <div className="scheduledDMActions"><button className="startButton" disabled={props.busy} onClick={props.onSave} type="button">{t(props.locale, "scheduledDMSave")}</button></div>
    </div>

    <div className="scheduledDMTable" role="table" aria-label={t(props.locale, "scheduledDMTasks")}>
      <div className="scheduledDMTable__header" role="row"><span>{t(props.locale, "scheduledDMContact")}</span><span>{t(props.locale, "scheduledDMAccount")}</span><span>{t(props.locale, "scheduledDMStatus")}</span><span>{t(props.locale, "scheduledDMLastCheck")}</span><span>{t(props.locale, "scheduledDMActions")}</span></div>
      {props.loading && <div className="scheduledDMTable__empty" role="status">{t(props.locale, "scheduledDMLoading")}</div>}
      {!props.loading && props.tasks.length === 0 && <div className="scheduledDMTable__empty">{t(props.locale, "scheduledDMEmpty")}</div>}
      {props.tasks.flatMap((task) => recipientAssignments(task.recipients.map((recipient) => recipient.username), task.recipients.map((recipient, index) => recipient.accountId || task.accountIds[index % task.accountIds.length]), props.accounts).map((assignment, index) => {
        const recipient = task.recipients[index];
        return <div className="scheduledDMTable__row" key={`${task.id}:${assignment.username}`} role="row">
          <span>@{assignment.username}</span><span>{assignment.accountLabel}</span><span className={`scheduledDMStatus scheduledDMStatus--${recipient.status}`}>{t(props.locale, recipientStatusKey(recipient.status) as never)}</span><time dateTime={recipient.lastCheckedAt}>{recipient.lastCheckedAt ? displayDate(recipient.lastCheckedAt, props.locale) : "-"}</time>
          {index === 0 ? <span className="scheduledDMRowActions"><button disabled={props.busy || !isScheduledDMTaskStartable(task.status)} onClick={() => props.onStart(task.id)} type="button">START</button><button disabled={props.busy || task.status !== "active"} onClick={() => props.onStop(task.id)} type="button">STOP</button><button disabled={props.busy || task.status === "cancelled"} onClick={() => props.onCancel(task.id)} type="button">{t(props.locale, "scheduledDMCancel")}</button><button disabled={props.busy} onClick={() => props.onResolve(task.id)} type="button">{t(props.locale, "scheduledDMResolve")}</button></span> : <span />}
        </div>;
      }))}
    </div>
  </section>;
}

function emptyDraft(): ScheduledDMEditorDraft { return { id: "", contactsText: "", accountIds: [], messageText: "", startAt: "", recurrence: "once", maxRuns: 1 }; }
function sameRecipientUsernames(left: readonly ScheduledDMRecipient[], right: readonly ScheduledDMRecipient[]): boolean { return left.length === right.length && left.every((recipient, index) => recipient.username === right[index].username); }
function hasRealDeliveryState(task: ScheduledDMTask): boolean { return task.completedRuns > 0 || ["active", "completed", "error"].includes(task.status); }
function isScheduledDMTaskStartable(status: string): boolean { return !["active", "cancelled", "completed"].includes(status); }
function toggleAccount(ids: readonly string[], id: string): string[] { return ids.includes(id) ? ids.filter((item) => item !== id) : [...ids, id]; }
function accountDisplay(account: ScheduledDMAccount): string { return [account.displayName, account.phoneMasked].filter(Boolean).join(", ") || account.id; }
function localDateTimeToRFC3339(value: string): string { return new Date(value).toISOString(); }
function minimumDateTime(): string { const date = new Date(); date.setMinutes(date.getMinutes() + 1); return localDateTime(date); }
function localDateTime(date: Date): string { const offset = date.getTimezoneOffset() * 60_000; return new Date(date.valueOf() - offset).toISOString().slice(0, 16); }
function recurrenceLabel(value: string, locale: Locale): string { if (value.endsWith("m")) return `${value.slice(0, -1)} ${t(locale, value === "1m" ? "scheduledDMMinute" : "scheduledDMMinutes")}`; if (value.endsWith("h")) return `${value.slice(0, -1)} ${t(locale, value === "1h" ? "scheduledDMHour" : "scheduledDMHours")}`; if (value === "daily") return t(locale, "scheduledDMDaily"); if (value === "weekly") return t(locale, "scheduledDMWeekly"); return t(locale, "scheduledDMOnce"); }
function displayDate(value: string, locale: Locale): string { const date = new Date(value); return Number.isNaN(date.valueOf()) ? "-" : date.toLocaleString(locale === "ru" ? "ru-RU" : "en-US"); }
function errorMessage(cause: unknown): string { return cause instanceof Error ? cause.message : String(cause); }
function capitalize(value: string): string { return value.slice(0, 1).toUpperCase() + value.slice(1); }
