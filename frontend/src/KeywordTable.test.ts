import { describe, expect, test } from "vitest";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { readFileSync } from "node:fs";
import * as keywordTable from "./KeywordTable";
import { KeywordTable } from "./KeywordTable";
import type { CanonicalKeyword } from "./analyticsController";
import type { KeywordRow } from "./keywords";

describe("keyword table delete actions", () => {
  test("keeps positive canonical values read-only", () => {
    const source = readFileSync(new URL("./KeywordTable.tsx", import.meta.url), "utf8");

    expect(source).not.toContain("renameKeyword");
    expect(source).not.toContain("editingKeyword");
    expect(source).not.toContain("<Pencil");
  });

  test("deletes the visible row immediately without a confirmation action", () => {
    const actions = keywordTable as unknown as {
      applyKeywordDeleteAction?: (
        rows: KeywordRow[],
        action: { type: "delete"; keyword: string }
      ) => KeywordRow[];
    };
    const rows: KeywordRow[] = [
      { keyword: "visible", dm: true },
      { keyword: "hidden", dm: false }
    ];

    expect(actions.applyKeywordDeleteAction).toBeTypeOf("function");
    const result = actions.applyKeywordDeleteAction?.(rows, { type: "delete", keyword: "visible" });
    expect(result).toEqual([{ keyword: "hidden", dm: false }]);
  });

  test("shows canonical form counts and the positive-form editor for active triggers", () => {
    const canonicals: CanonicalKeyword[] = [
      {
        id: "money",
        canonical: "money",
        language: "en",
        class: "positive",
        frequency: 3,
        frequencyDelta: 0,
        messageCount: 2,
        lastSeen: "",
        triggerActive: true,
        forms: [
          { value: "money", frequency: 3 },
          { value: "monies", frequency: 2 }
        ]
      },
      {
        id: "support",
        canonical: "support",
        language: "en",
        class: "positive",
        frequency: 1,
        frequencyDelta: 0,
        messageCount: 1,
        lastSeen: "",
        triggerActive: false,
        forms: [{ value: "support", frequency: 1 }]
      }
    ];

    const markup = renderToStaticMarkup(createElement(KeywordTable, {
      rows: [{ keyword: "money", dm: true }],
      visibleRows: [{ keyword: "money", dm: true }],
      canonicalRows: canonicals,
      expandedCanonicalIds: new Set(["money"]),
      locale: "en",
      onRowsChange: () => undefined,
      onToggleForms: () => undefined,
      onAddForm: () => undefined,
      onDetachForm: () => undefined,
      onMoveForm: () => undefined
    }));

    expect(markup).toContain(">Forms<");
    expect(markup).toContain('aria-label="Forms: 2"');
    expect(markup).toContain(">monies<");
    expect(markup).toContain('aria-label="New form"');
    expect(markup).toContain(">support<");
  });
});
