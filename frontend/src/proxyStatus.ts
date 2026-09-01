import { t, type Locale } from "./i18n";

export type ManagedProxyStatus = {
  mode: string;
  state: string;
  address: string;
  transport: string;
  lastError: string;
  restartCount: number;
  updatedAt: string;
  autoRestart: boolean;
};

export function proxyStatusLabel(locale: Locale, state: string): string {
  const key = ({
    stopped: "proxyStopped",
    starting: "proxyStarting",
    ready: "proxyReady",
    degraded: "proxyDegraded",
    error: "proxyError"
  } as const)[state] ?? "proxyError";
  return t(locale, key);
}

/**
 * Return a localized, stable name for the managed route.  The route id is
 * deliberately supplied separately so the compatibility value `system` can
 * be translated without reflecting a backend display string into the UI.
 */
export function proxyRouteLabel(locale: Locale, routeID: string, fallbackName: string): string {
  return routeID.trim() === "system" ? t(locale, "managedProxy") : fallbackName;
}

/**
 * Map the sanitized transport enum to a user-facing label. Unknown values are
 * intentionally collapsed to a generic label rather than echoed (bridge
 * lines, endpoints, and other transport material must never reach the UI).
 */
export function proxyTransportLabel(locale: Locale, transport: string): string {
  switch (transport.trim().toLowerCase()) {
    case "obfs4":
      return t(locale, "proxyTransportObfs4");
    case "snowflake":
      return t(locale, "proxyTransportSnowflake");
    default:
      return t(locale, "proxyTransportUnknown");
  }
}

const managedProxyErrorKeys = {
  managed_proxy_unavailable: "proxyManagedUnavailable",
  bridge_config_invalid: "proxyBridgeConfigInvalid",
  all_candidates_exhausted: "proxyAllCandidatesExhausted"
} as const;

export function proxyStatusErrorLabel(locale: Locale, errorCode: string): string {
  const key = managedProxyErrorKeys[errorCode as keyof typeof managedProxyErrorKeys];
  return key ? t(locale, key) : "";
}
