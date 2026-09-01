import { Fragment, useState } from "react";
import { ChevronDown, ChevronRight, Link, Plus, Trash2, Undo2 } from "lucide-react";
import type { CanonicalKeyword } from "./analyticsController";
import { t, type Locale } from "./i18n";
import { removeKeyword, type KeywordRow } from "./keywords";

type KeywordTableProps = {
  rows: KeywordRow[];
  visibleRows: KeywordRow[];
  canonicalRows: CanonicalKeyword[];
  expandedCanonicalIds: ReadonlySet<string>;
  locale: Locale;
  onRowsChange: (rows: KeywordRow[]) => void;
  onToggleForms: (id: string) => void;
  onAddForm: (id: string, form: string) => void;
  onDetachForm: (id: string, form: string) => void;
  onMoveForm: (targetId: string, form: string) => void;
};

type KeywordDeleteAction = { type: "delete"; keyword: string };

export function applyKeywordDeleteAction(
  rows: KeywordRow[],
  action: KeywordDeleteAction
): KeywordRow[] {
  return removeKeyword(rows, action.keyword);
}

export function KeywordTable({
  rows,
  visibleRows,
  canonicalRows,
  expandedCanonicalIds,
  locale,
  onRowsChange,
  onToggleForms,
  onAddForm,
  onDetachForm,
  onMoveForm
}: KeywordTableProps) {
  const deleteKeyword = (keyword: string) => {
    onRowsChange(applyKeywordDeleteAction(rows, { type: "delete", keyword }));
  };
  const canonicalByValue = new Map(
    canonicalRows.map((row) => [normalizeKeyword(row.canonical), row])
  );

  return (
    <>
      <div className="tableHeader keywordsGrid">
        <span>{t(locale, "keyword")}</span>
        <span>{t(locale, "analyticsForms")}</span>
        <span>{t(locale, "actions")}</span>
      </div>
      {visibleRows.map((row) => {
        const canonical = canonicalByValue.get(normalizeKeyword(row.keyword));
        const expanded = canonical ? expandedCanonicalIds.has(canonical.id) : false;
        return (
        <Fragment key={row.keyword}>
          <div className="tableRow keywordsGrid" key={row.keyword}>
            <div><strong>{row.keyword}</strong></div>
            <div>
              {canonical
                ? <button
                    aria-expanded={expanded}
                    aria-label={`${t(locale, "analyticsForms")}: ${canonical.forms.length}`}
                    className="keywordFormsButton"
                    onClick={() => onToggleForms(canonical.id)}
                    type="button"
                  >
                    {expanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
                    {canonical.forms.length}
                  </button>
                : <span className="keywordFormsMissing">0</span>}
            </div>
            <div className="keywordRowActions">
              <button aria-label={t(locale, "delete")} onClick={() => deleteKeyword(row.keyword)} title={t(locale, "delete")} type="button">
                <Trash2 size={17} />
              </button>
            </div>
          </div>
          {canonical && expanded && (
            <KeywordCanonicalForms
              canonical={canonical}
              locale={locale}
              onAddForm={onAddForm}
              onDetachForm={onDetachForm}
              onMoveForm={onMoveForm}
              targets={canonicalRows.filter((target) => target.id !== canonical.id)}
            />
          )}
        </Fragment>
      )})}
    </>
  );
}

function KeywordCanonicalForms({
  canonical,
  locale,
  targets,
  onAddForm,
  onDetachForm,
  onMoveForm
}: {
  canonical: CanonicalKeyword;
  locale: Locale;
  targets: CanonicalKeyword[];
  onAddForm: (id: string, form: string) => void;
  onDetachForm: (id: string, form: string) => void;
  onMoveForm: (targetId: string, form: string) => void;
}) {
  const [newForm, setNewForm] = useState("");
  return (
    <div className="keywordCanonicalForms">
      {canonical.forms.map((form) => (
        <KeywordCanonicalForm
          canonical={canonical}
          form={form.value}
          frequency={form.frequency}
          key={form.value}
          locale={locale}
          onDetachForm={onDetachForm}
          onMoveForm={onMoveForm}
          targets={targets}
        />
      ))}
      <div className="keywordCanonicalFormAdd">
        <input
          aria-label={t(locale, "analyticsNewForm")}
          onChange={(event) => setNewForm(event.target.value)}
          placeholder={t(locale, "analyticsNewForm")}
          value={newForm}
        />
        <button
          aria-label={t(locale, "analyticsAddForm")}
          disabled={!newForm.trim()}
          onClick={() => {
            onAddForm(canonical.id, newForm.trim());
            setNewForm("");
          }}
          title={t(locale, "analyticsAddForm")}
          type="button"
        >
          <Plus size={14} />
        </button>
      </div>
    </div>
  );
}

function KeywordCanonicalForm({
  canonical,
  form,
  frequency,
  locale,
  targets,
  onDetachForm,
  onMoveForm
}: {
  canonical: CanonicalKeyword;
  form: string;
  frequency: number;
  locale: Locale;
  targets: CanonicalKeyword[];
  onDetachForm: (id: string, form: string) => void;
  onMoveForm: (targetId: string, form: string) => void;
}) {
  const [target, setTarget] = useState("");
  return (
    <div className="keywordCanonicalForm">
      <span>{form}</span>
      <small>{frequency.toLocaleString(locale)}</small>
      <select
        aria-label={`${t(locale, "analyticsCanonicalFor")} ${form}`}
        onChange={(event) => setTarget(event.target.value)}
        value={target}
      >
        <option value="">{t(locale, "analyticsMoveToCanonical")}</option>
        {targets.map((item) => <option key={item.id} value={item.id}>{item.canonical}</option>)}
      </select>
      <button
        aria-label={t(locale, "analyticsMoveForm")}
        disabled={!target}
        onClick={() => onMoveForm(target, form)}
        title={t(locale, "analyticsMoveForm")}
        type="button"
      >
        <Link size={13} />
      </button>
      <button
        aria-label={t(locale, "analyticsDetachForm")}
        onClick={() => onDetachForm(canonical.id, form)}
        title={t(locale, "analyticsDetachForm")}
        type="button"
      >
        <Undo2 size={13} />
      </button>
    </div>
  );
}

function normalizeKeyword(value: string): string {
  return value.normalize("NFC").trim().toLocaleLowerCase("ru-RU");
}
