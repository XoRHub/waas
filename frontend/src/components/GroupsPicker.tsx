import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useKnownGroups } from '@/hooks/useApi';

/** Toggle a group in/out of the selection (pure, tested). */
export function toggleGroup(groups: string[], g: string): string[] {
  return groups.includes(g) ? groups.filter((x) => x !== g) : [...groups, g];
}

/** Add a custom group (trimmed, deduplicated; pure, tested). */
export function addGroup(groups: string[], raw: string): string[] {
  const g = raw.trim();
  if (!g || groups.includes(g)) return groups;
  return [...groups, g];
}

/**
 * The ONE groups selector, shared by the user create and edit dialogs
 * (they diverged: edit had regressed to a comma-separated text field).
 * Controlled component: chips for the current selection (click removes),
 * suggestions from the known groups (policy subjects ∪ users' groups),
 * free-text entry for new groups.
 */
export function GroupsPicker({
  value,
  onChange,
  hint,
}: {
  value: string[];
  onChange: (groups: string[]) => void;
  hint?: string;
}) {
  const { t } = useTranslation();
  const knownGroups = useKnownGroups();
  const [newGroup, setNewGroup] = useState('');

  const suggestions = (knownGroups.data?.data ?? []).filter((g) => !value.includes(g));

  const submitNew = () => {
    onChange(addGroup(value, newGroup));
    setNewGroup('');
  };

  return (
    <div>
      <span className="text-sm text-slate-600 dark:text-slate-300">
        {t('admin.usersPage.groups')}
      </span>
      {value.length > 0 && (
        <div className="mt-1 flex flex-wrap gap-1">
          {value.map((g) => (
            <button
              key={g}
              type="button"
              onClick={() => onChange(toggleGroup(value, g))}
              className="rounded-full bg-blue-100 px-2 py-0.5 text-xs text-blue-800 hover:bg-blue-200 dark:bg-blue-900/50 dark:text-blue-200"
            >
              {g} ✕
            </button>
          ))}
        </div>
      )}
      {suggestions.length > 0 && (
        <div className="mt-1 flex flex-wrap gap-1">
          {suggestions.map((g) => (
            <button
              key={g}
              type="button"
              onClick={() => onChange(toggleGroup(value, g))}
              className="rounded-full border border-slate-300 px-2 py-0.5 text-xs text-slate-600 hover:bg-slate-50 dark:border-slate-600 dark:text-slate-300 dark:hover:bg-slate-700"
            >
              + {g}
            </button>
          ))}
        </div>
      )}
      <div className="mt-1 flex gap-2">
        <input
          className="flex-1 rounded-md border border-slate-300 px-3 py-1.5 text-sm dark:border-slate-600 dark:bg-slate-700 dark:text-white"
          placeholder={t('admin.usersPage.addGroupPlaceholder')}
          value={newGroup}
          onChange={(e) => setNewGroup(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault();
              submitNew();
            }
          }}
        />
        <button
          type="button"
          onClick={submitNew}
          className="rounded-md border border-slate-300 px-3 py-1.5 text-sm text-slate-700 hover:bg-slate-50 dark:border-slate-600 dark:text-slate-200 dark:hover:bg-slate-700"
        >
          {t('admin.usersPage.addGroup')}
        </button>
      </div>
      {hint && <p className="mt-1 text-xs text-slate-400 dark:text-slate-500">{hint}</p>}
    </div>
  );
}
