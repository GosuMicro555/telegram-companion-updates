import { createElement } from "react";
import { createRoot } from "react-dom/client";
import "../src/styles.css";

const query = new URLSearchParams(window.location.search);
const state = query.get("state") || "initial";
const scale = Number(query.get("scale") || 100);
document.documentElement.style.setProperty("--ui-scale", String(scale / 100));
document.documentElement.dataset.uiScale = String(scale);
window.addEventListener("error", (event) => { document.body.dataset.geometryError = event.error?.stack || event.message; });
window.addEventListener("unhandledrejection", (event) => { document.body.dataset.geometryError = event.reason?.stack || String(event.reason); });
const canonical = (suffix, keywordClass, language = "ru") => ({
  id: `canonical-${keywordClass}-${suffix}`,
  canonical: language === "ru" ? `слово${suffix}` : `keyword${suffix}`,
  language,
  class: keywordClass,
  frequency: 128,
  frequencyDelta: suffix === "1" ? 12 : -3,
  messageCount: 42,
  lastSeen: "2026-07-15T09:45:00Z",
  triggerActive: keywordClass === "positive" && suffix === "1",
  forms: [
    { value: language === "ru" ? `слова${suffix}` : `keywords${suffix}`, frequency: 71 },
    { value: language === "ru" ? `словом${suffix}` : `keyworded${suffix}`, frequency: 24 }
  ]
});
const rowsByClass = {
  neutral: Array.from({ length: 24 }, (_, index) => canonical(String(index + 1), "neutral", index % 2 ? "en" : "ru")),
  positive: Array.from({ length: 24 }, (_, index) => canonical(String(index + 1), "positive", index % 2 ? "en" : "ru")),
  negative: Array.from({ length: 24 }, (_, index) => canonical(String(index + 1), "negative", index % 2 ? "en" : "ru"))
};

window.go = { wails: { Bindings: {
  ListCanonicalKeywords: async (keywordClass) => keywordClass === state ? rowsByClass[keywordClass] || [] : [],
  AnalyzeCanonicalKeywords: async () => ({ messageCount: 128450, keywordCount: 3842 }),
  ClassifyCanonicalKeyword: async () => undefined,
  SetCanonicalKeywordTrigger: async () => undefined,
  DeleteCanonicalKeyword: async () => undefined,
  ClearCanonicalKeywords: async () => undefined,
  RemoveCanonicalForm: async () => undefined,
  MoveCanonicalForm: async () => undefined,
  AddCanonicalForm: async () => undefined,
  SyncCanonicalTriggers: async () => undefined,
  GetAnalyticsTablePreferences: async () => ({}),
  SaveAnalyticsTablePreferences: async () => undefined,
  GetCanonicalAnalyticsStatus: async () => ({
    running: true,
    collecting: false,
    intervalMinutes: 10,
    lastRunAt: "2026-07-15T09:30:00Z",
    nextRunAt: "2026-07-15T09:40:00Z",
    allTimeMessages: 128450,
    allTimeKeywords: 3842,
    allTimeGroups: 126,
    latestCollection: { newMessages: 74, extractedWords: 201, processedGroups: 4 }
  }),
  GetKeywordSettings: async () => ({ keywords: ["слово1"], directMessageKeywords: [] })
} } };
window.runtime = { ClipboardSetText: async () => true };

const { AnalyticsView } = await import("../src/AnalyticsView.tsx");
document.documentElement.style.setProperty("--ui-scale", String(scale / 100));
document.documentElement.dataset.uiScale = String(scale);
const root = createRoot(document.getElementById("root"));
root.render(createElement("div", { className: "shell" },
createElement("aside", { className: "sidebar" }, createElement("div", { className: "brand" }, "Telegram Companion")),
createElement("main", { className: "content" }, createElement(AnalyticsView, {
  locale: "ru",
  topics: ["Финансы", "Работа", "Услуги"],
  existingKeywords: [],
  onKeywordsChange: () => undefined
}))));

await waitFor(() => document.querySelector(".analyticsRedesign"));
if (state === "initial") {
  await waitFor(() => document.querySelector(".analyticsRedesign__tableWrap .analyticsRedesign__empty"));
} else {
  const labels = { neutral: "Все keyword", positive: "Позитивные", negative: "Негативные" };
  const tab = [...document.querySelectorAll(".analyticsRedesign__tabs button")].find((button) => button.textContent.includes(labels[state]));
  if (!tab) throw new Error(`canonical tab ${state} was not found`);
  tab.click();
  await waitFor(() => document.querySelectorAll(".analyticsRedesign__row").length >= 2);
  document.querySelector(".analyticsRedesign__formsButton").click();
  await waitFor(() => document.querySelector(".analyticsRedesign__forms"));
  await assertColumnReorder();
}

await document.fonts.ready;
await nextFrame();
await nextFrame();
document.body.dataset.geometry = encodeURIComponent(JSON.stringify(measureGeometry(state)));

function nextFrame() {
  return new Promise((resolve) => requestAnimationFrame(() => resolve()));
}

async function waitFor(read, timeout = 4000) {
  const deadline = performance.now() + timeout;
  while (performance.now() < deadline) {
    const value = read();
    if (value) return value;
    await nextFrame();
  }
  throw new Error(`geometry state ${state} did not become ready`);
}

async function assertColumnReorder() {
  const headers = [...document.querySelectorAll(".analyticsRedesign__header [role=columnheader]")];
  if (headers.length < 6) throw new Error("table headers were not rendered for reorder verification");
  const dragged = headers[5];
  const target = headers[0];
  const label = dragged.textContent;
  dragged.dispatchEvent(new DragEvent("dragstart", { bubbles: true, cancelable: true }));
  await nextFrame();
  await nextFrame();
  target.dispatchEvent(new DragEvent("dragover", { bubbles: true, cancelable: true }));
  target.dispatchEvent(new DragEvent("drop", { bubbles: true, cancelable: true }));
  dragged.dispatchEvent(new DragEvent("dragend", { bubbles: true, cancelable: true }));
  await nextFrame();
  await nextFrame();
  const first = document.querySelector(".analyticsRedesign__header [role=columnheader]")?.textContent;
  if (first !== label) throw new Error(`column reorder failed: expected ${label}, got ${first}`);
}

function measureGeometry(activeState) {
  const panel = document.querySelector(".analyticsRedesign");
  const panelRect = panel.getBoundingClientRect();
  const viewport = { left: 0, right: window.innerWidth };
  const rect = (node) => {
    const value = node.getBoundingClientRect();
    return { left: value.left, right: value.right, top: value.top, bottom: value.bottom, width: value.width, height: value.height };
  };
  const inside = (child, parent) => child.left >= parent.left - 1 && child.right <= parent.right + 1;
  const clippingOverflow = new Set(["auto", "clip", "hidden", "scroll"]);
  const visibleRect = (node) => {
    if (!node.getClientRects().length) return null;
    const value = rect(node);
    for (let ancestor = node.parentElement; ancestor; ancestor = ancestor.parentElement) {
      const style = getComputedStyle(ancestor);
      const bounds = rect(ancestor);
      if (clippingOverflow.has(style.overflowX)) {
        value.left = Math.max(value.left, bounds.left);
        value.right = Math.min(value.right, bounds.right);
      }
      if (clippingOverflow.has(style.overflowY)) {
        value.top = Math.max(value.top, bounds.top);
        value.bottom = Math.min(value.bottom, bounds.bottom);
      }
      if (value.right <= value.left || value.bottom <= value.top) return null;
    }
    value.width = value.right - value.left;
    value.height = value.bottom - value.top;
    return value;
  };
  const interactiveNodes = [...panel.querySelectorAll("button,input,select")]
    .map((node) => ({ node, visible: visibleRect(node) }))
    .filter((entry) => entry.visible);
  const interactive = interactiveNodes.map((entry) => entry.visible);
  const buttons = interactiveNodes.filter((entry) => entry.node.matches("button")).map((entry) => entry.node);
  const controlGroups = [...panel.querySelectorAll(".analyticsRedesign__commands,.analyticsRedesign__forms,.analyticsRedesign__tableFooter")];
  const required = {
    initial: Boolean(panel.querySelector(".analyticsRedesign__tableWrap .analyticsRedesign__empty")) && Boolean(panel.querySelector('input[type="range"]')) && Boolean(panel.querySelector(".analyticsRedesign__metricGrid")),
    neutral: panel.querySelectorAll(".analyticsRedesign__row").length >= 2 && panel.querySelectorAll(".analyticsRedesign__actions button").length >= 6 && Boolean(panel.querySelector(".analyticsRedesign__forms")) && Boolean(panel.querySelector(".analyticsRedesign__tableFooter nav")),
    positive: panel.querySelectorAll(".analyticsRedesign__row").length >= 2 && panel.querySelectorAll(".analyticsRedesign__actions button").length >= 8 && [...panel.querySelectorAll(".analyticsRedesign__commands button")].some((button) => button.textContent.includes("Добавить все")) && Boolean(panel.querySelector(".analyticsRedesign__forms")) && Boolean(panel.querySelector(".analyticsRedesign__tableFooter nav")),
    negative: panel.querySelectorAll(".analyticsRedesign__row").length >= 2 && panel.querySelectorAll(".analyticsRedesign__actions button").length >= 6 && Boolean(panel.querySelector(".analyticsRedesign__forms")) && Boolean(panel.querySelector(".analyticsRedesign__tableFooter nav"))
  };
  const pass = inside(panelRect, viewport)
    && interactive.every((value) => value.width > 0 && value.height > 0 && inside(value, panelRect) && inside(value, viewport))
    && buttons.every((button) => button.scrollWidth <= button.clientWidth + 1 && button.scrollHeight <= button.clientHeight + 1)
    && controlGroups.every((group) => group.scrollWidth <= group.clientWidth + 1)
    && required[activeState];
  return {
    pass,
    state: activeState,
    innerWidth: window.innerWidth,
    innerHeight: window.innerHeight,
    devicePixelRatio: window.devicePixelRatio,
    panel: rect(panel),
    containers: [".shell", ".content", ".analyticsRedesign", ".analyticsRedesign__tableFrame", ".analyticsRedesign__tableWrap"].map((selector) => {
      const node = document.querySelector(selector);
      const style = node ? getComputedStyle(node) : null;
      return { selector, rect: node ? rect(node) : null, overflowX: style?.overflowX, width: style?.width };
    }),
    interactive,
    required,
    controlGroups: controlGroups.map((group) => ({ className: group.className, scrollWidth: group.scrollWidth, clientWidth: group.clientWidth })),
    buttonOverflows: buttons.filter((button) => button.scrollWidth > button.clientWidth + 1 || button.scrollHeight > button.clientHeight + 1).map((button) => ({ text: button.textContent, ariaLabel: button.getAttribute("aria-label"), scrollWidth: button.scrollWidth, clientWidth: button.clientWidth, scrollHeight: button.scrollHeight, clientHeight: button.clientHeight }))
  };
}
