import type { ReactNode } from 'react';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ParamField, tieredParams } from '@/components/ParamField';
import { useEscape } from '@/hooks/useEscape';
import type { ParamMeta, ProtocolMeta } from '@/types';

/**
 * Why a protocol tab cannot be removed right now, or null when it can.
 * Pure on purpose (tested): every tabs consumer shares the exact same
 * edge-case behavior — the LAST protocol of a set is never removable
 * (a template/remote machine must keep at least one way in), and a
 * locked protocol (imposed by the template) is never removable either.
 */
export function protocolRemovalBlock(opts: {
  count: number;
  locked?: boolean;
}): 'last' | 'locked' | null {
  if (opts.locked) return 'locked';
  if (opts.count <= 1) return 'last';
  return null;
}

/**
 * Tab bar over the protocols of a workspace/template: one tab per
 * protocol (VNC/RDP/SSH — whatever is configured) instead of one endless
 * vertical form. Used by the user connection settings, the create
 * dialogs AND the admin template editor, so all always look and behave
 * the same.
 *
 * The add/remove mechanic is OPT-IN and shared: `addable`+`onAdd` render
 * a "+" opening the list of protocols not configured yet (explicit
 * choice — never "add the first unused one"); `onRemove` renders a ✕ on
 * the active tab, confirmation included. Removal guards (last protocol,
 * template-locked protocol) are centralized in protocolRemovalBlock.
 */
export function ProtocolTabs({
  protocols,
  active,
  onSelect,
  badge,
  trailing,
  addable,
  onAdd,
  onRemove,
  locked,
}: {
  protocols: string[];
  active: string;
  onSelect: (protocol: string) => void;
  /** Optional marker rendered inside a tab (e.g. ● on the chosen one). */
  badge?: (protocol: string) => ReactNode;
  /** Optional element after the tabs. */
  trailing?: ReactNode;
  /** Protocols offered by the "+" menu (not configured yet + allowed). */
  addable?: string[];
  onAdd?: (protocol: string) => void;
  /** When set, the active tab gets a confirm-guarded remove button. */
  onRemove?: (protocol: string) => void;
  /** Template-imposed protocols: shown with a lock, never removable. */
  locked?: (protocol: string) => boolean;
}) {
  const { t } = useTranslation();
  const [menuOpen, setMenuOpen] = useState(false);
  useEscape(menuOpen, () => setMenuOpen(false));

  const removeButton = (p: string) => {
    if (!onRemove) return null;
    const block = protocolRemovalBlock({ count: protocols.length, locked: locked?.(p) });
    if (block === 'locked') {
      return (
        <span className="text-[11px]" title={t('protocolTabs.locked')}>
          🔒
        </span>
      );
    }
    return (
      <button
        type="button"
        disabled={Boolean(block)}
        title={
          block === 'last'
            ? t('protocolTabs.lastProtocol')
            : t('protocolTabs.remove', { protocol: p.toUpperCase() })
        }
        onClick={(e) => {
          e.stopPropagation();
          if (block) return;
          if (window.confirm(t('protocolTabs.removeConfirm', { protocol: p.toUpperCase() }))) {
            onRemove(p);
          }
        }}
        className={`-mr-1 rounded px-1 text-xs ${
          block
            ? 'cursor-not-allowed opacity-30'
            : 'text-slate-400 hover:bg-red-100 hover:text-red-600 dark:hover:bg-red-900/40'
        }`}
      >
        ✕
      </button>
    );
  };

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
          {p === active && removeButton(p)}
        </button>
      ))}
      {onAdd && (addable?.length ?? 0) > 0 && (
        <span className="relative pb-1">
          <button
            type="button"
            onClick={() => setMenuOpen((o) => !o)}
            className="text-sm text-blue-600 hover:underline dark:text-blue-400"
          >
            + {t('protocolTabs.add')}
          </button>
          {menuOpen && (
            <span className="absolute left-0 top-full z-10 mt-1 flex min-w-28 flex-col rounded-md border border-slate-200 bg-white py-1 shadow-lg dark:border-slate-600 dark:bg-slate-800">
              {addable!.map((p) => (
                <button
                  key={p}
                  type="button"
                  onClick={() => {
                    setMenuOpen(false);
                    onAdd(p);
                  }}
                  className="px-3 py-1.5 text-left text-sm uppercase text-slate-700 hover:bg-slate-100 dark:text-slate-200 dark:hover:bg-slate-700"
                >
                  {p}
                </button>
              ))}
            </span>
          )}
        </span>
      )}
      {trailing && <span className="ml-auto pb-1">{trailing}</span>}
    </div>
  );
}

/**
 * True when the form's enable-audio resolves to true: the user/admin
 * value wins, an unset value ("" = inherit) falls back to the template's
 * locked value (placeholders). Exported for its unit tests.
 */
export function audioEnabled(
  protocol: string,
  values: Record<string, string>,
  placeholders?: Record<string, string>,
): boolean {
  if (protocol !== 'vnc') return false;
  const value = values['enable-audio'] ?? '';
  return value !== '' ? value === 'true' : placeholders?.['enable-audio'] === 'true';
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
  audioPortExposed,
  onAudioPortChange,
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
  /** Whether the template exposes the PulseAudio port (4713). */
  audioPortExposed?: boolean;
  /**
   * Template editor only: makes the audio-port section an editable
   * checkbox driving the CR's exposeAudioPort. Absent (user dialogs),
   * the section is a read-only status — users cannot mutate the
   * template, they get told whether audio can actually stream.
   */
  onAudioPortChange?: (exposed: boolean) => void;
}) {
  const { t } = useTranslation();
  const [showAdvanced, setShowAdvanced] = useState(false);
  const { simple, advanced } = tieredParams(meta, protocol, allowList);
  const fields = showAdvanced ? [...simple, ...advanced] : simple;
  // First cross-field conditional rendering in the param forms: the
  // audio-port section only exists while enable-audio resolves to true.
  // Deliberately a hardcoded `enable-audio && <section>` — one registry
  // param gating one CR field — NOT a generic inter-field dependency
  // mechanism; revisit if the param grouping work (Feature 7) surfaces
  // more of these.
  const showAudioPort = audioEnabled(protocol, values, placeholders);

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
      {showAudioPort &&
        (onAudioPortChange ? (
          <label className="flex items-start gap-2 text-sm text-slate-600 dark:text-slate-300">
            <input
              type="checkbox"
              className="mt-0.5"
              checked={audioPortExposed ?? false}
              onChange={(e) => onAudioPortChange(e.target.checked)}
            />
            <span>
              {t('protocolTabs.exposeAudioPort')}
              <span className="block text-xs text-slate-400 dark:text-slate-500">
                {t('protocolTabs.exposeAudioPortHint')}
              </span>
            </span>
          </label>
        ) : audioPortExposed ? (
          <p className="text-xs text-slate-500 dark:text-slate-400">
            {t('protocolTabs.audioPortExposed')}
          </p>
        ) : (
          <p className="rounded-md bg-amber-50 px-2 py-1.5 text-xs text-amber-700 dark:bg-amber-900/30 dark:text-amber-300">
            {t('protocolTabs.audioPortMissing')}
          </p>
        ))}
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
