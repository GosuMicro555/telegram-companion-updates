import { useEffect, useState } from "react";
import { GetAppSettings, ListAccountRests, SaveAppSettings } from "../wailsjs/go/wails/Bindings";
import { formatAccountRestCountdown, formatAccountRestDate, isResting, normalizeAccountRests, type AccountRestRow } from "./accountRest";
import { t, type Locale } from "./i18n";

export function AccountRestView({ locale }: { locale: Locale }) {
  const [rows, setRows] = useState<AccountRestRow[]>([]);
  const [error, setError] = useState("");
  const [now, setNow] = useState(() => new Date());
  const [enabled, setEnabled] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    let canceled = false;
    void GetAppSettings().then((settings) => {
      if (!canceled) {
        setEnabled(settings.groupRestEnabled);
        setLoaded(true);
      }
    }).catch((cause) => {
      if (!canceled) setError(cause instanceof Error ? cause.message : String(cause));
    });
    return () => { canceled = true; };
  }, []);
  useEffect(() => {
    if (!shouldPollAccountRests(loaded, enabled)) {
      setRows([]);
      return;
    }
    return createAccountRestPolling(ListAccountRests, setRows, setError, () => setNow(new Date()));
  }, [enabled, loaded]);

  const toggle = async () => {
    if (saving) return;
    setSaving(true);
    try {
      const settings = await GetAppSettings();
      const saved = await SaveAppSettings({ ...settings, groupRestEnabled: !settings.groupRestEnabled, revision: 0 });
      setEnabled(saved.groupRestEnabled);
      if (!saved.groupRestEnabled) setRows([]);
      setError("");
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setSaving(false);
    }
  };

  return <AccountRestViewContent enabled={enabled} error={error} loaded={loaded} locale={locale} now={now} onToggle={() => void toggle()} rows={rows} saving={saving} />;
}

export function shouldPollAccountRests(loaded: boolean, enabled: boolean): boolean {
  return loaded && enabled;
}

export function createAccountRestPolling(load: () => Promise<unknown>, onRows: (rows: AccountRestRow[]) => void, onError: (message: string) => void, onTick: () => void): () => void {
  let canceled = false;
  const refresh = async () => {
    try {
      const rows = normalizeAccountRests(await load(), new Date());
      if (!canceled) { onRows(rows); onError(""); }
    } catch (cause) {
      if (!canceled) onError(cause instanceof Error ? cause.message : String(cause));
    }
  };
  void refresh();
  const backendTimer = globalThis.setInterval(() => void refresh(), 30_000);
  const countdownTimer = globalThis.setInterval(onTick, 1_000);
  return () => { canceled = true; globalThis.clearInterval(backendTimer); globalThis.clearInterval(countdownTimer); };
}

export function AccountRestViewContent({ enabled, error, loaded, locale, now, onToggle, rows, saving }: { enabled: boolean; error: string; loaded: boolean; locale: Locale; now: Date; onToggle: () => void; rows: readonly AccountRestRow[]; saving: boolean }) {
  const visibleRows = loaded && enabled ? rows : [];
  return <section className="accountRestWorkspace" aria-label={t(locale, "accountRest")}>
    {error && <div className="errorBanner" role="alert">{error}</div>}
    <div className="accountRestToolbar">
      <span>{t(locale, "accountRest")}</span>
      <button aria-label={t(locale, "accountRest")} aria-pressed={enabled} className={enabled ? "toggle on" : "toggle"} disabled={saving || !loaded} onClick={onToggle} type="button" />
      <strong className={enabled ? "accountRestToggleState accountRestToggleState--enabled" : "accountRestToggleState"}>{t(locale, enabled ? "enabled" : "disabled")}</strong>
    </div>
    <div className="accountRestTable" role="table" aria-label={t(locale, "accountRestTable")}>
      <div className="accountRestTable__header" role="row">
        <span>{t(locale, "accountRestAccount")}</span><span>{t(locale, "accountRestChannel")}</span><span>{t(locale, "accountRestCatalog")}</span><span>{t(locale, "accountRestStatus")}</span>
        <span>{t(locale, "accountRestStarted")}</span><span>{t(locale, "accountRestEnds")}</span><span>{t(locale, "accountRestRemaining")}</span><span>{t(locale, "accountRestTotalHours")}</span>
      </div>
      {visibleRows.length === 0 && <div className="accountRestTable__empty">{t(locale, "accountRestEmpty")}</div>}
      {visibleRows.map((row) => {
        const status = isResting(row.until, now) ? "resting" : "ready";
        return <div className="accountRestTable__row" key={row.key} role="row">
          <strong>{row.accountTitle || row.accountID || "-"}</strong><span>{row.channelTitle || row.channelID || "-"}</span><span>{row.catalog || "-"}</span>
          <span className={`accountRestStatus accountRestStatus--${status}`}>{t(locale, status === "resting" ? "accountResting" : "accountReady")}</span>
          <time dateTime={row.startedAt}>{formatAccountRestDate(row.startedAt, locale)}</time><time dateTime={row.until}>{formatAccountRestDate(row.until, locale)}</time>
          <span>{formatAccountRestCountdown(row.until, now, locale)}</span><span>{row.durationHours}</span>
        </div>;
      })}
    </div>
  </section>;
}
