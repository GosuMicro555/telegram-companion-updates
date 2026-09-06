export type CanonicalClass = "neutral" | "positive" | "negative" | "service";

export type CanonicalForm = {
  value: string;
  frequency: number;
};

export type CanonicalKeyword = {
  id: string;
  canonical: string;
  language: "ru" | "en";
  class: CanonicalClass;
  frequency: number;
  frequencyDelta: number;
  messageCount: number;
  lastSeen: string;
  forms: CanonicalForm[];
  triggerActive: boolean;
};

// Kept as a compatibility type for the existing presentation reducer while
// the canonical screen no longer renders comparison analytics.
export type TopicSummary = {
  topic: string;
  count: number;
  distinctCandidateCount: number;
  topPhrases: unknown[];
  relativeShare: number;
};

export type CanonicalAnalyticsDependencies = {
  list(keywordClass: CanonicalClass): Promise<unknown>;
  classify(id: string, keywordClass: CanonicalClass): Promise<void>;
  setTrigger(id: string, active: boolean): Promise<void>;
  remove(id: string): Promise<void>;
  clear(keywordClass: CanonicalClass): Promise<void>;
  detachForm(id: string, form: string): Promise<void>;
  moveForm(targetId: string, form: string): Promise<void>;
  addForm(targetId: string, form: string): Promise<void>;
  analyze(): Promise<void>;
  syncTriggers(): Promise<void>;
};

export type CanonicalAnalyticsSink = {
  onRows(rows: CanonicalKeyword[]): void;
  onError(message: string): void;
};

export function createCanonicalAnalyticsController(dependencies: CanonicalAnalyticsDependencies, sink: CanonicalAnalyticsSink) {
  let analysisRunning = false;
  const refresh = async (keywordClass: CanonicalClass) => {
    try {
      const result = await dependencies.list(keywordClass);
      sink.onRows(normalizeCanonicalKeywords(result, keywordClass));
    } catch (cause) {
      sink.onError(messageOf(cause));
    }
  };

  const mutate = async (keywordClass: CanonicalClass, operation: () => Promise<void>) => {
    try {
      await operation();
      await refresh(keywordClass);
      return true;
    } catch (cause) {
      sink.onError(messageOf(cause));
      return false;
    }
  };

  return {
    load: refresh,
    classify: (id: string, keywordClass: CanonicalClass, currentClass: CanonicalClass = keywordClass) => mutate(currentClass, () => dependencies.classify(id, keywordClass)),
    setTrigger: (id: string, active: boolean, keywordClass: CanonicalClass) => mutate(keywordClass, () => dependencies.setTrigger(id, active)),
    remove: (id: string, keywordClass: CanonicalClass) => mutate(keywordClass, () => dependencies.remove(id)),
    clear: (keywordClass: CanonicalClass) => mutate(keywordClass, () => dependencies.clear(keywordClass)),
    detachForm: (id: string, form: string, keywordClass: CanonicalClass) => mutate(keywordClass, () => dependencies.detachForm(id, form)),
    moveForm: (targetId: string, form: string, keywordClass: CanonicalClass) => mutate(keywordClass, () => dependencies.moveForm(targetId, form)),
    addForm: (targetId: string, form: string, keywordClass: CanonicalClass) => mutate(keywordClass, () => dependencies.addForm(targetId, form)),
    analyze: async (keywordClass: CanonicalClass) => {
      if (analysisRunning) return false;
      analysisRunning = true;
      try {
        return await mutate(keywordClass, async () => {
          await dependencies.analyze();
          await dependencies.syncTriggers();
        });
      } finally {
        analysisRunning = false;
      }
    }
  };
}

export function filterCanonicalKeywords(rows: CanonicalKeyword[], query: string): CanonicalKeyword[] {
  const needle = query.trim().toLocaleLowerCase("ru-RU");
  if (!needle) return rows;
  return rows.filter((row) =>
    row.canonical.toLocaleLowerCase("ru-RU").includes(needle) ||
    row.forms.some((form) => form.value.toLocaleLowerCase("ru-RU").includes(needle))
  );
}

export function normalizeCanonicalKeywords(value: unknown, fallbackClass: CanonicalClass): CanonicalKeyword[] {
  if (!Array.isArray(value)) return [];
  return value.map((row) => normalizeCanonicalKeyword(row, fallbackClass)).filter((row): row is CanonicalKeyword => row !== null);
}

function normalizeCanonicalKeyword(value: unknown, fallbackClass: CanonicalClass): CanonicalKeyword | null {
  if (!value || typeof value !== "object") return null;
  const row = value as Record<string, unknown>;
  const id = text(row, "id", "ID");
  const canonical = text(row, "canonical", "Canonical");
  if (!id || !canonical) return null;
  const language = text(row, "language", "Language") === "en" ? "en" : "ru";
  const keywordClass = text(row, "class", "Class");
  return {
    id,
    canonical,
    language,
    class: keywordClass === "positive" || keywordClass === "negative" ? keywordClass : fallbackClass,
    frequency: number(row, "frequency", "Frequency"),
    frequencyDelta: number(row, "frequencyDelta", "FrequencyDelta"),
    messageCount: number(row, "messageCount", "MessageCount"),
    lastSeen: text(row, "lastSeen", "LastSeen"),
    forms: normalizeForms(row.forms ?? row.Forms),
    triggerActive: Boolean(row.triggerActive ?? row.TriggerActive)
  };
}

function normalizeForms(value: unknown): CanonicalForm[] {
  if (!Array.isArray(value)) return [];
  return value.flatMap((item) => {
    if (!item || typeof item !== "object") return [];
    const form = item as Record<string, unknown>;
    const value = text(form, "value", "Value");
    return value ? [{ value, frequency: number(form, "frequency", "Frequency") }] : [];
  });
}

function text(record: Record<string, unknown>, lower: string, upper: string): string {
  const value = record[lower] ?? record[upper];
  return typeof value === "string" ? value : "";
}

function number(record: Record<string, unknown>, lower: string, upper: string): number {
  const value = record[lower] ?? record[upper];
  return typeof value === "number" ? value : 0;
}

function messageOf(cause: unknown): string {
  return cause instanceof Error ? cause.message : String(cause);
}
