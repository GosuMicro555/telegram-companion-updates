import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, test } from "vitest";

const appSource = readFileSync(fileURLToPath(new URL("./App.tsx", import.meta.url)), "utf8");
const styles = readFileSync(fileURLToPath(new URL("./styles.css", import.meta.url)), "utf8");
const keywordsView = appSource.slice(appSource.indexOf("function KeywordsView"), appSource.indexOf("function SettingsView"));

describe("keywords layout", () => {
  test("keeps replies and search inline while opening bulk import in a modal", () => {
    expect(keywordsView).toContain('className="sharedReplyInput"');
    expect(keywordsView).toContain("<input");
    expect(keywordsView).toContain('aria-modal="true"');
    expect(keywordsView).toContain('t(locale, "analyticsBulkInput")');
    expect(keywordsView).not.toContain('t(locale, "newKeyword")');
    expect(keywordsView).not.toContain('t(locale, "bulkKeywords")');
    expect(keywordsView).not.toContain('t(locale, "deleteAll")');
    expect(keywordsView).toContain('className="keywordTableScroll"');
  });

  test("hides the keywords subtitle while retaining the page subtitle behavior elsewhere", () => {
    expect(appSource).toContain('section !== "keywords" && <p>{t(locale, leads[section])}</p>');
  });

  test("uses a small equal shell inset and lets the keyword table fill its panel", () => {
    expect(styles).toMatch(/\.sidebar\s*\{[^}]*padding:\s*12px;/s);
    expect(styles).toMatch(/\.content\s*\{[^}]*padding:\s*12px;/s);
    expect(styles).toMatch(/\.keywordPanel\s*\{[^}]*flex:\s*1[^}]*display:\s*flex/s);
    expect(styles).toMatch(/\.keywordTableScroll\s*\{[^}]*flex:\s*1[^}]*overflow-y:\s*auto/s);
  });

  test("gives comment and private replies equal input columns", () => {
    expect(styles).toMatch(/\.keywordControls\s*\{[^}]*grid-template-columns:\s*repeat\(2,\s*minmax\(0,\s*1fr\)\)/s);
  });
});
