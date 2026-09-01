import { CheckCircle2, Pencil, Plus, RefreshCw, Shield, Trash2, X } from "lucide-react";
import { useState } from "react";
import { emptyProxyDraft, proxyEndpointRequired, type ProxyProfileDraft, type ProxyProfileRow } from "./proxyProfiles";

type Labels = {
  title: string; add: string; route: string; endpoint: string; state: string; capacity: string; actions: string;
  edit: string; remove: string; check: string; save: string; cancel: string; name: string; protocol: string;
  host: string; port: string; username: string; password: string; enabled: string; passwordConfigured: string;
  clearPassword: string;
};

export function ProxyProfilesPanel({ rows, labels, busyID, onSave, onDelete, onCheck }: {
  rows: ProxyProfileRow[];
  labels: Labels;
  busyID: string;
  onSave: (draft: ProxyProfileDraft) => Promise<void>;
  onDelete: (id: string) => Promise<void>;
  onCheck: (id: string) => Promise<void>;
}) {
  const [draft, setDraft] = useState<ProxyProfileDraft | null>(null);
  const update = <K extends keyof ProxyProfileDraft>(key: K, value: ProxyProfileDraft[K]) => setDraft((current) => current ? { ...current, [key]: value } : current);
  const edit = (row: ProxyProfileRow) => setDraft({
    ...emptyProxyDraft(), id: row.id, name: row.name, protocol: row.protocol, enabled: row.enabled,
    password: "", clearPassword: false
  });

  return <section className="proxyProfilesSection">
    <header>
      <div><Shield size={17} /><strong>{labels.title}</strong></div>
      <button className="secondaryButton" onClick={() => setDraft(emptyProxyDraft())} type="button"><Plus size={15} />{labels.add}</button>
    </header>
    <div className="proxyProfilesTable">
      <div className="proxyProfilesHead"><span>{labels.route}</span><span>{labels.endpoint}</span><span>{labels.state}</span><span>{labels.capacity}</span><span>{labels.actions}</span></div>
      {rows.map((row) => <div className="proxyProfilesRow" key={row.id}>
        <span><strong>{row.name}</strong><small>{row.protocol.toUpperCase()}{row.passwordConfigured ? ` · ${labels.passwordConfigured}` : ""}</small></span>
        <span>{row.endpoint || "-"}</span>
        <span className={`routeState route-${row.state}`}><i />{row.state}</span>
        <span>{row.usage} / {row.capacity === 0 ? "∞" : row.capacity}</span>
        <span className="proxyRowActions">
          <button disabled={busyID === row.id} onClick={() => void onCheck(row.id)} title={labels.check} type="button"><RefreshCw size={14} /></button>
          {row.id !== "system" && <button onClick={() => edit(row)} title={labels.edit} type="button"><Pencil size={14} /></button>}
          {row.id !== "system" && <button className="dangerIcon" disabled={busyID === row.id} onClick={() => void onDelete(row.id)} title={labels.remove} type="button"><Trash2 size={14} /></button>}
        </span>
      </div>)}
    </div>
    {draft && <div className="modalBackdrop" role="presentation">
      <form className="proxyEditor" onSubmit={(event) => { event.preventDefault(); void onSave(draft).then(() => setDraft(null)); }}>
        <header><strong>{draft.id ? labels.edit : labels.add}</strong><button onClick={() => setDraft(null)} type="button" title={labels.cancel}><X size={16} /></button></header>
        <div className="proxyEditorGrid">
          <label>{labels.name}<input required value={draft.name} onChange={(event) => update("name", event.target.value)} /></label>
          <label>{labels.protocol}<select value={draft.protocol} onChange={(event) => update("protocol", event.target.value as ProxyProfileDraft["protocol"])}><option value="socks5">SOCKS5</option><option value="http">HTTP CONNECT</option></select></label>
          <label>{labels.host}<input required={proxyEndpointRequired(draft)} value={draft.host} onChange={(event) => update("host", event.target.value)} /></label>
          <label>{labels.port}<input min={1} max={65535} required type="number" value={draft.port} onChange={(event) => update("port", Number(event.target.value))} /></label>
          <label>{labels.username}<input autoComplete="off" value={draft.username} onChange={(event) => update("username", event.target.value)} /></label>
          <label>{labels.password}<input autoComplete="new-password" type="password" value={draft.password} onChange={(event) => update("password", event.target.value)} /></label>
          <label className="proxyCheck"><input checked={draft.enabled} onChange={(event) => update("enabled", event.target.checked)} type="checkbox" />{labels.enabled}</label>
          {draft.id && <label className="proxyCheck"><input checked={draft.clearPassword} onChange={(event) => update("clearPassword", event.target.checked)} type="checkbox" />{labels.clearPassword}</label>}
        </div>
        <footer><button className="secondaryButton" onClick={() => setDraft(null)} type="button"><X size={14} />{labels.cancel}</button><button className="primaryButton" type="submit"><CheckCircle2 size={14} />{labels.save}</button></footer>
      </form>
    </div>}
  </section>;
}
