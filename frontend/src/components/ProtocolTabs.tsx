import type { ReactNode } from 'react';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ParamField, tieredParams } from '@/components/ParamField';
import type { ParamMeta, ProtocolMeta } from '@/types';

/**
 * Tab bar over the protocols of a workspace/template: one tab per
 * protocol (VNC/RDP/SSH — whatever is configured) instead of one endless
 * vertical form. Used by the user connection settings AND the admin
 * template editor, so both always look and behave the same.
 */
export function ProtocolTabs({
  protocols,
  active,
  onSelect,
  badge,
  trailing,
}: {
  protocols: string[];
  active: string;
  onSelect: (protocol: string) => void;
  /** Optional marker rendered inside a tab (e.g. ● on the chosen one). */
  badge?: (protocol: string) => ReactNode;
  /** Optional element after the tabs (e.g. "+ add protocol"). */
  trailing?: ReactNode;
}) {
  return (
    <div className="flex items-center gap-1 border-b border-slate-200 dark:border-slate-700">
      {protocols.map((p) => (
        <button
          key={p}
          type="button"
          onClick={() => onSelect(p)}
          className={`-mb-px flex items-center gap-1.5 rounded-t-md border-x border-t px-3 py-1.5 text-sm font-medium uppercase ${
            p === active
              ? 'border-slate-200 bg-white text-blue-600 dark:border-slate-700 dark:bg-slate-800 dark:text-blue-400'
              : 'border-transparent text-slate-500 hover:text-slate-700 dark:text-slate-400 dark:hover:text-slate-200'
          }`}
        >
          {p}
          {badge?.(p)}
        </button>
      ))}
      {trailing && <span className="ml-auto pb-1">{trailing}</span>}
    </div>
  );
}

/**
 * The registry-driven parameter form of ONE protocol: simple tier always,
 * advanced tier behind the shared toggle, values re-validated server-side.
 * Shared between the connection-settings dialog (instance context: the
 * template's userParams allow-list applies, admins bypass) and the
 * template editor (admin context: no allow-list, plus a per-param extra
 * slot for the "user-overridable" checkbox).
 */
export function ProtocolParamsForm({
  meta,
  protocol,
  values,
  onChange,
  allowList,
  placeholders,
  columns = 2,
  renderParamExtra,
}: {
  meta: ProtocolMeta[] | undefined;
  protocol: string;
  values: Record<string, string>;
  onChange: (name: string, value: string) => void;
  /** Tunable names; undefined = every non-platform parameter (admin/owner). */
  allowList?: string[];
  /** Defaults shown as placeholders (e.g. the template's locked params). */
  placeholders?: Record<string, string>;
  columns?: 1 | 2;
  renderParamExtra?: (param: ParamMeta) => ReactNode;
}) {
  const { t } = useTranslation();
  const [showAdvanced, setShowAdvanced] = useState(false);
  const { simple, advanced } = tieredParams(meta, protocol, allowList);
  const fields = showAdvanced ? [...simple, ...advanced] : simple;

  return (
    <div className="space-y-3">
      {fields.length > 0 ? (
        <div className={columns === 2 ? 'grid grid-cols-2 gap-3' : 'space-y-3'}>
          {fields.map((pm) => (
            <div key={pm.name} className="space-y-1">
              <ParamField
                meta={placeholders?.[pm.name] ? { ...pm, default: placeholders[pm.name] } : pm}
                value={values[pm.name] ?? ''}
                onChange={(value) => onChange(pm.name, value)}
              />
              {renderParamExtra?.(pm)}
            </div>
          ))}
        </div>
      ) : (
        <p className="text-xs text-slate-400 dark:text-slate-500">{t('portal.noTunableParams')}</p>
      )}
      {advanced.length > 0 && (
        <label className="flex items-center gap-2 text-xs text-slate-500 dark:text-slate-400">
          <input
            type="checkbox"
            checked={showAdvanced}
            onChange={(e) => setShowAdvanced(e.target.checked)}
          />
          {t('portal.showAdvancedParams')}
        </label>
      )}
    </div>
  );
}
