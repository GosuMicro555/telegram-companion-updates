import { useEffect, useMemo, useRef, useState } from "react";
import { Download, Plus, X } from "lucide-react";
import { driveImportBridge, driveImportText, emptyDriveBatch, parseDriveInput, type DriveImportBatch } from "./driveAccounts";
import type { Locale } from "./i18n";
import "./driveAccounts.css";

export function DriveAccountsImport({ locale }: { locale: Locale }) {
 const text = driveImportText[locale];
 const [open, setOpen] = useState(false);
 const [draft, setDraft] = useState("");
 const [batch, setBatch] = useState<DriveImportBatch>(emptyDriveBatch);
 const [pending, setPending] = useState(false);
 const [error, setError] = useState("");
 const [statusError, setStatusError] = useState("");
 const inFlight = useRef(false);
 const generation = useRef(0);
 useEffect(() => {
  if (!open) return;
  let stopped = false;
  let timer: ReturnType<typeof setTimeout>;
  const poll = async () => {
   const requestedGeneration = generation.current;
   try {
    const next = await driveImportBridge().GetDriveAccountImportStatus();
    if (!stopped && requestedGeneration === generation.current) { setBatch(next); setStatusError(""); }
   } catch { if (!stopped) setStatusError(text.connection); }
   if (!stopped) timer = setTimeout(poll, 750);
  };
  void poll();
  return () => { stopped = true; clearTimeout(timer); };
 }, [open, text.connection]);
 const showError = (value: unknown) => {
  const code = value instanceof Error ? value.message : String(value);
  setError(text.errors[code] ?? text.fallback);
 };
 const start = async () => {
  if (inFlight.current) return;
  inFlight.current = true; generation.current++; setPending(true); setError("");
  try { const next = await driveImportBridge().StartDriveAccountImport(draft); generation.current++; setBatch(next); setDraft(""); }
  catch (err) { showError(err); }
  finally { inFlight.current = false; setPending(false); }
 };
 const cancel = async () => { try { await driveImportBridge().CancelDriveAccountImport(); } catch (err) { showError(err); } };
 const close = () => { if (!pending) { setOpen(false); if (!batch.running) setBatch(emptyDriveBatch); } };
 return <>
  <div className="driveAccountsToolbar"><button className="startButton" type="button" onClick={() => setOpen(true)}><Plus size={17} />{text.title}</button></div>
  {open && <DriveAccountsDialog locale={locale} draft={draft} batch={batch} pending={pending} error={error || statusError} onDraft={setDraft} onClose={close} onStart={() => void start()} onCancel={() => void cancel()} />}
 </>;
}

type DialogProps = { locale: Locale; draft: string; batch: DriveImportBatch; pending: boolean; error: string; onDraft(value: string): void; onClose(): void; onStart(): void; onCancel(): void };
export function DriveAccountsDialog({ locale, draft, batch, pending, error, onDraft, onClose, onStart, onCancel }: DialogProps) {
 const text = driveImportText[locale];
 const input = useMemo(() => parseDriveInput(draft), [draft]);
 const dialog = useRef<HTMLElement>(null);
 const closeRef = useRef(onClose); closeRef.current = onClose;
 useEffect(() => {
  const previous = document.activeElement as HTMLElement | null;
  const node = dialog.current;
  node?.querySelector<HTMLElement>("textarea,button")?.focus();
  const keys = (event: KeyboardEvent) => {
   if (event.key === "Escape") { event.preventDefault(); closeRef.current(); }
   if (event.key !== "Tab" || !node) return;
   const targets = Array.from(node.querySelectorAll<HTMLElement>('button:not(:disabled),textarea:not(:disabled),[tabindex="0"]'));
   const first = targets[0], last = targets[targets.length - 1];
   if (event.shiftKey && (document.activeElement === first || !node.contains(document.activeElement))) { event.preventDefault(); last?.focus(); }
   else if (!event.shiftKey && (document.activeElement === last || !node.contains(document.activeElement))) { event.preventDefault(); first?.focus(); }
  };
  document.addEventListener("keydown", keys);
  return () => { document.removeEventListener("keydown", keys); previous?.focus(); };
 }, []);
 const finished = batch.items.length > 0 && !batch.running;
 const added = batch.items.reduce((n, item) => n + item.added, 0);
 const skipped = batch.items.reduce((n, item) => n + item.skipped, 0);
 return <div className="driveImportBackdrop">
  <section ref={dialog} className="driveImportDialog" role="dialog" aria-modal="true" aria-labelledby="drive-import-title" aria-describedby="drive-import-description">
   <header><h2 id="drive-import-title">{text.title}</h2><button type="button" disabled={pending} aria-label={text.close} onClick={onClose}><X size={20} /></button></header>
   <p id="drive-import-description">{text.description}</p>
   {!batch.running && <label className="driveImportInput">{text.links}<textarea rows={7} maxLength={262144} spellCheck={false} autoComplete="off" disabled={pending} value={draft} onChange={e => onDraft(e.target.value)} placeholder="https://drive.google.com/uc?id=…&export=download" aria-invalid={!!input.error} />
    <span className="driveImportCounter">{input.count} / 100{input.duplicates > 0 && ` · ${text.duplicate}: ${input.duplicates}`}</span>
   </label>}
   {input.error && <p className="driveImportError" role="alert">{text[input.error]}</p>}
   {error && <p className="driveImportError" role="alert">{error}</p>}
   {batch.items.length > 0 && <div className="driveImportResults" role="status" aria-live="polite">
    <p className="driveImportSummary">{finished ? `${text.completed}. ` : ""}{text.added}: {added} · {text.skipped}: {skipped}</p>
    <ol>{batch.items.map(item => <li key={item.ordinal} className={`driveImportRow ${item.phase}`}><span>#{item.ordinal}</span><div><strong>{text.phases[item.phase] ?? text.fallback}</strong>{item.error && <small>{text.errors[item.error] ?? text.fallback}</small>}</div><span>{item.added > 0 ? `+${item.added}` : ""}</span></li>)}</ol>
   </div>}
   <footer><button disabled={pending} type="button" onClick={onClose}>{batch.running ? text.hide : text.close}</button>{batch.running ? <button type="button" onClick={onCancel}>{text.cancel}</button> : <button className="startButton" type="button" disabled={pending || !!input.error || input.links.length === 0} onClick={onStart}><Download size={17} />{text.start}</button>}</footer>
  </section>
 </div>;
}
