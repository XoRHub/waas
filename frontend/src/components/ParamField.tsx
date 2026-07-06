import type { ParamMeta } from '@/types';

const fieldClass =
  'mt-0.5 w-full rounded-md border border-slate-300 px-3 py-1.5 text-sm dark:border-slate-600 dark:bg-slate-700 dark:text-white';

/**
 * One guacd parameter input, rendered from the platform registry entry
 * (GET /api/v1/meta/protocols): enum → select, bool → tri-state select
 * (empty = guacd default), int → number, string → text. Every form that
 * touches guacd params goes through this component, so a new registry
 * entry needs zero form code.
 */
export function ParamField({
  meta,
  value,
  onChange,
  disabled,
}: {
  meta: ParamMeta;
  value: string;
  onChange: (value: string) => void;
  disabled?: boolean;
}) {
  const input = () => {
    switch (meta.kind) {
      case 'enum':
        return (
          <select
            className={fieldClass}
            value={value}
            onChange={(e) => onChange(e.target.value)}
            disabled={disabled}
          >
            <option value="">{meta.default ? `(${meta.default})` : '—'}</option>
            {meta.enum?.map((v) => (
              <option key={v} value={v}>
                {v}
              </option>
            ))}
          </select>
        );
      case 'bool':
        return (
          <select
            className={fieldClass}
            value={value}
            onChange={(e) => onChange(e.target.value)}
            disabled={disabled}
          >
            <option value="">{meta.default ? `(${meta.default})` : '—'}</option>
            <option value="true">true</option>
            <option value="false">false</option>
          </select>
        );
      case 'int':
        return (
          <input
            type="number"
            className={fieldClass}
            value={value}
            min={meta.min}
            max={meta.max}
            placeholder={meta.default}
            onChange={(e) => onChange(e.target.value)}
            disabled={disabled}
          />
        );
      default:
        return (
          <input
            className={fieldClass}
            value={value}
            placeholder={meta.default}
            onChange={(e) => onChange(e.target.value)}
            disabled={disabled}
          />
        );
    }
  };

  return (
    <label className="block">
      <span className="flex items-baseline justify-between">
        <span className="font-mono text-xs text-slate-500 dark:text-slate-400">{meta.name}</span>
        {meta.live && (
          <span className="rounded bg-emerald-100 px-1 text-[10px] uppercase text-emerald-700 dark:bg-emerald-900/50 dark:text-emerald-300">
            live
          </span>
        )}
      </span>
      {input()}
      <span className="mt-0.5 block text-[11px] leading-tight text-slate-400 dark:text-slate-500">
        {meta.description}
      </span>
    </label>
  );
}

/** The registry entries for one protocol, filtered by tier and (optionally)
 * an allow-list of names. */
export function paramsFor(
  meta: { name: string; params: ParamMeta[] }[] | undefined,
  protocol: string,
  tiers: ParamMeta['tier'][],
  allowList?: string[],
): ParamMeta[] {
  const entry = meta?.find((m) => m.name === protocol);
  if (!entry) return [];
  return entry.params.filter(
    (p) => tiers.includes(p.tier) && (!allowList || allowList.includes(p.name)),
  );
}

/**
 * Splits a protocol's tunable params into the simple ("ui") tier and the
 * advanced tier, honoring the same allow-list. Callers show `simple`
 * always, `advanced` behind the toggle, and only render the toggle when
 * `advanced` is non-empty — otherwise the toggle would look inert (that
 * is the "show advanced parameters does nothing" bug: templates rarely
 * delegate advanced-tier params, so with an allow-list there was nothing
 * to reveal, and admins were wrongly kept inside the allow-list too).
 */
export function tieredParams(
  meta: { name: string; params: ParamMeta[] }[] | undefined,
  protocol: string,
  allowList?: string[],
): { simple: ParamMeta[]; advanced: ParamMeta[] } {
  return {
    simple: paramsFor(meta, protocol, ['ui'], allowList),
    advanced: paramsFor(meta, protocol, ['advanced'], allowList),
  };
}
