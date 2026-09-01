import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type Dispatch, type SetStateAction } from "react";
import { BarChart3, Bot, Check, FolderSearch, Hash, Hourglass, LogIn, LogOut, MessageCircle, Pencil, Plus, RadioTower, Search, Settings, ShieldCheck, Sparkles, Trash2, X } from "lucide-react";
import * as models from "../wailsjs/go/models";
import {
	AddCatalogLinks,
	AddCanonicalForm,
	BulkImportCanonicalKeywords,
	AssignAccountProxy,
	CheckProxyProfile,
	DeleteProxyProfile,
	DeleteCatalogEntries,
	GetAppSettings,
	GetAccounts,
	GetCatalog,
  GetProxyStatus,
  GetDashboard,
	GetKeywordSettings,
	GetLiveDeliveryStatistics,
	JoinCatalogEntry,
	LeaveCatalogEntry,
	ListProxyProfiles,
	ListCanonicalKeywords,
	MoveCanonicalForm,
	RemoveCanonicalForm,
	SaveAppSettings,
	SaveProxyProfile,
	SaveKeywordSettings,
	ResumeLegacyPausedAccount,
	SetAccountRole,
	SetChannelTopics,
  StartAutomation,
  StopAutomation,
	ToggleCatalogEntry,
} from "../wailsjs/go/wails/Bindings";
import { formatCatalogMembershipSummary, persistLocale, readStoredLocale, t, tAccountRuntimeHint, tAccountRuntimeStatus, tBackendStatus, tCatalogStatus, type Locale, type MessageKey } from "./i18n";
import { readAutomationRunning, startAutomationAfterSavingKeywordSettings } from "./automation";
import {
  directMessageKeywordsFromRows,
  filterKeywords,
  isKeywordOperatorExpression,
  keywordRowsFromSettings,
  mergeKeywords,
  parseKeywordExpressionLines,
  type KeywordRow
} from "./keywords";
import { KeywordTable } from "./KeywordTable";
import {
  AnalyticsView,
  parseBulkCanonicalValues,
  type CanonicalImportEntry,
  type CanonicalImportResult
} from "./AnalyticsView";
import { applyAccountRole, applyAccountSnapshot, createLatestAccountRefresh, createQueuedRoleChange, roleOptions, toAccount, type AccountRole, type AccountRow } from "./accounts";
import { applyBulkTopic, createCatalogRemovalPolling, existingTopics, normalizeCatalogEntry, requestCatalogEntriesDeletion, requestCatalogEntryJoin, requestCatalogEntryLeave, requestCatalogEntryToggle, toggleSelected, type CatalogEntry, type CatalogKind } from "./catalogs";
import { autoScale, installScaleShortcuts, readStoredScale, type UIScale } from "./uiScale";
import { WindowGetSize } from "../wailsjs/runtime/runtime";
import { proxyRouteLabel, proxyStatusErrorLabel, proxyStatusLabel, proxyTransportLabel, type ManagedProxyStatus } from "./proxyStatus";
import telegramTurquoiseLogo from "./assets/telegram-turquoise.svg";
import { BUILD_TIMESTAMP_LABEL } from "./buildTimestamp";
import { APP_VERSION_LABEL } from "./version";
import { currentReleaseNotes, markReleaseNotesSeen, RELEASE_NOTES, shouldPresentReleaseNotes, type ReleaseNote } from "./releaseNotes";
import { persistTheme, readStoredTheme, resolveTheme, type ThemePreference } from "./theme";
import {
  completeHydration,
  failedHydration,
  initialHydration,
  nextJoinIntervalRange,
  markHydratedEdit,
  normalizeJoinIntervalRange,
  persistedHydratedSettings,
  safeInitialAppSettings,
  safeInitialKeywordSettings,
  isValidGroupRestHours,
  isValidJoinIntervalRange,
  shouldPersistHydratedSettings,
  successfulHydration
} from "./settingsHydration";
import { createLatestKeywordSettingsSaveQueue, type KeywordDeliveryMode, type KeywordSettingsSnapshot } from "./keywordSettingsSaveQueue";
import { createLatestAppSettingsSaveGuard } from "./appSettingsSaveGuard";
import { StatsView } from "./StatsView";
import { ChannelModerationView } from "./ChannelModerationView";
import { ScheduledDMView } from "./ScheduledDMView";
import { createLiveStatisticsPolling, LiveStatisticsView } from "./LiveStatisticsView";
import { createLiveStatisticsUnreadTracker, formatLiveStatisticsNavLabel, normalizeLiveDeliveryPage } from "./liveStatistics";
import { ProxyProfilesPanel } from "./ProxyProfilesPanel";
import { proxyRouteUnavailable, toProxyProfile, type ProxyProfileDraft, type ProxyProfileRow } from "./proxyProfiles";
import { UpdatePanel } from "./UpdatePanel";
import { arrayFromBridge } from "./bridgeCollections";
import "./styles.css";
import { AccountRestView } from "./AccountRestView";
import { normalizeCanonicalKeywords, type CanonicalKeyword } from "./analyticsController";

const LocaleContext = createContext<Locale>("ru");
function useLocale(): Locale {
  return useContext(LocaleContext);
}

type Section = "channels" | "scheduledDM" | "channelModeration" | "accounts" | "accountRest" | "keywords" | "analytics" | "stats" | "liveStats" | "settings" | "releaseNotes";

export function accountReplyTotal(items: ReadonlyArray<{ sent: number }>): number {
  return items.reduce((sum, account) => sum + account.sent, 0);
}

export function activateImportedPositiveCanons(
  rows: KeywordRow[],
  entries: CanonicalImportEntry[]
): KeywordRow[] {
  return mergeKeywords(rows, entries.map((entry) => entry.canonical));
}

export function canActivateChannel(_channel: Pick<CatalogEntry, "active">, _joinIntervalValid: boolean): boolean {
  return true;
}

export function shouldShowGlobalAutomationControls(section: string): boolean {
	return section !== "channelModeration" && section !== "scheduledDM" && section !== "releaseNotes";
}

export function channelJoinSchedule(row: { planned?: boolean; joinNotBefore?: string }): { status: "planned"; joinNotBefore: string } | null {
  return row.planned && row.joinNotBefore ? { status: "planned", joinNotBefore: row.joinNotBefore } : null;
}

export async function refreshKeywordSettingsAuthoritatively(dependencies: {
  invalidatePendingSave(): void;
  load(): Promise<KeywordSettingsSnapshot>;
  apply(settings: KeywordSettingsSnapshot): void;
}): Promise<KeywordSettingsSnapshot> {
  dependencies.invalidatePendingSave();
  const settings = await dependencies.load();
  dependencies.apply(settings);
  return settings;
}

type AnalyticsStats = {
  running: boolean;
  state: string;
  lastRunAt: string;
  nextRunAt: string;
  latestCollection: { newMessages: number; extractedWords: number; newCanonicalWords: number; processedGroups: number; durationMillis: number; errors: string[] };
  totals: { messages: number; canonicalWords: number; forms: number; groups: number; keywordDbBytes: number };
};

const emptyAnalyticsStats: AnalyticsStats = {
  running: false, state: "idle", lastRunAt: "", nextRunAt: "",
  latestCollection: { newMessages: 0, extractedWords: 0, newCanonicalWords: 0, processedGroups: 0, durationMillis: 0, errors: [] },
  totals: { messages: 0, canonicalWords: 0, forms: 0, groups: 0, keywordDbBytes: 0 }
};

export function analyticsStatsFromStatus(value: unknown): AnalyticsStats {
  if (!value || typeof value !== "object") return emptyAnalyticsStats;
  const row = value as Record<string, unknown>;
  const latest = recordValue(firstValue(row, "latestCollection", "LatestCollection", "latestRun", "LatestRun", "lastCollection", "LastCollection")) ?? row;
  const totals = recordValue(firstValue(row, "totals", "Totals", "metrics", "Metrics")) ?? row;
  const state = stringValue(firstValue(row, "state", "State", "status", "Status", "currentState", "CurrentState")) || (anyTruthy(row, "collecting", "Collecting", "running", "Running") ? "running" : "idle");
  const errors = errorValues(firstValue(latest, "errors", "Errors"));
  const lastError = stringValue(firstValue(row, "lastError", "LastError"));
  if (lastError && !errors.includes(lastError)) errors.push(lastError);
  return {
    running: anyTruthy(row, "running", "Running", "collecting", "Collecting", "enabled", "Enabled") || state === "running" || state === "collecting",
    state,
    lastRunAt: stringValue(firstValue(row, "lastRunAt", "LastRunAt")),
    nextRunAt: stringValue(firstValue(row, "nextRunAt", "NextRunAt")),
    latestCollection: {
      newMessages: numberValue(firstValue(latest, "newMessages", "NewMessages")),
      extractedWords: numberValue(firstValue(latest, "extractedWords", "ExtractedWords")),
      newCanonicalWords: numberValue(firstValue(latest, "newCanonicalWords", "NewCanonicalWords", "newCanonicals", "NewCanonicals")),
      processedGroups: numberValue(firstValue(latest, "processedGroups", "ProcessedGroups")),
      durationMillis: numberValue(firstValue(latest, "durationMillis", "DurationMillis", "durationMs", "DurationMs")),
      errors
    },
    totals: {
      messages: numberValue(firstValue(totals, "messages", "Messages", "messageCount", "MessageCount", "allTimeMessages", "AllTimeMessages")),
      canonicalWords: numberValue(firstValue(totals, "canonicalWords", "CanonicalWords", "canonicalCount", "CanonicalCount", "allTimeKeywords", "AllTimeKeywords")),
      forms: numberValue(firstValue(totals, "forms", "Forms", "formCount", "FormCount")),
      groups: numberValue(firstValue(totals, "groups", "Groups", "groupCount", "GroupCount", "allTimeGroups", "AllTimeGroups")),
      keywordDbBytes: numberValue(firstValue(totals, "keywordDbBytes", "KeywordDbBytes", "logicalKeywordBytes", "LogicalKeywordBytes"))
    }
  };
}

function firstValue(row: Record<string, unknown>, ...keys: string[]): unknown {
  return keys.map((key) => row[key]).find((value) => value !== undefined && value !== null);
}

function recordValue(value: unknown): Record<string, unknown> | null { return value && typeof value === "object" ? value as Record<string, unknown> : null; }
function stringValue(value: unknown): string { return typeof value === "string" ? value : ""; }
function normalizeKeywordDeliveryMode(value: unknown): KeywordDeliveryMode {
  return value === "private" || value === "both" || value === "comments" ? value : "comments";
}
function numberValue(value: unknown): number { const number = typeof value === "number" ? value : Number(value); return Number.isFinite(number) ? number : 0; }
function truthy(value: unknown): boolean { return value === true || value === 1 || value === "true"; }
function anyTruthy(row: Record<string, unknown>, ...keys: string[]): boolean { return keys.some((key) => truthy(row[key])); }
function errorValues(value: unknown): string[] { return Array.isArray(value) ? value.filter((item): item is string => typeof item === "string" && item !== "") : stringValue(value) ? [stringValue(value)] : []; }

const navItems: Array<{ section: Section; label: MessageKey; icon: typeof RadioTower }> = [
  { section: "channels", label: "channels", icon: RadioTower },
  { section: "scheduledDM", label: "scheduledDM", icon: MessageCircle },
  { section: "channelModeration", label: "channelModeration", icon: ShieldCheck },
  { section: "accounts", label: "accounts", icon: Bot },
  { section: "accountRest", label: "accountRest", icon: Hourglass },
  { section: "keywords", label: "keywords", icon: Hash },
  { section: "analytics", label: "analytics", icon: FolderSearch },
  { section: "stats", label: "stats", icon: BarChart3 },
  { section: "liveStats", label: "liveStats", icon: BarChart3 },
  { section: "settings", label: "settings", icon: Settings },
  { section: "releaseNotes", label: "releaseNotes", icon: Sparkles }
];

const leads: Record<Section, MessageKey> = {
  channels: "channelsLead",
  scheduledDM: "scheduledDMLead",
  channelModeration: "channelModerationLead",
  accounts: "accountsLead",
  accountRest: "accountRestLead",
  keywords: "keywordsLead",
  analytics: "analyticsLead",
  stats: "statsLead",
  liveStats: "liveStatsLead",
  settings: "settingsLead",
  releaseNotes: "releaseNotesLead"
};

const liveStatisticsUnreadQuery = new models.wails.LiveDeliveryQueryDTO({
  from: "1970-01-01T00:00:00Z",
  to: "2100-01-01T00:00:00Z",
  page: 1,
  pageSize: 1,
  sortBy: "triggeredAt",
  sortDirection: "descending"
});

export default function App() {
  const [locale, setLocale] = useState<Locale>(() => readStoredLocale(localStorage));
  const [theme, setTheme] = useState<ThemePreference>(() => readStoredTheme(localStorage));
  const [section, setSection] = useState<Section>("channels");
  const [liveStatisticsUnread, setLiveStatisticsUnread] = useState(0);
  const activeSectionRef = useRef<Section>("channels");
  const liveStatisticsUnreadTrackerRef = useRef<ReturnType<typeof createLiveStatisticsUnreadTracker> | null>(null);
  if (!liveStatisticsUnreadTrackerRef.current) {
    liveStatisticsUnreadTrackerRef.current = createLiveStatisticsUnreadTracker(localStorage);
  }
  const liveStatisticsUnreadTracker = liveStatisticsUnreadTrackerRef.current;
  const [running, setRunning] = useState(false);
  const [showReleaseNotes, setShowReleaseNotes] = useState(() => shouldPresentReleaseNotes(localStorage));
  const [automationError, setAutomationError] = useState("");
  const [accounts, setAccounts] = useState<AccountRow[]>([]);
  const [accountsError, setAccountsError] = useState("");
  const [proxyProfiles, setProxyProfiles] = useState<ProxyProfileRow[]>([]);
  const [proxyProfilesError, setProxyProfilesError] = useState("");
  const [proxyBusyID, setProxyBusyID] = useState("");
  const [uiScale, setUIScale] = useState<UIScale>(100);
  const [keywordRows, setKeywordRows] = useState<KeywordRow[]>(keywordRowsFromSettings(safeInitialKeywordSettings.keywords, safeInitialKeywordSettings.directMessageKeywords));
  const [minusKeywords, setMinusKeywords] = useState<string[]>(safeInitialKeywordSettings.minusKeywords);
  const [sharedReply, setSharedReply] = useState(safeInitialKeywordSettings.sharedReply);
  const [privateReply, setPrivateReply] = useState("");
  const [keywordDeliveryMode, setKeywordDeliveryMode] = useState<KeywordDeliveryMode>(safeInitialKeywordSettings.deliveryMode);
  const [keywordHydration, setKeywordHydration] = useState(initialHydration);
  const [keywordSettingsError, setKeywordSettingsError] = useState("");
  const keywordSettingsRevisionRef = useRef(0);
  const keywordSettingsSaveQueueRef = useRef<ReturnType<typeof createLatestKeywordSettingsSaveQueue> | null>(null);
  if (!keywordSettingsSaveQueueRef.current) {
    keywordSettingsSaveQueueRef.current = createLatestKeywordSettingsSaveQueue(
      (settings) => SaveKeywordSettings(settings),
      (applied, version) => {
        setKeywordRows(keywordRowsFromSettings(applied.keywords, applied.directMessageKeywords));
        setMinusKeywords(applied.minusKeywords ?? []);
        setSharedReply(applied.sharedReply);
        setPrivateReply(applied.privateReply ?? applied.sharedReply);
        setKeywordDeliveryMode(normalizeKeywordDeliveryMode(applied.deliveryMode));
        keywordSettingsRevisionRef.current = applied.revision;
        setKeywordHydration((state) => persistedHydratedSettings(state, version));
        setKeywordSettingsError("");
      },
      (error) => setKeywordSettingsError(error instanceof Error ? error.message : String(error))
    );
  }
  const keywordSettingsSaveQueue = keywordSettingsSaveQueueRef.current;

  const refreshKeywordSettings = useCallback(async () => {
    setKeywordSettingsError("");
    try {
      await refreshKeywordSettingsAuthoritatively({
        invalidatePendingSave: keywordSettingsSaveQueue.invalidate,
        load: GetKeywordSettings,
        apply: (settings) => {
          setKeywordRows(keywordRowsFromSettings(settings.keywords, settings.directMessageKeywords));
          setMinusKeywords(settings.minusKeywords ?? []);
          setSharedReply(settings.sharedReply);
          setPrivateReply(settings.privateReply ?? settings.sharedReply);
          setKeywordDeliveryMode(normalizeKeywordDeliveryMode(settings.deliveryMode));
          keywordSettingsRevisionRef.current = settings.revision;
          setKeywordHydration((state) => successfulHydration({
            ...state,
            editVersion: Math.max(state.editVersion, settings.revision)
          }));
        }
      });
    } catch (error) {
      setKeywordSettingsError(error instanceof Error ? error.message : String(error));
      throw error;
    }
  }, [keywordSettingsSaveQueue]);

  useEffect(() => {
    persistLocale(localStorage, locale);
    document.documentElement.lang = locale;
  }, [locale]);

  useEffect(() => {
    persistTheme(localStorage, theme);
    document.documentElement.dataset.theme = resolveTheme(theme);
  }, [theme]);

  useEffect(() => {
    const stored = readStoredScale(localStorage);
    if (stored) {
      setUIScale(stored);
      return;
    }
    let cancelled = false;
    WindowGetSize().then((size) => {
      if (!cancelled) setUIScale(autoScale({ width: size.w, height: size.h }, 1));
    }).catch(() => {
      if (!cancelled) setUIScale(autoScale({ width: window.innerWidth, height: window.innerHeight }, 1));
    });
    return () => { cancelled = true; };
  }, []);

  const refreshProxyProfiles = async () => {
    try {
      const profiles = await ListProxyProfiles();
      setProxyProfiles(arrayFromBridge(profiles).map(toProxyProfile));
      setProxyProfilesError("");
    } catch (error) {
      setProxyProfilesError(error instanceof Error ? error.message : String(error));
    }
  };

  useEffect(() => {
    let canceled = false;
    const refresh = () => ListProxyProfiles()
      .then((profiles) => { if (!canceled) { setProxyProfiles(arrayFromBridge(profiles).map(toProxyProfile)); setProxyProfilesError(""); } })
      .catch((error) => { if (!canceled) setProxyProfilesError(error instanceof Error ? error.message : String(error)); });
    void refresh();
    const handle = window.setInterval(refresh, 5000);
    return () => { canceled = true; window.clearInterval(handle); };
  }, []);

  const saveProxyProfile = async (draft: ProxyProfileDraft) => {
    setProxyBusyID(draft.id || "new");
    setProxyProfilesError("");
    try {
      await SaveProxyProfile(draft);
      await refreshProxyProfiles();
    } catch (error) {
      setProxyProfilesError(error instanceof Error ? error.message : String(error));
      throw error;
    } finally {
      setProxyBusyID("");
    }
  };

  const deleteProxyProfile = async (id: string) => {
    setProxyBusyID(id);
    try {
      await DeleteProxyProfile(id);
      await refreshProxyProfiles();
    } catch (error) {
      setProxyProfilesError(error instanceof Error ? error.message : String(error));
    } finally {
      setProxyBusyID("");
    }
  };

  const checkProxyProfile = async (id: string) => {
    setProxyBusyID(id);
    try {
      if (id !== "system") await CheckProxyProfile(id);
      await refreshProxyProfiles();
    } catch (error) {
      setProxyProfilesError(error instanceof Error ? error.message : String(error));
      await refreshProxyProfiles();
    } finally {
      setProxyBusyID("");
    }
  };

  const assignAccountProxy = async (accountID: string, routeID: string) => {
    setAccountsError("");
    const updated = toAccount(await AssignAccountProxy(accountID, routeID === "system" ? "" : routeID));
    setAccounts((current) => applyAccountSnapshot(current, updated));
    await refreshProxyProfiles();
  };

  useEffect(() => {
    document.documentElement.style.setProperty("--ui-scale", String(uiScale / 100));
    document.documentElement.dataset.uiScale = String(uiScale);
    return installScaleShortcuts({ target: window, storage: localStorage, initialScale: uiScale, onScale: setUIScale });
  }, [uiScale]);

  useEffect(() => {
    let canceled = false;
    GetKeywordSettings()
      .then((settings) => {
        if (canceled) return;
        setKeywordRows(keywordRowsFromSettings(settings.keywords, settings.directMessageKeywords));
        setMinusKeywords(settings.minusKeywords ?? []);
        setSharedReply(settings.sharedReply);
        setPrivateReply(settings.privateReply ?? settings.sharedReply);
        setKeywordDeliveryMode(normalizeKeywordDeliveryMode(settings.deliveryMode));
        keywordSettingsRevisionRef.current = settings.revision;
        setKeywordHydration(successfulHydration);
      })
      .catch((error) => {
        if (!canceled) {
          setKeywordSettingsError(error instanceof Error ? error.message : String(error));
          setKeywordHydration(failedHydration);
        }
      });
    return () => { canceled = true; };
  }, []);

  useEffect(() => {
	if (!shouldPersistHydratedSettings(keywordHydration)) return;
    const version = keywordHydration.editVersion;
    const handle = window.setTimeout(() => {
      const settings: KeywordSettingsSnapshot = {
        keywords: keywordRows.map((row) => row.keyword),
        minusKeywords,
        sharedReply,
        privateReply,
        deliveryMode: keywordDeliveryMode,
        directMessageKeywords: directMessageKeywordsFromRows(keywordRows),
        revision: keywordSettingsRevisionRef.current
      };
      void keywordSettingsSaveQueue.enqueue(settings, version).catch(() => undefined);
    }, 250);
    return () => window.clearTimeout(handle);
  }, [keywordDeliveryMode, keywordRows, keywordHydration, minusKeywords, privateReply, sharedReply]);

  useEffect(() => {
    let canceled = false;
    const refreshAccounts = createLatestAccountRefresh(
      () => GetAccounts().then((rows) => arrayFromBridge(rows).map(toAccount)),
      (rows) => {
        if (!canceled) {
          setAccounts(rows);
          setAccountsError("");
        }
      },
      (error) => {
        if (!canceled) setAccountsError(error instanceof Error ? error.message : String(error));
      }
    );
    void refreshAccounts();
    const handle = window.setInterval(refreshAccounts, 2000);
    return () => {
      canceled = true;
      window.clearInterval(handle);
    };
  }, []);

  useEffect(() => {
    let canceled = false;
    const refresh = async () => {
      try {
        const backendRunning = await readAutomationRunning(GetDashboard);
        if (!canceled) {
          setRunning(backendRunning);
        }
      } catch (error) {
        if (!canceled) {
          setAutomationError(error instanceof Error ? error.message : String(error));
        }
      }
    };

    void refresh();
    const handle = window.setInterval(() => void refresh(), 1000);
    return () => {
      canceled = true;
      window.clearInterval(handle);
    };
  }, []);

  useEffect(() => {
    let disposed = false;
    let inFlight = false;
    const refresh = () => {
      if (inFlight) return;
      inFlight = true;
      void GetLiveDeliveryStatistics(liveStatisticsUnreadQuery)
        .then(normalizeLiveDeliveryPage)
        .then((page) => {
          if (!disposed) {
            setLiveStatisticsUnread(liveStatisticsUnreadTracker.observe(page.total));
          }
        })
        .catch(() => undefined)
        .finally(() => { inFlight = false; });
    };
    const stopPolling = createLiveStatisticsPolling(document, refresh);
    return () => {
      disposed = true;
      stopPolling();
    };
  }, [liveStatisticsUnreadTracker]);

  const toggleAutomation = async () => {
    setAutomationError("");
    try {
      if (running) {
        await StopAutomation();
        setRunning(false);
        return;
      }
      const version = keywordHydration.editVersion;
      await startAutomationAfterSavingKeywordSettings(
        () => keywordSettingsSaveQueue.enqueue({
          keywords: keywordRows.map((row) => row.keyword),
          minusKeywords,
          sharedReply,
          privateReply,
          deliveryMode: keywordDeliveryMode,
          directMessageKeywords: directMessageKeywordsFromRows(keywordRows),
          revision: keywordSettingsRevisionRef.current
        }, version),
        StartAutomation
      );
      setKeywordHydration((state) => persistedHydratedSettings(state, version));
      setKeywordSettingsError("");
      setRunning(true);
    } catch (error) {
      setAutomationError(error instanceof Error ? error.message : String(error));
    }
  };

  const updateKeywordRows: Dispatch<SetStateAction<KeywordRow[]>> = (next) => {
    keywordSettingsSaveQueue.invalidate();
    setKeywordRows(next);
    setKeywordHydration(markHydratedEdit);
  };
  const updateSharedReply: Dispatch<SetStateAction<string>> = (next) => {
    keywordSettingsSaveQueue.invalidate();
    setSharedReply(next);
    setKeywordHydration(markHydratedEdit);
  };
  const updatePrivateReply: Dispatch<SetStateAction<string>> = (next) => {
    keywordSettingsSaveQueue.invalidate();
    setPrivateReply(next);
    setKeywordHydration(markHydratedEdit);
  };
  const updateMinusKeywords: Dispatch<SetStateAction<string[]>> = (next) => {
    keywordSettingsSaveQueue.invalidate();
    setMinusKeywords(next);
    setKeywordHydration(markHydratedEdit);
  };
  const updateKeywordDeliveryMode = (next: KeywordDeliveryMode) => {
    keywordSettingsSaveQueue.invalidate();
    setKeywordDeliveryMode(next);
    setKeywordHydration(markHydratedEdit);
  };

  const selectSection = (next: Section) => {
    const previous = activeSectionRef.current;
    activeSectionRef.current = next;
    if (previous === "liveStats" && next !== "liveStats") {
      setLiveStatisticsUnread(liveStatisticsUnreadTracker.markRead());
    }
    setSection(next);
  };

  return (
    <LocaleContext.Provider value={locale}>
    <main className={`shell uiScale${uiScale}`}>
      <aside className="sidebar">
        <div>
          <div className="brand">
            <span className="brandLogo" aria-hidden="true">
              <img alt="" src={telegramTurquoiseLogo} />
            </span>
            <span className="brandTitle">Telegram Companion</span>
            <span className="appVersion">{APP_VERSION_LABEL}</span>
          </div>
          <nav>
            {navItems.map((item) => {
              const Icon = item.icon;
              return (
                <button
                  className={section === item.section ? "nav active" : "nav"}
                  key={item.section}
                  onClick={() => selectSection(item.section)}
                  type="button"
                >
                  <Icon size={18} />
                  {formatLiveStatisticsNavLabel(t(locale, item.label), item.section === "liveStats" ? liveStatisticsUnread : 0)}
                </button>
              );
            })}
          </nav>
        </div>
        <div className="developerCredit">
          <div className="buildTimestamp">{BUILD_TIMESTAMP_LABEL}</div>
          <div>Developed by Denis Myskar</div>
        </div>
      </aside>

      <section className={section === "analytics" ? "content content--analytics" : section === "keywords" ? "content content--keywords" : section === "liveStats" ? "content content--liveStats" : section === "channelModeration" ? "content content--channelModeration" : section === "scheduledDM" ? "content content--scheduledDM" : "content"}>
        <header className="topbar">
          <div>
            <h1>{t(locale, section)}</h1>
            {section !== "keywords" && <p>{t(locale, leads[section])}</p>}
          </div>
          {shouldShowGlobalAutomationControls(section) && <div className="topbarActions">
            <button className={running ? "startButton stop" : "startButton"} onClick={() => void toggleAutomation()} type="button">
              {running ? t(locale, "stop") : t(locale, "start")}
            </button>
          </div>}
          {section === "releaseNotes" && <div className="topbarActions topbarActions--updates">
            <UpdatePanel locale={locale} />
          </div>}
        </header>
        {automationError && <div className="errorBanner">{automationError}</div>}

        {section === "channels" && <ChannelsView />}
        {section === "scheduledDM" && <ScheduledDMView accounts={accounts} locale={locale} />}
        {section === "channelModeration" && <ChannelModerationView locale={locale} />}
        {section === "accounts" && <section className="accountsWorkspace">
          <AccountsView accounts={accounts} error={accountsError} profiles={proxyProfiles} setAccounts={setAccounts} setError={setAccountsError} onAssignProxy={assignAccountProxy} />
        </section>}
        {section === "keywords" && (keywordHydration.phase === "loaded"
          ? <KeywordsView keywordRows={keywordRows} onKeywordRowsChange={updateKeywordRows} minusKeywords={minusKeywords} onMinusKeywordsChange={updateMinusKeywords} sharedReply={sharedReply} onSharedReplyChange={updateSharedReply} privateReply={privateReply} onPrivateReplyChange={updatePrivateReply} deliveryMode={keywordDeliveryMode} onDeliveryModeChange={updateKeywordDeliveryMode} settingsError={keywordSettingsError} />
          : <SettingsHydrationState error={keywordSettingsError} />)}
        {section === "analytics" && (keywordHydration.phase === "loaded"
          ? <AnalyticsSection keywordRows={keywordRows} onKeywordRowsChange={updateKeywordRows} />
          : <SettingsHydrationState error={keywordSettingsError} />)}
        {section === "stats" && <StatsView locale={locale} />}
        {section === "liveStats" && <LiveStatisticsView locale={locale} onTriggerDeleted={refreshKeywordSettings} runTriggerDeletion={keywordSettingsSaveQueue.runExclusive} />}
        {section === "accountRest" && <AccountRestView locale={locale} />}
        {section === "settings" && <SettingsView locale={locale} onLocaleChange={setLocale} theme={theme} onThemeChange={setTheme} proxyProfiles={proxyProfiles} proxyError={proxyProfilesError} proxyBusyID={proxyBusyID} onSaveProxy={saveProxyProfile} onDeleteProxy={deleteProxyProfile} onCheckProxy={checkProxyProfile} />}
        {section === "releaseNotes" && <ReleaseNotesView locale={locale} />}
      </section>
    </main>
    {showReleaseNotes && <ReleaseNotesOverlay locale={locale} onDismiss={() => {
      markReleaseNotesSeen(localStorage);
      setShowReleaseNotes(false);
    }} />}
    </LocaleContext.Provider>
  );
}

function ReleaseNotesOverlay({ locale, onDismiss }: { locale: Locale; onDismiss: () => void }) {
  const release = currentReleaseNotes();
  return <div aria-modal="true" className="releaseOverlay" role="dialog">
    <div className="releaseOverlayContent">
      <div className="releaseEyebrow">Telegram Companion {APP_VERSION_LABEL}</div>
      <h1>{locale === "ru" ? "Что нового" : "What's new"}</h1>
      <ReleaseNoteBody locale={locale} release={release} />
      <button autoFocus className="startButton releaseContinue" onClick={onDismiss} type="button">
        {locale === "ru" ? "Продолжить" : "Continue"}
      </button>
    </div>
  </div>;
}

function ReleaseNotesView({ locale }: { locale: Locale }) {
  return <section className="releaseNotesPage">
    {RELEASE_NOTES.map((release) => <article className="releaseNoteEntry" key={release.version}>
      <div className="releaseEyebrow">v{release.version} · {release.date}</div>
      <ReleaseNoteBody locale={locale} release={release} />
    </article>)}
  </section>;
}

function ReleaseNoteBody({ locale, release }: { locale: Locale; release: ReleaseNote }) {
  return <>
    <h2>{release.title[locale]}</h2>
    <ul>{release.items[locale].map((item) => <li key={item}>{item}</li>)}</ul>
  </>;
}

function AnalyticsSection({ keywordRows, onKeywordRowsChange }: { keywordRows: KeywordRow[]; onKeywordRowsChange: Dispatch<SetStateAction<KeywordRow[]>> }) {
  const locale = useLocale();
  const [topics, setTopics] = useState<string[]>([]);
  useEffect(() => {
    let cancelled = false;
    GetCatalog("scout").then((rows) => {
      if (!cancelled) setTopics(existingTopics(arrayFromBridge(rows).map((row) => row.topic)));
    }).catch(() => {
      if (!cancelled) setTopics([]);
    });
    return () => { cancelled = true; };
  }, []);
  return <AnalyticsView existingKeywords={keywordRows} locale={locale} onKeywordsChange={onKeywordRowsChange} topics={topics} />;
}

type CatalogEntryWithJoinSchedule = CatalogEntry & { planned?: boolean; joinNotBefore?: string };
type JoinSettingsSnapshot = typeof safeInitialAppSettings & { revision: number };
type DeleteCatalogEntriesBinding = (catalog: CatalogKind, channelIDs: string[]) => Promise<CatalogEntry[]>;

function deleteCatalogEntries(catalog: CatalogKind, channelIDs: string[]): Promise<CatalogEntry[]> {
	return (DeleteCatalogEntries as DeleteCatalogEntriesBinding)(catalog, channelIDs);
}

function toCatalogEntry(row: Omit<CatalogEntryWithJoinSchedule, "active"> & { active?: boolean }): CatalogEntryWithJoinSchedule {
  return normalizeCatalogEntry(row) as CatalogEntryWithJoinSchedule;
}

type ConfirmationRequest = {
  message: string;
  resolve: (confirmed: boolean) => void;
};

function useInAppConfirmation() {
  const [confirmation, setConfirmation] = useState<ConfirmationRequest | null>(null);
  const confirmationRef = useRef<ConfirmationRequest | null>(null);
  const confirmInApp = useCallback((message: string) => new Promise<boolean>((resolve) => {
    confirmationRef.current?.resolve(false);
    const request = { message, resolve };
    confirmationRef.current = request;
    setConfirmation(request);
  }), []);
  const resolveConfirmation = useCallback((confirmed: boolean) => {
    const request = confirmationRef.current;
    if (!request) return;
    confirmationRef.current = null;
    setConfirmation(null);
    request.resolve(confirmed);
  }, []);

  useEffect(() => () => {
    confirmationRef.current?.resolve(false);
    confirmationRef.current = null;
  }, []);

  return { confirmation, confirmInApp, resolveConfirmation };
}

export function cycleConfirmationFocusIndex(activeIndex: number, backwards: boolean): number {
  return (activeIndex + (backwards ? -1 : 1) + 2) % 2;
}

function ConfirmationDialog({ cancelLabel, confirmLabel, message, onCancel, onConfirm }: {
  cancelLabel: string;
  confirmLabel: string;
  message: string;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const cancelButtonRef = useRef<HTMLButtonElement>(null);
  const confirmButtonRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const keepFocusInsideDialog = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        onCancel();
        return;
      }
      if (event.key !== "Tab") return;
      event.preventDefault();
      const controls = [cancelButtonRef.current, confirmButtonRef.current];
      const currentIndex = controls.findIndex((control) => control === document.activeElement);
      const nextIndex = currentIndex < 0 ? 0 : cycleConfirmationFocusIndex(currentIndex, event.shiftKey);
      controls[nextIndex]?.focus();
    };
    cancelButtonRef.current?.focus();
    window.addEventListener("keydown", keepFocusInsideDialog);
    return () => {
      window.removeEventListener("keydown", keepFocusInsideDialog);
      previousFocus?.focus();
    };
  }, [onCancel]);

  return (
    <div className="modalBackdrop" onMouseDown={onCancel} role="presentation">
      <section
        aria-label={message}
        aria-modal="true"
        className="confirmationDialog"
        onMouseDown={(event) => event.stopPropagation()}
        role="dialog"
      >
        <p>{message}</p>
        <footer>
          <button autoFocus className="secondaryButton" onClick={onCancel} ref={cancelButtonRef} type="button"><X size={15} />{cancelLabel}</button>
          <button className="secondaryButton confirmDangerButton" onClick={onConfirm} ref={confirmButtonRef} type="button"><Check size={15} />{confirmLabel}</button>
        </footer>
      </section>
    </div>
  );
}

function ChannelsView() {
  const locale = useLocale();
  const [tab, setTab] = useState<CatalogKind | "topics">("outbound");
  const [catalogs, setCatalogs] = useState<Record<CatalogKind, CatalogEntryWithJoinSchedule[]>>({ outbound: [], scout: [] });
  const [error, setError] = useState("");
  const [showImport, setShowImport] = useState(false);
  const [linksDraft, setLinksDraft] = useState("");
  const [topicDraft, setTopicDraft] = useState("");
  const [topicInvalid, setTopicInvalid] = useState(false);
  const [bulkTopic, setBulkTopic] = useState("");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [deleting, setDeleting] = useState(false);
  const deletingRef = useRef(false);
  const [saving, setSaving] = useState(false);
  const [appSettings, setAppSettings] = useState<JoinSettingsSnapshot>({ ...safeInitialAppSettings, revision: 0 });
  const [joinHydration, setJoinHydration] = useState(initialHydration);
  const joinSettingsEditVersionRef = useRef(0);
  const joinSettingsSaveGuardRef = useRef<ReturnType<typeof createLatestAppSettingsSaveGuard<JoinSettingsSnapshot>> | null>(null);
  const { confirmation, confirmInApp, resolveConfirmation } = useInAppConfirmation();
  if (!joinSettingsSaveGuardRef.current) {
    joinSettingsSaveGuardRef.current = createLatestAppSettingsSaveGuard<JoinSettingsSnapshot>(SaveAppSettings, setAppSettings);
  }
  const joinSettingsSaveGuard = joinSettingsSaveGuardRef.current;

  const joinIntervalValid = isValidJoinIntervalRange(
    appSettings.joinIntervalMinMinutes,
    appSettings.joinIntervalMaxMinutes
  );
  const joinActivationReady = joinHydration.phase === "loaded" && joinIntervalValid;

  const topics = useMemo(
    () => existingTopics([...catalogs.outbound, ...catalogs.scout].map((row) => row.topic)),
    [catalogs]
  );

  useEffect(() => {
    let canceled = false;
    Promise.all([GetCatalog("outbound"), GetCatalog("scout")])
      .then(([outbound, scout]) => {
        if (!canceled) setCatalogs({ outbound: arrayFromBridge(outbound).map(toCatalogEntry), scout: arrayFromBridge(scout).map(toCatalogEntry) });
      })
      .catch((err) => {
        if (!canceled) setError(err instanceof Error ? err.message : String(err));
      });
    return () => { canceled = true; };
  }, []);

  useEffect(() => {
    let canceled = false;
    GetAppSettings()
      .then((settings) => {
        if (canceled) return;
        const preservePendingEdits = joinSettingsEditVersionRef.current > 0;
        setAppSettings((current) => preservePendingEdits ? {
          ...settings,
          joinIntervalMinMinutes: current.joinIntervalMinMinutes,
          joinIntervalMaxMinutes: current.joinIntervalMaxMinutes,
          joinIntervalEnabled: current.joinIntervalEnabled
        } : settings);
        setJoinHydration((state) => completeHydration(state, preservePendingEdits));
      })
      .catch((err) => {
        if (canceled) return;
        setError(err instanceof Error ? err.message : String(err));
        setJoinHydration(failedHydration);
      });
    return () => { canceled = true; };
  }, []);

  useEffect(() => {
    if (tab === "topics") return;
    return createCatalogRemovalPolling({
      rows: catalogs[tab],
      load: () => GetCatalog(tab).then((rows) => arrayFromBridge(rows).map(toCatalogEntry)),
      onRows: (rows) => setCatalogs((current) => ({ ...current, [tab]: rows.map(toCatalogEntry) })),
      onError: setError
    });
  }, [catalogs, tab]);

  useEffect(() => {
    if (!shouldPersistHydratedSettings(joinHydration) || !joinIntervalValid) return;
    const version = joinHydration.editVersion;
    const handle = window.setTimeout(() => {
      joinSettingsSaveGuard.save({
        repliesPerMinute: appSettings.repliesPerMinute,
        minIntervalSeconds: appSettings.minIntervalSeconds,
        joinIntervalMinMinutes: appSettings.joinIntervalMinMinutes,
        joinIntervalMaxMinutes: appSettings.joinIntervalMaxMinutes,
        joinIntervalEnabled: appSettings.joinIntervalEnabled,
        groupRestHours: appSettings.groupRestHours,
        groupRestEnabled: appSettings.groupRestEnabled,
        directMessages: appSettings.directMessages,
        proxy: appSettings.proxy,
        revision: appSettings.revision
      }, version)
        .then(() => {
          setJoinHydration((state) => persistedHydratedSettings(state, version));
        })
        .catch((err) => setError(err instanceof Error ? err.message : String(err)));
    }, 250);
    return () => window.clearTimeout(handle);
  }, [appSettings, joinHydration, joinIntervalValid, joinSettingsSaveGuard]);

  const addLinks = async () => {
    if (tab === "topics") return;
    const input = applyBulkTopic(linksDraft.split(/[\s,;]+/u), topicDraft);
    if (!linksDraft.trim() || input.error) {
      setShowImport(true);
      if (input.error) {
        setTopicInvalid(true);
        setError("");
      }
      return;
    }
    setSaving(true);
    setError("");
    setTopicInvalid(false);
    try {
      const rows = await AddCatalogLinks(input.links.join("\n"), tab, input.topic);
      setCatalogs((current) => ({ ...current, [tab]: arrayFromBridge(rows).map(toCatalogEntry) }));
      setLinksDraft("");
      setTopicDraft("");
      setTopicInvalid(false);
      setShowImport(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  };

  const toggleChannel = async (channel: CatalogEntry) => {
    if (tab === "topics" || deleting || channel.status === "removing") return;
    setError("");
    try {
      const rows = await requestCatalogEntryToggle({
        catalog: tab,
        channel,
        toggleCatalogEntry: ToggleCatalogEntry
      });
      if (rows) setCatalogs((current) => ({ ...current, [tab]: arrayFromBridge(rows).map(toCatalogEntry) }));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  const joinChannel = async (channel: CatalogEntry) => {
    if (tab === "topics" || deleting || channel.status === "removing" || !joinActivationReady) return;
    setError("");
    try {
      const rows = await requestCatalogEntryJoin({
        catalog: tab,
        channelID: channel.id,
        confirmationMessage: t(locale, "confirmChannelActivation"),
        confirm: confirmInApp,
        joinCatalogEntry: JoinCatalogEntry
      });
      if (rows) setCatalogs((current) => ({ ...current, [tab]: arrayFromBridge(rows).map(toCatalogEntry) }));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  const leaveChannel = async (channel: CatalogEntry) => {
    if (tab === "topics" || deleting || channel.status === "removing") return;
    setError("");
    try {
      const rows = await requestCatalogEntryLeave({
        catalog: tab,
        channelID: channel.id,
        confirmationMessage: t(locale, "confirmChannelLeave"),
        confirm: confirmInApp,
        leaveCatalogEntry: LeaveCatalogEntry
      });
      if (rows) setCatalogs((current) => ({ ...current, [tab]: arrayFromBridge(rows).map(toCatalogEntry) }));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  const changeSelectedTopics = async () => {
    if (tab === "topics" || deleting) return;
    const topic = bulkTopic.trim();
    if (!topic) {
      setError(t(locale, "topicRequired"));
      return;
    }
    setSaving(true);
    setError("");
    try {
      const rows = await SetChannelTopics(tab, [...selected], topic);
      setCatalogs((current) => ({ ...current, [tab]: arrayFromBridge(rows).map(toCatalogEntry) }));
      setSelected(new Set());
      setBulkTopic("");
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  };

  const deleteSelectedChannels = async () => {
    if (tab === "topics" || deleting || deletingRef.current || selected.size === 0) return;
    const catalog = tab;
    deletingRef.current = true;
    setDeleting(true);
    setError("");
    try {
      const rows = await requestCatalogEntriesDeletion({
        catalog,
        channelIDs: [...selected],
        confirmationMessage: t(locale, "confirmDeleteCatalogEntries"),
        confirm: confirmInApp,
        deleteCatalogEntries
      });
      if (!rows) return;
      setCatalogs((current) => ({ ...current, [catalog]: arrayFromBridge(rows).map(toCatalogEntry) }));
      setSelected(new Set());
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      deletingRef.current = false;
      setDeleting(false);
    }
  };

  const rows = tab === "topics" ? [] : catalogs[tab];

  const updateJoinInterval = (field: "joinIntervalMinMinutes" | "joinIntervalMaxMinutes", value: number) => {
    joinSettingsEditVersionRef.current += 1;
    joinSettingsSaveGuard.markEdited(joinSettingsEditVersionRef.current);
    setAppSettings((current) => {
      const range = nextJoinIntervalRange(
        current.joinIntervalMinMinutes,
        current.joinIntervalMaxMinutes,
        field === "joinIntervalMinMinutes" ? "minimum" : "maximum",
        value
      );
      return {
        ...current,
        joinIntervalMinMinutes: range.minimum,
        joinIntervalMaxMinutes: range.maximum
      };
    });
    setJoinHydration(markHydratedEdit);
  };

  const toggleJoinInterval = () => {
    joinSettingsEditVersionRef.current += 1;
    joinSettingsSaveGuard.markEdited(joinSettingsEditVersionRef.current);
    setAppSettings((current) => {
      const range = normalizeJoinIntervalRange(
        current.joinIntervalMinMinutes,
        current.joinIntervalMaxMinutes
      );
      return {
        ...current,
        joinIntervalEnabled: !current.joinIntervalEnabled,
        joinIntervalMinMinutes: range.minimum,
        joinIntervalMaxMinutes: range.maximum
      };
    });
    setJoinHydration(markHydratedEdit);
  };

  return (
    <section className="glassPanel">
      <div className="catalogTabs" role="tablist">
        <button className={tab === "outbound" ? "active" : ""} onClick={() => { setTab("outbound"); setSelected(new Set()); }} type="button">{t(locale, "outboundChannels")}</button>
        <button className={tab === "scout" ? "active" : ""} onClick={() => { setTab("scout"); setSelected(new Set()); }} type="button">{t(locale, "scoutChannels")}</button>
        <button className={tab === "topics" ? "active" : ""} onClick={() => { setTab("topics"); setSelected(new Set()); }} type="button">{t(locale, "topics")}</button>
      </div>
      {tab !== "topics" && <div aria-busy={deleting} className="panelToolbar">
        <fieldset className="joinIntervalControls">
          <legend>{t(locale, "joinInterval")}</legend>
          <label className="joinIntervalToggle">
            <span>{t(locale, "joinIntervalEnabled")}</span>
            <button
              aria-label={t(locale, "joinIntervalEnabled")}
              aria-pressed={appSettings.joinIntervalEnabled}
              className={appSettings.joinIntervalEnabled ? "toggle on" : "toggle"}
              onClick={toggleJoinInterval}
              type="button"
            />
          </label>
          <label>
            <span>{t(locale, "joinIntervalFrom")}</span>
            <input aria-label={t(locale, "joinIntervalFrom")} disabled={!appSettings.joinIntervalEnabled} max={3000} min={0} type="number" value={appSettings.joinIntervalMinMinutes} onChange={(event) => updateJoinInterval("joinIntervalMinMinutes", Number(event.target.value))} />
          </label>
          <label>
            <span>{t(locale, "joinIntervalTo")}</span>
            <input aria-label={t(locale, "joinIntervalTo")} disabled={!appSettings.joinIntervalEnabled} max={3000} min={0} type="number" value={appSettings.joinIntervalMaxMinutes} onChange={(event) => updateJoinInterval("joinIntervalMaxMinutes", Number(event.target.value))} />
          </label>
        </fieldset>
        {selected.size > 0 && (
          <div className="bulkTopicAction">
            <input disabled={deleting} list="catalog-topics" value={bulkTopic} onChange={(event) => setBulkTopic(event.target.value)} placeholder={t(locale, "topic")} />
            <button disabled={saving || deleting} onClick={() => void changeSelectedTopics()} type="button"><Pencil size={16} />{t(locale, "changeTopic")}</button>
          </div>
        )}
        {selected.size > 0 && <button aria-label={deleting ? t(locale, "deletingSelectedChannels") : t(locale, "deleteSelectedChannels")} className="catalogDeleteButton" disabled={deleting} onClick={() => void deleteSelectedChannels()} title={t(locale, "deleteSelectedChannels")} type="button"><Trash2 size={16} />{t(locale, "delete")}</button>}
        <button onClick={() => setShowImport((value) => !value)} type="button"><Plus size={16} />{t(locale, "addLinks")}</button>
      </div>}
      {tab !== "topics" && showImport && (
        <div className="channelImport">
          <label>
            {t(locale, "channelLinks")}
            <textarea
              value={linksDraft}
              onChange={(event) => setLinksDraft(event.target.value)}
              placeholder={t(locale, "channelLinksPlaceholder")}
            />
          </label>
          <label className={topicInvalid ? "invalidField" : ""}>
            {t(locale, "topic")}
            <input
              aria-invalid={topicInvalid}
              className={topicInvalid ? "invalid" : ""}
              required
              list="catalog-topics"
              value={topicDraft}
              onChange={(event) => {
                setTopicDraft(event.target.value);
                if (event.target.value.trim()) setTopicInvalid(false);
              }}
              placeholder={t(locale, "topicPlaceholder")}
            />
            {topicInvalid && <small className="fieldError">{t(locale, "topicRequired")}</small>}
          </label>
          <div className="channelImportActions">
            <button disabled={saving} onClick={() => void addLinks()} type="button"><Check size={16} />{saving ? t(locale, "saving") : t(locale, "addChannels")}</button>
            <button onClick={() => setShowImport(false)} type="button"><X size={16} />{t(locale, "cancel")}</button>
          </div>
        </div>
      )}
      <datalist id="catalog-topics">{topics.map((topic) => <option key={topic} value={topic} />)}</datalist>
      {error && <div className="inlineError" role="alert">{error}</div>}
      {joinHydration.phase === "loaded" && !joinIntervalValid && <div className="inlineError">{t(locale, "joinIntervalInvalid")}</div>}
      {tab === "topics" ? <TopicsView catalogs={catalogs} topics={topics} /> : <>
        <div className="tableHeader catalogGrid">
          <span />
          <span>{t(locale, "channelTitle")}</span>
          <span>{t(locale, "topic")}</span>
          <span>{t(locale, "status")}</span>
          <span>{tab === "scout" ? t(locale, "messages") : t(locale, "sent")}</span>
          <span>{t(locale, "lastActivity")}</span>
          <span>{t(locale, "membership")}</span>
          <span>{t(locale, "active")}</span>
        </div>
        {rows.length === 0 && <div className="emptyState">{t(locale, "emptyChannels")}</div>}
        {rows.map((channel) => {
          const removing = channel.status === "removing";
          const membershipSummary = formatCatalogMembershipSummary(locale, {
            member: channel.member ?? 0,
            pendingApproval: channel.pendingApproval ?? 0,
            joining: channel.joining ?? 0,
            leaving: channel.leaving ?? 0,
            failed: channel.failed ?? 0
          });
          const membershipCount = (channel.member ?? 0) + (channel.pendingApproval ?? 0) + (channel.joining ?? 0) + (channel.leaving ?? 0) + (channel.failed ?? 0);
          const leaving = (channel.leaving ?? 0) > 0;
          const schedule = channelJoinSchedule(channel);
          const status = schedule ? t(locale, schedule.status) : tCatalogStatus(locale, channel.status);
          return (
            <div className="tableRow catalogGrid" key={channel.id}>
              <input aria-label={t(locale, "selectChannel")} checked={selected.has(channel.id)} disabled={deleting || removing} onChange={() => setSelected((current) => toggleSelected(current, channel.id))} type="checkbox" />
              <div><strong>{channel.title}</strong><small>{channel.link}</small></div>
              <span>{channel.topic}</span>
              <div className="catalogStatus">
                <span className={`status ${channel.status}`} title={status}>{status}</span>
                {schedule ? <small title={schedule.joinNotBefore}>{schedule.joinNotBefore}</small> : membershipSummary && <small title={membershipSummary}>{membershipSummary}</small>}
              </div>
              <span>{tab === "scout" ? channel.messageCount : channel.sentCount}</span>
              <span>{channel.lastActivity}</span>
              <div className="catalogMembershipActions">
                <button disabled={deleting || removing || leaving || !joinActivationReady} onClick={() => void joinChannel(channel)} title={t(locale, "join")} type="button"><LogIn size={14} />{t(locale, "join")}</button>
                <button disabled={deleting || removing || leaving || membershipCount === 0} onClick={() => void leaveChannel(channel)} title={t(locale, "leave")} type="button"><LogOut size={14} />{leaving ? t(locale, "leaving") : t(locale, "leave")}</button>
              </div>
              <button aria-label={channel.title} className={channel.active && !removing ? "toggle on" : "toggle"} disabled={deleting || removing || !canActivateChannel(channel, joinActivationReady)} onClick={() => void toggleChannel(channel)} type="button" />
            </div>
          );
        })}
      </>}
      {confirmation && (
        <ConfirmationDialog
          cancelLabel={t(locale, "cancel")}
          confirmLabel={t(locale, "confirm")}
          message={confirmation.message}
          onCancel={() => resolveConfirmation(false)}
          onConfirm={() => resolveConfirmation(true)}
        />
      )}
    </section>
  );
}

function TopicsView({ catalogs, topics }: { catalogs: Record<CatalogKind, CatalogEntryWithJoinSchedule[]>; topics: string[] }) {
  const locale = useLocale();
  return <div className="topicsList">
    {topics.length === 0 && <div className="emptyState">{t(locale, "emptyTopics")}</div>}
    {topics.map((topic) => <div className="topicRow" key={topic}>
      <span><FolderSearch size={17} />{topic}</span>
      <small>{catalogs.outbound.filter((row) => row.topic === topic).length} / {catalogs.scout.filter((row) => row.topic === topic).length}</small>
    </div>)}
  </div>;
}

function AccountsView({ accounts, error, profiles, setAccounts, setError, onAssignProxy }: {
  accounts: AccountRow[];
  error: string;
  profiles: ProxyProfileRow[];
  setAccounts: Dispatch<SetStateAction<AccountRow[]>>;
  setError: (message: string) => void;
  onAssignProxy: (accountID: string, routeID: string) => Promise<void>;
}) {
  const locale = useLocale();
  const [resuming, setResuming] = useState<Set<string>>(new Set());
  const [assigningProxy, setAssigningProxy] = useState<Set<string>>(new Set());
  const changeRole = useMemo(() => createQueuedRoleChange(
    async (id, role) => {
      const updated = await SetAccountRole(id, role);
      return updated.role as AccountRole;
    },
    (id, role) => setAccounts((current) => applyAccountRole(current, id, role)),
    (err) => setError(err instanceof Error ? err.message : String(err))
  ), [setAccounts, setError]);

  const resumeLegacyPause = async (id: string) => {
    setError("");
    setResuming((current) => new Set(current).add(id));
    try {
      const updated = toAccount(await ResumeLegacyPausedAccount(id));
      setAccounts((current) => applyAccountSnapshot(current, updated));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setResuming((current) => {
        const next = new Set(current);
        next.delete(id);
        return next;
      });
    }
  };

  const assignProxy = async (accountID: string, routeID: string) => {
    setError("");
    setAssigningProxy((current) => new Set(current).add(accountID));
    try {
      await onAssignProxy(accountID, routeID);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setAssigningProxy((current) => {
        const next = new Set(current);
        next.delete(accountID);
        return next;
      });
    }
  };

  return (
    <section>
      {error && <div className="errorBanner">{error}</div>}
      <div className="cardsGrid">
      {accounts.map((account) => (
        <article className="glassCard accountCard" key={account.id}>
          <span
            aria-label={tAccountRuntimeStatus(locale, account.status, account.errorCode)}
            className={`status ${account.status}`}
            title={tAccountRuntimeStatus(locale, account.status, account.errorCode)}
          >
            {tAccountRuntimeStatus(locale, account.status, account.errorCode)}
          </span>
          <h2>{account.displayName || account.phoneMasked}</h2>
          {account.username && <p>{account.username}</p>}
          {tAccountRuntimeHint(locale, account.errorCode) && <p className="accountRuntimeHint">{tAccountRuntimeHint(locale, account.errorCode)}</p>}
          <p>{t(locale, "proxy")}: {proxyRouteLabel(locale, account.proxyProfileId ?? "", account.proxyRouteName || account.proxy)}</p>
          {account.proxyWarning === "route_capacity" && <p className="accountProxyWarning">{t(locale, "proxyRouteCapacity")}</p>}
          <label className="accountProxySelect">
            {t(locale, "proxyAssignment")}
            <select disabled={assigningProxy.has(account.id)} value={account.proxyProfileId ?? "system"} onChange={(event) => void assignProxy(account.id, event.target.value)}>
              {account.proxyProfileId === "" && <option disabled value="">{t(locale, "proxyUnassigned")}</option>}
              {profiles.map((profile) => <option disabled={proxyRouteUnavailable(profile, account.proxyProfileId ?? "system")} key={profile.id} value={profile.id}>{proxyRouteLabel(locale, profile.id, profile.name)} · {profile.usage}/{profile.capacity === 0 ? "∞" : profile.capacity}</option>)}
            </select>
          </label>
          <p className="accountNextDelivery">{t(locale, "nextDelivery")}: {t(locale, account.nextDelivery === "public" ? "deliveryPublic" : "deliveryPrivate")}</p>
          <div className="roleSegments" aria-label={t(locale, "accountRole")}>
            {roleOptions.map((option) => {
              const Icon = option.icon;
              return <button className={account.role === option.value ? "active" : ""} key={option.value} onClick={() => { setError(""); void changeRole(account.id, option.value, account.role); }} type="button" title={t(locale, option.value)}><Icon size={16} />{t(locale, option.value)}</button>;
            })}
          </div>
          {account.legacyPauseReviewRequired && (
            <button className="accountResumeButton" disabled={resuming.has(account.id)} onClick={() => void resumeLegacyPause(account.id)} type="button">
              {t(locale, "start")}
            </button>
          )}
        </article>
      ))}
      </div>
    </section>
  );
}

function KeywordsView({ keywordRows, onKeywordRowsChange, minusKeywords, onMinusKeywordsChange, sharedReply, onSharedReplyChange, privateReply, onPrivateReplyChange, deliveryMode, onDeliveryModeChange, settingsError }: {
  keywordRows: KeywordRow[];
  onKeywordRowsChange: Dispatch<SetStateAction<KeywordRow[]>>;
  minusKeywords: string[];
  onMinusKeywordsChange: Dispatch<SetStateAction<string[]>>;
  sharedReply: string;
  onSharedReplyChange: (value: string) => void;
  privateReply: string;
  onPrivateReplyChange: (value: string) => void;
  deliveryMode: KeywordDeliveryMode;
  onDeliveryModeChange: (value: KeywordDeliveryMode) => void;
  settingsError: string;
}) {
  const locale = useLocale();
  const [keywordTab, setKeywordTab] = useState<"positive" | "minus">("positive");
  const [keywordSearch, setKeywordSearch] = useState("");
  const [minusSearch, setMinusSearch] = useState("");
  const [showKeywordImport, setShowKeywordImport] = useState(false);
  const [showMinusImport, setShowMinusImport] = useState(false);
  const [minusImportInput, setMinusImportInput] = useState("");
  const [keywordImportInput, setKeywordImportInput] = useState("");
  const [keywordImportBusy, setKeywordImportBusy] = useState(false);
  const [keywordImportError, setKeywordImportError] = useState("");
  const [positiveCanonicals, setPositiveCanonicals] = useState<CanonicalKeyword[]>([]);
  const [expandedCanonicalIds, setExpandedCanonicalIds] = useState<ReadonlySet<string>>(new Set());
  const keywordExpressionLines = useMemo(
    () => parseKeywordExpressionLines(keywordImportInput),
    [keywordImportInput]
  );
  const operatorKeywordPreview = useMemo(
    () => keywordExpressionLines.filter(isKeywordOperatorExpression),
    [keywordExpressionLines]
  );
  const plainKeywordImportInput = useMemo(
    () => keywordExpressionLines.filter((value) => !isKeywordOperatorExpression(value)).join("\n"),
    [keywordExpressionLines]
  );

  const loadPositiveCanonicals = useCallback(async () => {
    try {
      const result = await ListCanonicalKeywords("positive");
      setPositiveCanonicals(normalizeCanonicalKeywords(result, "positive"));
    } catch (cause) {
      setKeywordImportError(cause instanceof Error ? cause.message : String(cause));
    }
  }, []);

  useEffect(() => {
    void loadPositiveCanonicals();
  }, [loadPositiveCanonicals]);

  const filteredKeywords = useMemo(
    () => filterKeywords(keywordRows, keywordSearch, sharedReply),
    [keywordRows, keywordSearch, sharedReply]
  );
  const keywordImportPreview = useMemo(
    () => parseBulkCanonicalValues(plainKeywordImportInput),
    [plainKeywordImportInput]
  );
  const filteredMinusKeywords = useMemo(() => {
    const query = minusSearch.trim().toLocaleLowerCase("ru-RU");
    return query ? minusKeywords.filter((keyword) => keyword.toLocaleLowerCase("ru-RU").includes(query)) : minusKeywords;
  }, [minusKeywords, minusSearch]);
  const minusImportPreview = useMemo(
    () => parseKeywordExpressionLines(minusImportInput),
    [minusImportInput]
  );

  const importPositiveCanons = async () => {
    if (!keywordImportPreview.values.length && !operatorKeywordPreview.length) {
      setKeywordImportError(t(locale, "analyticsBulkNoValid"));
      return;
    }
    setKeywordImportBusy(true);
    setKeywordImportError("");
    try {
      if (keywordImportPreview.values.length) {
        await (BulkImportCanonicalKeywords as unknown as (
          entries: CanonicalImportEntry[],
          keywordClass: string
        ) => Promise<CanonicalImportResult>)(keywordImportPreview.values, "positive");
      }
      onKeywordRowsChange((current) => mergeKeywords(
        activateImportedPositiveCanons(current, keywordImportPreview.values),
        operatorKeywordPreview
      ));
      await loadPositiveCanonicals();
      setKeywordImportInput("");
      setShowKeywordImport(false);
    } catch (cause) {
      setKeywordImportError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setKeywordImportBusy(false);
    }
  };

  const importMinusKeywords = () => {
    if (!minusImportPreview.length) {
      setKeywordImportError(t(locale, "analyticsBulkNoValid"));
      return;
    }
    onMinusKeywordsChange((current) => {
      const seen = new Set(current.map((value) => value.toLocaleLowerCase("ru-RU")));
      return [...current, ...minusImportPreview.filter((value) => {
        const normalized = value.toLocaleLowerCase("ru-RU");
        if (seen.has(normalized)) return false;
        seen.add(normalized);
        return true;
      })].slice(0, 1000);
    });
    setMinusImportInput("");
    setShowMinusImport(false);
  };

  const mutateCanonicalForms = async (operation: () => Promise<unknown>) => {
    setKeywordImportError("");
    try {
      await operation();
      await loadPositiveCanonicals();
    } catch (cause) {
      setKeywordImportError(cause instanceof Error ? cause.message : String(cause));
    }
  };

  const toggleCanonicalForms = (id: string) => {
    setExpandedCanonicalIds((current) => {
      const next = new Set(current);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const readKeywordImportFile = async (file: File | undefined) => {
    if (!file) return;
    try {
      setKeywordImportInput(await file.text());
      setKeywordImportError("");
    } catch (cause) {
      setKeywordImportError(cause instanceof Error ? cause.message : String(cause));
    }
  };

  return (
    <section className="glassPanel keywordPanel">
      {settingsError && <div className="inlineError">{settingsError}</div>}
      {keywordImportError && <div className="inlineError">{keywordImportError}</div>}
      <div className="keywordControls">
        <label className="sharedReplyField">
          {t(locale, "sharedReplyComments")}
          <input className="sharedReplyInput" type="text" value={sharedReply} onChange={(event) => onSharedReplyChange(event.target.value)} />
        </label>
        <label className="sharedReplyField">
          {t(locale, "privateReply")}
          <input className="sharedReplyInput" type="text" value={privateReply} onChange={(event) => onPrivateReplyChange(event.target.value)} />
        </label>
        <div className="keywordDeliveryMode">
          <span>{t(locale, "keywordDeliveryMode")}</span>
          <div className="roleSegments" role="group" aria-label={t(locale, "keywordDeliveryMode")}>
            <button className={deliveryMode === "private" ? "active" : ""} onClick={() => onDeliveryModeChange("private")} type="button">{t(locale, "keywordDeliveryPrivate")}</button>
            <button className={deliveryMode === "comments" ? "active" : ""} onClick={() => onDeliveryModeChange("comments")} type="button">{t(locale, "keywordDeliveryComments")}</button>
            <button className={deliveryMode === "both" ? "active" : ""} onClick={() => onDeliveryModeChange("both")} type="button">{t(locale, "keywordDeliveryBoth")}</button>
          </div>
        </div>
      </div>

      <div className="catalogTabs keywordTabs" role="tablist" aria-label={t(locale, "keywordSections")}>
        <button className={keywordTab === "positive" ? "active" : ""} onClick={() => setKeywordTab("positive")} role="tab" type="button">{t(locale, "keywords")}</button>
        <button className={keywordTab === "minus" ? "active" : ""} onClick={() => setKeywordTab("minus")} role="tab" type="button">{t(locale, "minusKeywords")}</button>
      </div>
      <div className="keywordListToolbar">
        <div className="keywordMeta">
          <span>{t(locale, "keywordCounter")}: {keywordTab === "positive" ? keywordRows.length : minusKeywords.length} / 1000</span>
          <span>{t(locale, "shown")}: {keywordTab === "positive" ? filteredKeywords.length : filteredMinusKeywords.length}</span>
        </div>
        <label className="searchField">
          <Search size={17} />
          <input
            value={keywordTab === "positive" ? keywordSearch : minusSearch}
            onChange={(event) => keywordTab === "positive" ? setKeywordSearch(event.target.value) : setMinusSearch(event.target.value)}
            placeholder={keywordTab === "positive" ? t(locale, "searchKeywords") : t(locale, "searchMinusKeywords")}
          />
        </label>
        <button className="secondaryButton keywordImportButton" onClick={() => {
          setKeywordImportError("");
          if (keywordTab === "positive") setShowKeywordImport(true);
          else setShowMinusImport(true);
        }} type="button">
          <Plus size={14} />{t(locale, "analyticsBulkAdd")}
        </button>
      </div>
      <div className="keywordTableScroll">
        {keywordTab === "positive" ? <KeywordTable
          rows={keywordRows}
          visibleRows={filteredKeywords}
          canonicalRows={positiveCanonicals}
          expandedCanonicalIds={expandedCanonicalIds}
          locale={locale}
          onRowsChange={onKeywordRowsChange}
          onToggleForms={toggleCanonicalForms}
          onAddForm={(id, form) => void mutateCanonicalForms(() => AddCanonicalForm(id, form))}
          onDetachForm={(id, form) => void mutateCanonicalForms(() => RemoveCanonicalForm(id, form))}
          onMoveForm={(targetId, form) => void mutateCanonicalForms(() => MoveCanonicalForm(targetId, form))}
        /> : <MinusKeywordTable
          locale={locale}
          rows={filteredMinusKeywords}
          onDelete={(keyword) => onMinusKeywordsChange((current) => current.filter((value) => value !== keyword))}
        />}
      </div>
      {showKeywordImport && <div aria-modal="true" className="analyticsRedesign__modalBackdrop" role="dialog" aria-label={t(locale, "analyticsBulkTitle")}>
        <div className="analyticsRedesign__bulkModal">
          <h2>{t(locale, "analyticsBulkTitle")}</h2>
          <label>{t(locale, "analyticsBulkInput")}<textarea autoFocus onChange={(event) => setKeywordImportInput(event.target.value)} placeholder={t(locale, "analyticsBulkPlaceholder")} value={keywordImportInput} /></label>
          <label className="analyticsRedesign__filePicker"><input accept=".txt,text/plain" aria-label={t(locale, "analyticsBulkChooseFile")} onChange={(event) => { void readKeywordImportFile(event.currentTarget.files?.[0]); event.currentTarget.value = ""; }} type="file" />{t(locale, "analyticsBulkChooseFile")}</label>
          <div className="analyticsRedesign__bulkPreview" role="status">
            <span>{t(locale, "analyticsBulkPreview")} {keywordImportPreview.values.length + operatorKeywordPreview.length}</span>
            {keywordImportPreview.skipped > 0 && <span>{t(locale, "analyticsBulkSkipped")} {keywordImportPreview.skipped}</span>}
          </div>
          <div className="analyticsRedesign__bulkActions">
            <button disabled={keywordImportBusy || (!keywordImportPreview.values.length && !operatorKeywordPreview.length)} onClick={() => void importPositiveCanons()} type="button">{t(locale, "analyticsBulkSave")}</button>
            <button disabled={keywordImportBusy} onClick={() => { setKeywordImportInput(""); setKeywordImportError(""); setShowKeywordImport(false); }} type="button">{t(locale, "analyticsBulkCancel")}</button>
          </div>
        </div>
      </div>}
      {showMinusImport && <div aria-modal="true" className="analyticsRedesign__modalBackdrop" role="dialog" aria-label={t(locale, "minusKeywords")}>
        <div className="analyticsRedesign__bulkModal">
          <h2>{t(locale, "minusKeywords")}</h2>
          <label>
            {t(locale, "minusKeywordExpression")}
            <textarea autoFocus onChange={(event) => setMinusImportInput(event.target.value)} placeholder={t(locale, "minusKeywordInputHint")} value={minusImportInput} />
          </label>
          <div className="analyticsRedesign__bulkPreview" role="status">
            <span>{t(locale, "analyticsBulkPreview")} {minusImportPreview.length}</span>
          </div>
          <div className="analyticsRedesign__bulkActions">
            <button disabled={!minusImportPreview.length} onClick={importMinusKeywords} type="button">{t(locale, "analyticsBulkSave")}</button>
            <button onClick={() => { setMinusImportInput(""); setShowMinusImport(false); }} type="button">{t(locale, "analyticsBulkCancel")}</button>
          </div>
        </div>
      </div>}
    </section>
  );
}

function MinusKeywordTable({ locale, rows, onDelete }: { locale: Locale; rows: string[]; onDelete: (keyword: string) => void }) {
  return <>
    <div className="tableHeader minusKeywordsGrid">
      <span>{t(locale, "minusKeywordExpression")}</span>
      <span>{t(locale, "actions")}</span>
    </div>
    {rows.map((keyword) => <div className="tableRow minusKeywordsGrid" key={keyword}>
      <strong>{keyword}</strong>
      <div className="keywordRowActions">
        <button aria-label={t(locale, "delete")} onClick={() => onDelete(keyword)} title={t(locale, "delete")} type="button"><Trash2 size={17} /></button>
      </div>
    </div>)}
  </>;
}

function SettingsHydrationState({ error }: { error: string }) {
  const locale = useLocale();
  return <section aria-busy={!error} className="glassPanel settingsPanel">
    {error
      ? <div className="inlineError">{error}</div>
      : <div aria-live="polite">{locale === "ru" ? "Загрузка сохранённых настроек..." : "Loading saved settings..."}</div>}
  </section>;
}

function SettingsView({ locale, onLocaleChange, theme, onThemeChange, proxyProfiles, proxyError, proxyBusyID, onSaveProxy, onDeleteProxy, onCheckProxy }: {
  locale: Locale;
  onLocaleChange: (locale: Locale) => void;
  theme: ThemePreference;
  onThemeChange: (theme: ThemePreference) => void;
  proxyProfiles: ProxyProfileRow[];
  proxyError: string;
  proxyBusyID: string;
  onSaveProxy: (draft: ProxyProfileDraft) => Promise<void>;
  onDeleteProxy: (id: string) => Promise<void>;
  onCheckProxy: (id: string) => Promise<void>;
}) {
  const [repliesPerMinute, setRepliesPerMinute] = useState(safeInitialAppSettings.repliesPerMinute);
  const [minIntervalSeconds, setMinIntervalSeconds] = useState(safeInitialAppSettings.minIntervalSeconds);
  const [joinIntervalMinMinutes, setJoinIntervalMinMinutes] = useState(safeInitialAppSettings.joinIntervalMinMinutes);
  const [joinIntervalMaxMinutes, setJoinIntervalMaxMinutes] = useState(safeInitialAppSettings.joinIntervalMaxMinutes);
  const [joinIntervalEnabled, setJoinIntervalEnabled] = useState(safeInitialAppSettings.joinIntervalEnabled);
  const [groupRestHours, setGroupRestHours] = useState(safeInitialAppSettings.groupRestHours);
  const [groupRestEnabled, setGroupRestEnabled] = useState(safeInitialAppSettings.groupRestEnabled);
  const [directMessages, setDirectMessages] = useState(safeInitialAppSettings.directMessages);
  const [proxy, setProxy] = useState(safeInitialAppSettings.proxy);
  const [hydration, setHydration] = useState(initialHydration);
  const [error, setError] = useState("");
  const [proxyStatus, setProxyStatus] = useState<ManagedProxyStatus>({
    mode: "tor_snowflake", state: "stopped", address: "127.0.0.1:19050", transport: "snowflake",
    lastError: "", restartCount: 0, updatedAt: "", autoRestart: true
  });
  const settingsReady = hydration.phase === "loaded";

  useEffect(() => {
    let canceled = false;
    GetAppSettings()
      .then((settings) => {
        if (canceled) {
          return;
        }
        setRepliesPerMinute(settings.repliesPerMinute);
        setMinIntervalSeconds(settings.minIntervalSeconds);
        setJoinIntervalMinMinutes(settings.joinIntervalMinMinutes);
        setJoinIntervalMaxMinutes(settings.joinIntervalMaxMinutes);
        setJoinIntervalEnabled(settings.joinIntervalEnabled);
        setGroupRestHours(settings.groupRestHours);
        setGroupRestEnabled(settings.groupRestEnabled);
        setDirectMessages(settings.directMessages);
        setProxy(settings.proxy);
        setHydration(successfulHydration);
      })
      .catch((err) => {
        if (!canceled) {
          setError(err instanceof Error ? err.message : String(err));
          setHydration(failedHydration);
        }
      });
    return () => {
      canceled = true;
    };
  }, []);

  useEffect(() => {
    let canceled = false;
    const refresh = () => GetProxyStatus()
      .then((status) => { if (!canceled) setProxyStatus(status); })
      .catch((err) => { if (!canceled) setError(err instanceof Error ? err.message : String(err)); });
    void refresh();
    const handle = window.setInterval(refresh, 2000);
    return () => { canceled = true; window.clearInterval(handle); };
  }, []);

  useEffect(() => {
    if (!shouldPersistHydratedSettings(hydration)) {
      return;
    }
	const version = hydration.editVersion;
    const handle = window.setTimeout(() => {
      SaveAppSettings({
        repliesPerMinute,
        minIntervalSeconds,
        joinIntervalMinMinutes,
        joinIntervalMaxMinutes,
        joinIntervalEnabled,
        groupRestHours,
        groupRestEnabled,
        directMessages,
        proxy,
        revision: 0
		}).then(() => setHydration((state) => persistedHydratedSettings(state, version)))
			.catch((err) => setError(err instanceof Error ? err.message : String(err)));
    }, 250);
    return () => window.clearTimeout(handle);
  }, [directMessages, groupRestEnabled, groupRestHours, hydration, joinIntervalEnabled, joinIntervalMaxMinutes, joinIntervalMinMinutes, minIntervalSeconds, proxy, repliesPerMinute]);

  const markEdit = () => setHydration(markHydratedEdit);

  return (
    <section aria-busy={!settingsReady} className="glassPanel settingsPanel">
      {(error || proxyError) && <div className="inlineError">{error || proxyError}</div>}
      <label className="localeControl">
        <span>{t(locale, "language")}</span>
        <select aria-label={t(locale, "language")} onChange={(event) => onLocaleChange(event.target.value as Locale)} value={locale}>
          <option value="ru">RU</option>
          <option value="en">EN</option>
        </select>
      </label>
      <label className="themeControl">
        <span>{t(locale, "appearance")}</span>
        <select aria-label={t(locale, "appearance")} onChange={(event) => onThemeChange(event.target.value as ThemePreference)} value={theme}>
          <option value="light">{t(locale, "lightTheme")}</option>
          <option value="dark">{t(locale, "darkTheme")}</option>
        </select>
      </label>
      <label>
        {t(locale, "repliesPerMinute")}
        <input disabled={!settingsReady} max={19} min={1} type="number" value={repliesPerMinute} onChange={(event) => { setRepliesPerMinute(Number(event.target.value)); markEdit(); }} />
      </label>
      <label>
        {t(locale, "minInterval")}
        <input disabled={!settingsReady} min={2} type="number" value={minIntervalSeconds} onChange={(event) => { setMinIntervalSeconds(Number(event.target.value)); markEdit(); }} />
      </label>
      <label>
        {t(locale, "groupRestHours")}
        <input disabled={!settingsReady} max={720} min={1} step={1} type="number" value={groupRestHours} onChange={(event) => {
          const value = Number(event.target.value);
          if (isValidGroupRestHours(value)) { setGroupRestHours(value); markEdit(); }
        }} />
      </label>
      <section className={`proxyStatus proxy-${proxyStatus.state}`} aria-live="polite">
        <div>
          <span>{t(locale, "proxy")}</span>
          <strong>{t(locale, "managedProxy")}</strong>
        </div>
        <div className="proxyStatusMeta">
          <span>{proxyStatusLabel(locale, proxyStatus.state)}</span>
          <span>{t(locale, "proxyTransport")}: {proxyTransportLabel(locale, proxyStatus.transport)}</span>
          <span>{proxyStatus.address}</span>
          {proxyStatus.restartCount > 0 && <span>{t(locale, "proxyRestarts")}: {proxyStatus.restartCount}</span>}
        </div>
        {proxyStatusErrorLabel(locale, proxyStatus.lastError) && <small>{proxyStatusErrorLabel(locale, proxyStatus.lastError)}</small>}
      </section>
      <ProxyProfilesPanel
        rows={proxyProfiles.map((profile) => ({ ...profile, name: proxyRouteLabel(locale, profile.id, profile.name) }))}
        busyID={proxyBusyID}
        onSave={onSaveProxy}
        onDelete={onDeleteProxy}
        onCheck={onCheckProxy}
        labels={{
          title: t(locale, "proxyProfiles"), add: t(locale, "proxyAdd"), route: t(locale, "proxyRoute"), endpoint: t(locale, "proxyEndpoint"),
          state: t(locale, "proxyState"), capacity: t(locale, "proxyCapacity"), actions: t(locale, "actions"), edit: t(locale, "edit"),
          remove: t(locale, "delete"), check: t(locale, "proxyCheck"), save: t(locale, "save"), cancel: t(locale, "cancel"),
          name: t(locale, "proxyName"), protocol: t(locale, "proxyProtocol"), host: t(locale, "proxyHost"), port: t(locale, "proxyPort"),
          username: t(locale, "proxyUsername"), password: t(locale, "proxyPassword"), enabled: t(locale, "enabled"),
          passwordConfigured: t(locale, "proxyPasswordConfigured"), clearPassword: t(locale, "proxyClearPassword")
        }}
      />
    </section>
  );
}
