export type HydrationPhase = "loading" | "loaded" | "failed";

export type SettingsHydration = {
  phase: HydrationPhase;
  editVersion: number;
  savedVersion: number;
};

export const initialHydration: SettingsHydration = { phase: "loading", editVersion: 0, savedVersion: 0 };

export const safeInitialKeywordSettings = {
  keywords: [] as string[],
  minusKeywords: [] as string[],
  sharedReply: "",
  deliveryMode: "comments" as const,
  directMessageKeywords: [] as string[]
};

export const safeInitialAppSettings = {
  repliesPerMinute: 19,
  minIntervalSeconds: 2,
  joinIntervalMinMinutes: 10,
  joinIntervalMaxMinutes: 60,
  joinIntervalEnabled: true,
  groupRestHours: 36,
  groupRestEnabled: true,
  directMessages: false,
  proxy: ""
};

export function isValidJoinIntervalRange(minimum: number, maximum: number): boolean {
  return Number.isInteger(minimum)
    && Number.isInteger(maximum)
    && minimum >= 0
    && maximum <= 3000
    && minimum <= maximum;
}

function clampJoinIntervalMinute(value: number): number {
  if (!Number.isFinite(value)) return 0;
  return Math.min(3000, Math.max(0, Math.trunc(value)));
}

export function normalizeJoinIntervalRange(minimum: number, maximum: number): { minimum: number; maximum: number } {
  const boundedMinimum = clampJoinIntervalMinute(minimum);
  const boundedMaximum = clampJoinIntervalMinute(maximum);
  return boundedMinimum <= boundedMaximum
    ? { minimum: boundedMinimum, maximum: boundedMaximum }
    : { minimum: boundedMaximum, maximum: boundedMinimum };
}

export function nextJoinIntervalRange(
  minimum: number,
  maximum: number,
  field: "minimum" | "maximum",
  value: number
): { minimum: number; maximum: number } {
  const next = clampJoinIntervalMinute(value);
  if (field === "minimum") {
    return { minimum: next, maximum: Math.max(next, clampJoinIntervalMinute(maximum)) };
  }
  return { minimum: Math.min(clampJoinIntervalMinute(minimum), next), maximum: next };
}

export function isValidGroupRestHours(value: number): boolean {
  return Number.isInteger(value) && value >= 1 && value <= 720;
}

export function successfulHydration(state: SettingsHydration): SettingsHydration {
  return { ...state, phase: "loaded", savedVersion: state.editVersion };
}

export function completeHydration(state: SettingsHydration, preservePendingEdits: boolean): SettingsHydration {
  if (preservePendingEdits) {
    return { ...state, phase: "loaded" };
  }
  return successfulHydration(state);
}

export function failedHydration(state: SettingsHydration): SettingsHydration {
  return { ...state, phase: "failed" };
}

export function markHydratedEdit(state: SettingsHydration): SettingsHydration {
  return { ...state, editVersion: state.editVersion + 1 };
}

export function shouldPersistHydratedSettings(state: SettingsHydration): boolean {
  return state.phase === "loaded" && state.editVersion > state.savedVersion;
}

export function persistedHydratedSettings(state: SettingsHydration, version: number): SettingsHydration {
  return { ...state, savedVersion: Math.max(state.savedVersion, version) };
}
