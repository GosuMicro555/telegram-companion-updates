import type { Dispatch, SetStateAction } from "react";
import type { CanonicalClass, CanonicalKeyword } from "./analyticsController";
import type { KeywordRow } from "./keywords";

type KeywordRowsChange = Dispatch<SetStateAction<KeywordRow[]>>;

export function removeCanonicalTriggerKeyword(rows: KeywordRow[], canonical: string): KeywordRow[] {
  const normalized = canonical.toLocaleLowerCase("ru-RU");
  return rows.filter((row) => row.keyword.toLocaleLowerCase("ru-RU") !== normalized);
}

function removeActiveCanonical(row: CanonicalKeyword, onKeywordsChange: KeywordRowsChange): void {
  if (!row.triggerActive) return;
  onKeywordsChange((current) => removeCanonicalTriggerKeyword(current, row.canonical));
}

export async function handleCanonicalClassificationAction(
  row: CanonicalKeyword,
  nextClass: CanonicalClass,
  onKeywordsChange: KeywordRowsChange,
  classify: (id: string, keywordClass: CanonicalClass) => Promise<boolean>
): Promise<boolean> {
  const succeeded = await classify(row.id, nextClass);
  if (succeeded && nextClass !== "positive") removeActiveCanonical(row, onKeywordsChange);
  return succeeded;
}

export async function handleCanonicalDeleteAction(
  row: CanonicalKeyword,
  onKeywordsChange: KeywordRowsChange,
  remove: (id: string) => Promise<boolean>
): Promise<boolean> {
  const succeeded = await remove(row.id);
  if (succeeded) removeActiveCanonical(row, onKeywordsChange);
  return succeeded;
}

export async function handleCanonicalClearAction(
  rows: CanonicalKeyword[],
  keywordClass: CanonicalClass,
  onKeywordsChange: KeywordRowsChange,
  clear: () => Promise<boolean>
): Promise<boolean> {
  const succeeded = await clear();
  if (!succeeded || keywordClass !== "positive") return succeeded;
  const active = rows.filter((row) => row.triggerActive);
  onKeywordsChange((current) => active.reduce(
    (result, row) => removeCanonicalTriggerKeyword(result, row.canonical),
    current
  ));
  return true;
}
