import { useTranslation } from 'react-i18next';
import type { TemplateEnvVar } from '@/types';
import { fieldSm } from './fields';

/** Env vars of the template (literal or Secret-backed, CR passthrough). */
export function EnvFieldset({
  env,
  onChange,
  suggestions,
  onAdopt,
  valuePlaceholders,
}: {
  env: TemplateEnvVar[] | undefined;
  onChange: (env: TemplateEnvVar[]) => void;
  /** Catalog-recommended vars without a default: rendered greyed and
   * inert (never part of the template) until adopted with a click. */
  suggestions?: { name: string; description?: string }[];
  onAdopt?: (name: string) => void;
  /** Per-name placeholder for the value input (hint descriptions). */
  valuePlaceholders?: Record<string, string>;
}) {
  const { t } = useTranslation();
  const vars = env ?? [];

  return (
    <fieldset className="space-y-2 rounded-lg border border-slate-200 p-3 dark:border-slate-700">
      <legend className="px-1 text-sm font-medium text-slate-700 dark:text-slate-200">
        {t('admin.templatesPage.env')}
      </legend>
      <p className="text-xs text-slate-400 dark:text-slate-500">
        {t('admin.templatesPage.envHint')}
      </p>
      {vars.map((v, i) => (
        <EnvRow
          key={i}
          env={v}
          valuePlaceholder={valuePlaceholders?.[v.name]}
          onChange={(next) => onChange(vars.map((e, j) => (j === i ? next : e)))}
          onRemove={() => onChange(vars.filter((_, j) => j !== i))}
        />
      ))}
      {(suggestions ?? []).map((s) => (
        <button
          key={s.name}
          type="button"
          onClick={() => onAdopt?.(s.name)}
          title={t('admin.templatesPage.envSuggestionTooltip')}
          className="flex w-full items-center gap-2 rounded-md border border-dashed border-slate-300 px-2 py-1.5 text-left opacity-50 transition-opacity hover:opacity-100 dark:border-slate-600"
        >
          <span className="font-mono text-xs text-slate-700 dark:text-slate-200">{s.name}</span>
          {s.description && (
            <span className="truncate text-xs text-slate-500 dark:text-slate-400">
              {s.description}
            </span>
          )}
          <span className="ml-auto shrink-0 text-xs font-medium text-blue-600 dark:text-blue-400">
            + {t('admin.templatesPage.envSuggestionAdopt')}
          </span>
        </button>
      ))}
      {(suggestions?.length ?? 0) > 0 && (
        <p className="text-xs text-slate-400 dark:text-slate-500">
          {t('admin.templatesPage.envSuggestionHint')}
        </p>
      )}
      <button
        type="button"
        onClick={() => onChange([...vars, { name: '', value: '' }])}
        className="text-sm text-blue-600 hover:underline dark:text-blue-400"
      >
        + {t('admin.templatesPage.addEnv')}
      </button>
    </fieldset>
  );
}

// One env row: literal value or Secret reference — matching corev1.EnvVar,
// so what the form writes is exactly what the CR stores.
function EnvRow({
  env,
  valuePlaceholder,
  onChange,
  onRemove,
}: {
  env: TemplateEnvVar;
  valuePlaceholder?: string;
  onChange: (env: TemplateEnvVar) => void;
  onRemove: () => void;
}) {
  const { t } = useTranslation();
  const fromSecret = !!env.valueFrom?.secretKeyRef;

  return (
    <div className="flex items-end gap-2">
      <label className="block flex-1">
        <span className="text-xs text-slate-500 dark:text-slate-400">name</span>
        <input
          className={fieldSm}
          value={env.name}
          onChange={(e) => onChange({ ...env, name: e.target.value })}
          required
        />
      </label>
      <label className="flex items-center gap-1 pb-2 text-xs text-slate-500 dark:text-slate-400">
        <input
          type="checkbox"
          checked={fromSecret}
          onChange={(e) =>
            onChange(
              e.target.checked
                ? { name: env.name, valueFrom: { secretKeyRef: { name: '', key: '' } } }
                : { name: env.name, value: '' },
            )
          }
        />
        {t('admin.templatesPage.fromSecret')}
      </label>
      {fromSecret ? (
        <>
          <label className="block flex-1">
            <span className="text-xs text-slate-500 dark:text-slate-400">secret</span>
            <input
              className={fieldSm}
              value={env.valueFrom?.secretKeyRef?.name ?? ''}
              onChange={(e) =>
                onChange({
                  ...env,
                  valueFrom: {
                    secretKeyRef: {
                      name: e.target.value,
                      key: env.valueFrom?.secretKeyRef?.key ?? '',
                    },
                  },
                })
              }
              required
            />
          </label>
          <label className="block flex-1">
            <span className="text-xs text-slate-500 dark:text-slate-400">key</span>
            <input
              className={fieldSm}
              value={env.valueFrom?.secretKeyRef?.key ?? ''}
              onChange={(e) =>
                onChange({
                  ...env,
                  valueFrom: {
                    secretKeyRef: {
                      name: env.valueFrom?.secretKeyRef?.name ?? '',
                      key: e.target.value,
                    },
                  },
                })
              }
              required
            />
          </label>
        </>
      ) : (
        <label className="block flex-1">
          <span className="text-xs text-slate-500 dark:text-slate-400">value</span>
          <input
            className={fieldSm}
            value={env.value ?? ''}
            placeholder={valuePlaceholder}
            onChange={(e) => onChange({ ...env, value: e.target.value })}
          />
        </label>
      )}
      <button
        type="button"
        onClick={onRemove}
        className="pb-2 text-sm text-red-600 hover:underline"
      >
        ✕
      </button>
    </div>
  );
}
