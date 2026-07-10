import { useTranslation } from 'react-i18next';
import type { ParamMeta } from '@/types';

const fieldClass =
  'mt-0.5 w-full rounded-md border border-slate-300 px-3 py-1.5 text-sm dark:border-slate-600 dark:bg-slate-700 dark:text-white';

/**
 * Tri-state segmented control for KindBool. A binary toggle cannot say
 * "no preference, let guacd decide" — and that third state matters: an
 * admin forcing `false` on a param whose guacd default is `true`
 * (ignore-cert) is NOT the same as leaving it unset. The wire contract is
 * untouched: "" (inherit) / "true" / "false", exactly like the old
 * tri-state <select>, but all three states are now visible at a glance.
 */
function BoolField({
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
  const { t } = useTranslation();
  const segments = [
    { value: '', label: t('paramField.boolDefault', { value: meta.default || '—' }) },
    { value: 'true', label: t('paramField.boolOn') },
    { value: 'false', label: t('paramField.boolOff') },
  ];
  return (
    <span className="mt-0.5 flex w-full divide-x divide-slate-300 overflow-hidden rounded-md border border-slate-300 dark:divide-slate-600 dark:border-slate-600">
      {segments.map((s) => (
        <button
          key={s.value}
          type="button"
          aria-pressed={value === s.value}
          disabled={disabled}
          onClick={() => onChange(s.value)}
          className={`flex-1 px-2 py-1.5 text-xs ${
            value === s.value
              ? 'bg-blue-600 font-medium text-white'
              : 'bg-white text-slate-600 hover:bg-slate-100 dark:bg-slate-700 dark:text-slate-300 dark:hover:bg-slate-600'
          } ${disabled ? 'cursor-not-allowed opacity-50' : ''}`}
        >
          {s.label}
        </button>
      ))}
    </span>
  );
}

/**
 * One guacd parameter input, rendered from the platform registry entry
 * (GET /api/v1/meta/protocols). Kind → widget mapping:
 *   enum → select with the default as first, empty-valued option;
 *   bool → tri-state segmented control (BoolField — empty = guacd default);
 *   int → number input, string → text input.
 * Every form that touches guacd params goes through this component, so a
 * new registry entry needs zero form code — and a widget change here
 * lands on every screen (connection settings, create dialogs, template
 * editor, session overlay) at once, deliberately: a bool means the same
 * thing everywhere. The value contract is untouched everywhere: "" means
 * "inherit the default", anything else is sent verbatim.
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
        return <BoolField meta={meta} value={value} onChange={onChange} disabled={disabled} />;
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

  // A <label> around the bool segments would forward every click on the
  // name/description to the first button (labels activate their first
  // labelable descendant), silently resetting the value to "Default".
  const Wrapper = meta.kind === 'bool' ? 'div' : 'label';
  return (
    <Wrapper className="block">
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
    </Wrapper>
  );
}

/** The registry entries for one protocol, filtered by tier and (optionally)
 * an allow-list of names. */
export function paramsFor(
  meta: { name: string; params: ParamMeta[] | null }[] | undefined,
  protocol: string,
  tiers: ParamMeta['tier'][],
  allowList?: string[],
): ParamMeta[] {
  const entry = meta?.find((m) => m.name === protocol);
  if (!entry) return [];
  // Defensive on top of the API contract ("params is always an array"):
  // a protocol without registry entries (kasmvnc) crashed every param
  // form here when the backend leaked null.
  return (entry.params ?? []).filter(
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
