import { describe, expect, test } from "vitest";
import * as keywordHelpers from "./keywords";
import { filterKeywords, isKeywordOperatorExpression, parseKeywordExpressionLines, parseKeywordInput, removeKeyword, renameKeyword } from "./keywords";

describe("keyword helpers", () => {
  test("keeps operator expressions and minus phrases intact per line", () => {
    expect(parseKeywordExpressionLines(" [машина едет] -(ремонт|сломалась) \n\"нет денег\"\nНЕТ ДЕНЕГ")).toEqual([
      "[машина едет] -(ремонт|сломалась)",
      "\"нет денег\"",
      "НЕТ ДЕНЕГ"
    ]);
    expect(isKeywordOperatorExpression("[машина едет]")).toBe(true);
    expect(isKeywordOperatorExpression("деньги")).toBe(false);
  });

  test("parseKeywordInput splits pasted text, removes duplicates, and limits to 1000 keywords", () => {
    const bulk = [
      "тест1, тест2; тест3. тест1",
      "слово",
      "длинное словосочетание",
      ...Array.from({ length: 1100 }, (_, index) => `ключ-${index}`)
    ].join("\n");

    const parsed = parseKeywordInput(bulk);

    expect(parsed).toHaveLength(1000);
    expect(parsed.slice(0, 6)).toEqual(["тест1", "тест2", "тест3", "слово", "длинное", "словосочетание"]);
    expect(parsed.filter((keyword) => keyword === "тест1")).toHaveLength(1);
  });

  test("filterKeywords searches by keyword and shared reply", () => {
    const rows = [
      { keyword: "тест1", dm: true },
      { keyword: "зимняя шутка", dm: false },
      { keyword: "длинное словосочетание", dm: true }
    ];

    expect(filterKeywords(rows, "зимн", "Общий ответ")).toEqual([{ keyword: "зимняя шутка", dm: false }]);
    expect(filterKeywords(rows, "общий", "Общий ответ")).toEqual(rows);
  });

  test("renameKeyword trims and renames one keyword", () => {
    const rows = [{ keyword: "тест1", dm: true }];
    expect(renameKeyword(rows, "тест1", "  новый ключ  ")).toEqual({
      rows: [{ keyword: "новый ключ", dm: true }],
      error: null
    });
  });

  test("renameKeyword rejects empty and duplicate values", () => {
    const rows = [
      { keyword: "тест1", dm: true },
      { keyword: "тест2", dm: false }
    ];
    expect(renameKeyword(rows, "тест1", " ").error).toBe("empty");
    expect(renameKeyword(rows, "тест1", "ТЕСТ2").error).toBe("duplicate");
  });

  test("removeKeyword removes only the selected row", () => {
    const rows = [
      { keyword: "тест1", dm: true },
      { keyword: "тест2", dm: false }
    ];
    expect(removeKeyword(rows, "тест1")).toEqual([{ keyword: "тест2", dm: false }]);
  });

  test("clearKeywords removes every keyword in one operation", () => {
    const helpers = keywordHelpers as unknown as {
      clearKeywords?: (rows: Array<{ keyword: string; dm: boolean }>) => Array<{ keyword: string; dm: boolean }>;
    };

    expect(helpers.clearKeywords).toBeTypeOf("function");
    expect(helpers.clearKeywords?.([
      { keyword: "first", dm: true },
      { keyword: "second", dm: false }
    ])).toEqual([]);
  });

  test("maps legacy and explicit direct-message keyword settings", () => {
    const helpers = keywordHelpers as unknown as {
      keywordRowsFromSettings?: (keywords: string[], directMessageKeywords: string[] | null | undefined) => Array<{ keyword: string; dm: boolean }>;
      directMessageKeywordsFromRows?: (rows: Array<{ keyword: string; dm: boolean }>) => string[];
    };

    expect(helpers.keywordRowsFromSettings).toBeTypeOf("function");
    expect(helpers.directMessageKeywordsFromRows).toBeTypeOf("function");
    expect(helpers.keywordRowsFromSettings?.(["слил", "тест1"], undefined)).toEqual([
      { keyword: "слил", dm: true },
      { keyword: "тест1", dm: true }
    ]);
    expect(helpers.keywordRowsFromSettings?.(["слил", "тест1"], [])).toEqual([
      { keyword: "слил", dm: false },
      { keyword: "тест1", dm: false }
    ]);
    expect(helpers.directMessageKeywordsFromRows?.([
      { keyword: "слил", dm: false },
      { keyword: "тест1", dm: true }
    ])).toEqual(["тест1"]);
  });
});
