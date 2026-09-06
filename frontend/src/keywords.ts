export type KeywordRow = {
  keyword: string;
  dm: boolean;
};

export type KeywordMutationResult = {
  rows: KeywordRow[];
  error: "empty" | "duplicate" | null;
};

const MAX_KEYWORDS = 1000;
const SPLIT_PATTERN = /[\s,;.]+/u;

export function parseKeywordExpressionLines(input: string, limit = MAX_KEYWORDS): string[] {
  const seen = new Set<string>();
  const result: string[] = [];
  for (const rawLine of input.split(/\r\n?|\n/u)) {
    const expression = rawLine.trim().replace(/\s+/gu, " ");
    const normalized = expression.toLocaleLowerCase("ru-RU");
    if (!expression || seen.has(normalized)) continue;
    seen.add(normalized);
    result.push(expression);
    if (result.length >= limit) break;
  }
  return result;
}

export function isKeywordOperatorExpression(value: string): boolean {
  return /(?:[!"\[\]|]|(?:^|\s)[+-]\S)/u.test(value);
}

export function parseKeywordInput(input: string, limit = MAX_KEYWORDS): string[] {
  const seen = new Set<string>();
  const result: string[] = [];

  for (const rawToken of input.split(SPLIT_PATTERN)) {
    const keyword = rawToken.trim();
    const normalized = keyword.toLocaleLowerCase("ru-RU");
    if (!keyword || seen.has(normalized)) {
      continue;
    }
    seen.add(normalized);
    result.push(keyword);
    if (result.length >= limit) {
      break;
    }
  }

  return result;
}

export function mergeKeywords(existing: KeywordRow[], incoming: string[], defaultDm = true, limit = MAX_KEYWORDS): KeywordRow[] {
  const rows = [...existing];
  const seen = new Set(existing.map((row) => row.keyword.toLocaleLowerCase("ru-RU")));

  for (const keyword of incoming) {
    const normalized = keyword.toLocaleLowerCase("ru-RU");
    if (seen.has(normalized)) {
      continue;
    }
    rows.push({ keyword, dm: defaultDm });
    seen.add(normalized);
    if (rows.length >= limit) {
      break;
    }
  }

  return rows;
}

export function keywordRowsFromSettings(keywords: string[] | null | undefined, directMessageKeywords: string[] | null | undefined): KeywordRow[] {
  const safeKeywords = Array.isArray(keywords) ? keywords : [];
  if (directMessageKeywords == null) {
    return safeKeywords.map((keyword) => ({ keyword, dm: true }));
  }

  const enabled = new Set(directMessageKeywords.map((keyword) => keyword.toLocaleLowerCase("ru-RU")));
  return safeKeywords.map((keyword) => ({
    keyword,
    dm: enabled.has(keyword.toLocaleLowerCase("ru-RU"))
  }));
}

export function directMessageKeywordsFromRows(rows: KeywordRow[]): string[] {
  return rows.filter((row) => row.dm).map((row) => row.keyword);
}

export function filterKeywords(rows: KeywordRow[], query: string, sharedReply: string): KeywordRow[] {
  const needle = query.trim().toLocaleLowerCase("ru-RU");
  if (!needle) {
    return rows;
  }

  const replyMatches = sharedReply.toLocaleLowerCase("ru-RU").includes(needle);
  if (replyMatches) {
    return rows;
  }

  return rows.filter((row) => row.keyword.toLocaleLowerCase("ru-RU").includes(needle));
}

export function renameKeyword(rows: KeywordRow[], currentKeyword: string, nextKeyword: string): KeywordMutationResult {
  const keyword = nextKeyword.trim();
  if (!keyword) return { rows, error: "empty" };
  const normalized = keyword.toLocaleLowerCase("ru-RU");
  const duplicate = rows.some((row) =>
    row.keyword !== currentKeyword && row.keyword.toLocaleLowerCase("ru-RU") === normalized
  );
  if (duplicate) return { rows, error: "duplicate" };
  return {
    rows: rows.map((row) => row.keyword === currentKeyword ? { ...row, keyword } : row),
    error: null
  };
}

export function removeKeyword(rows: KeywordRow[], keyword: string): KeywordRow[] {
  return rows.filter((row) => row.keyword !== keyword);
}

export function clearKeywords(_rows: ReadonlyArray<KeywordRow>): KeywordRow[] {
  return [];
}
