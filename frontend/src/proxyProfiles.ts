export type ProxyProfileRow = {
  id: string;
  name: string;
  protocol: "socks5" | "http";
  endpoint: string;
  state: string;
  usage: number;
  capacity: number;
  lastError: string;
  enabled: boolean;
  passwordConfigured: boolean;
};

export type ProxyProfileDraft = {
  id: string;
  name: string;
  protocol: "socks5" | "http";
  host: string;
  port: number;
  username: string;
  password: string;
  clearPassword: boolean;
  enabled: boolean;
};

export function toProxyProfile(value: unknown): ProxyProfileRow {
  const row = value && typeof value === "object" ? value as Record<string, unknown> : {};
  const protocol = row.protocol === "http" ? "http" : "socks5";
  return {
    id: stringValue(row.id),
    name: stringValue(row.name),
    protocol,
    endpoint: stringValue(row.endpoint),
    state: stringValue(row.state) || "checking",
    usage: finiteNumber(row.usage),
    capacity: typeof row.capacity === "number" && Number.isFinite(row.capacity) ? row.capacity : 10,
    lastError: stringValue(row.lastError),
    enabled: row.enabled !== false,
    passwordConfigured: row.passwordConfigured === true
  };
}

export function proxyRouteUnavailable(profile: ProxyProfileRow, currentRouteID: string): boolean {
  if (profile.id === currentRouteID) return false;
  if (!profile.enabled) return true;
  if (profile.capacity > 0 && profile.usage >= profile.capacity) return true;
  return profile.state !== "ready" && profile.state !== "full";
}

export function emptyProxyDraft(): ProxyProfileDraft {
  return { id: "", name: "", protocol: "socks5", host: "", port: 1080, username: "", password: "", clearPassword: false, enabled: true };
}

export function proxyEndpointRequired(draft: ProxyProfileDraft): boolean {
  return draft.id === "";
}

function stringValue(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function finiteNumber(value: unknown): number {
  const number = typeof value === "number" ? value : Number(value);
  return Number.isFinite(number) ? number : 0;
}
