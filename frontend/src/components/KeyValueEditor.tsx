import { useState } from 'react';
import { useTranslation } from 'react-i18next';

// Shared row-editor classes: one definition for every key/value-style
// list (this editor, the runtime form's env and toleration rows) —
// imported, never pasted again.
export const rowInput =
  'rounded-md border border-slate-300 px-2 py-1.5 font-mono text-xs disabled:opacity-50 dark:border-slate-600 dark:bg-slate-700 dark:text-white';
export const addButton =
  'rounded-md border border-slate-300 px-2 py-1 text-xs text-slate-600 hover:bg-slate-50 dark:border-slate-600 dark:text-slate-300 dark:hover:bg-slate-700';
export const removeButton = 'rounded px-2 text-slate-400 hover:bg-slate-100 dark:hover:bg-slate-700';

interface Row {
  key: string;
  value: string;
}

const toRows = (value: Record<string, string>): Row[] =>
  Object.entries(value).map(([key, value]) => ({ key, value }));

// Export drops in-progress rows: only non-empty (trimmed) keys reach the
// caller — the same doctrine as the runtime form's nodeSelector rows.
const fromRows = (rows: Row[]): Record<string, string> => {
  const out: Record<string, string> = {};
  for (const row of rows) {
    if (row.key.trim() !== '') out[row.key.trim()] = row.value;
  }
  return out;
};

/**
 * Generic string-map editor: key/value rows with add/remove, exporting a
 * clean Record on every edit. Rows are INTERNAL state so a key mid-typing
 * (still empty, or briefly duplicate) never round-trips through the
 * exported record and vanishes under the user's cursor. All labels come
 * from the caller — the component chooses no text itself. Deliberately no
 * client-side reserved-key validation: the denylist is server-side
 * (operator/pkg/metakeys) and the UI only relays the server's 403.
 */
export function KeyValueEditor({
  value,
  onChange,
  disabled,
  keyPlaceholder,
  valuePlaceholder,
  addLabel,
}: {
  value: Record<string, string>;
  onChange: (value: Record<string, string>) => void;
  /** Renders the rows read-only (the caller shows its own 🔒 marker). */
  disabled?: boolean;
  keyPlaceholder: string;
  valuePlaceholder: string;
  addLabel: string;
}) {
  const { t } = useTranslation();
  const [rows, setRows] = useState<Row[]>(() => toRows(value));

  const patch = (next: Row[]) => {
    setRows(next);
    onChange(fromRows(next));
  };

  return (
    <div className="space-y-2">
      {rows.map((row, i) => (
        <div key={i} className="flex gap-2">
          <input
            className={`w-2/5 ${rowInput}`}
            placeholder={keyPlaceholder}
            value={row.key}
            disabled={disabled}
            onChange={(e) => patch(rows.map((r, j) => (j === i ? { ...r, key: e.target.value } : r)))}
          />
          <input
            className={`flex-1 ${rowInput}`}
            placeholder={valuePlaceholder}
            value={row.value}
            disabled={disabled}
            onChange={(e) =>
              patch(rows.map((r, j) => (j === i ? { ...r, value: e.target.value } : r)))
            }
          />
          {!disabled && (
            <button
              type="button"
              onClick={() => patch(rows.filter((_, j) => j !== i))}
              className={removeButton}
              aria-label={t('app.delete')}
            >
              ✕
            </button>
          )}
        </div>
      ))}
      {!disabled && (
        <button
          type="button"
          onClick={() => patch([...rows, { key: '', value: '' }])}
          className={addButton}
        >
          + {addLabel}
        </button>
      )}
    </div>
  );
}
