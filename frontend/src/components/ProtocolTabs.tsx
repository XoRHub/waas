import type { ReactNode } from 'react';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ParamField, sectionedParams } from '@/components/ParamField';
import { YamlEditor } from '@/components/YamlEditor';
import { useEscape } from '@/hooks/useEscape';
import type { ParamMeta, ProtocolMeta } from '@/types';

/**
 * Why a protocol tab cannot be removed right now, or null when it can.
 * Pure on purpose (tested): every tabs consumer shares the exact same
 * edge-case behavior — a locked protocol (imposed by the template) is
 * never removable, and the LAST protocol of a set is only removable
 * when the consumer declares an empty set valid (`allowEmpty`): the
 * template editor does (no protocols block = the legacy OS-derived
 * entry applies), a remote machine does not (zero endpoints is not a
 * reachable machine, and the API rejects it).
 */
export function protocolRemovalBlock(opts: {
  count: number;
  locked?: boolean;
  allowEmpty?: boolean;
}): 'last' | 'locked' | null {
  if (opts.locked) return 'locked';
  if (!opts.allowEmpty && opts.count <= 1) return 'last';
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
  allowEmpty,
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
  /** The consumer has a valid zero-protocol state: the last tab stays
   * removable (template editor); leave unset where at least one
   * protocol must remain (remote machines). */
  allowEmpty?: boolean;
}) {
  const { t } = useTranslation();
  const [menuOpen, setMenuOpen] = useState(false);
  useEscape(menuOpen, () => setMenuOpen(false));

  const removeButton = (p: string) => {
    if (!onRemove) return null;
    const block = protocolRemovalBlock({
      count: protocols.length,
      locked: locked?.(p),
      allowEmpty,
    });
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
 * Read-only viewer of the admin-managed KasmVNC configuration. KasmVNC
 * deliberately has no userParams registry entry — its config does not
 * travel through guacd — but a config still exists and applies (that is
 * where a read-only session or a resolution lives), so users get to SEE
 * it instead of the misleading "no tunable parameters" message. Two
 * variants for the two read paths: 'template' = the admin's raw text
 * (creation dialog, workspace not born yet), 'effective' = the merged
 * content the operator materialized for the workspace (template + policy
 * clipboard layer). Never editable here: the shared YamlEditor renders
 * it read-only — same highlighting and gutter as the admin editor,
 * typing blocked.
 */
export function KasmVNCConfigView({
  config,
  variant,
}: {
  config: string;
  variant: 'template' | 'effective';
}) {
  const { t } = useTranslation();
  return (
    <div className="space-y-1">
      <h4 className="text-xs font-semibold uppercase tracking-wide opacity-60">
        {t('portal.kasmvncManagedConfig')}
      </h4>
      <p className="text-xs opacity-60">
        {variant === 'effective'
          ? t('portal.kasmvncManagedConfigHintEffective')
          : t('portal.kasmvncManagedConfigHintTemplate')}
      </p>
      {config.trim() !== '' ? (
        // Same YAML rendering as the admin editor, typing blocked. Frame
        // sized to the content, capped at 7 lines (≈ the old max-h-48)
        // with the textarea's native scroll past that.
        <YamlEditor
          value={config}
          onChange={() => {}}
          readOnly
          rows={Math.min(7, config.trimEnd().split('\n').length)}
        />
      ) : (
        <p className="text-xs italic opacity-60">{t('portal.kasmvncManagedConfigEmpty')}</p>
      )}
    </div>
  );
}

/**
 * The registry-driven parameter form of ONE protocol, grouped into the
 * registry's thematic sections (display, audio, …): within a section the
 * simple params are always visible and the advanced ones sit behind a
 * per-section disclosure. Values are re-validated server-side. Shared
 * between the connection-settings dialog (instance context: the
 * template's resolved allow-list applies, admins bypass) and the
 * template editor (admin context: no allow-list, plus per-param and
 * per-section extra slots for the delegation controls).
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
  renderSectionExtra,
  audioPortExposed,
  onAudioPortChange,
  kasmvncConfig,
}: {
  meta: ProtocolMeta[] | undefined;
  protocol: string;
  values: Record<string, string>;
  onChange: (name: string, value: string) => void;
  /** Tunable names — pass the server-resolved flat list
   * (resolvedUserParams: cat: selectors already expanded); undefined =
   * every non-platform parameter (admin/owner). */
  allowList?: string[];
  /** Defaults shown as placeholders (e.g. the template's locked params). */
  placeholders?: Record<string, string>;
  columns?: 1 | 2;
  renderParamExtra?: (param: ParamMeta) => ReactNode;
  /** Template editor only: extra control in a section's heading row
   * (the "allow the whole category" toggle). */
  renderSectionExtra?: (category: ParamMeta['category']) => ReactNode;
  /** Whether the template exposes the PulseAudio port (4713). */
  audioPortExposed?: boolean;
  /**
   * Template editor only: makes the audio-port section an editable
   * checkbox driving the CR's exposeAudioPort. Absent (user dialogs),
   * the section is a read-only status — users cannot mutate the
   * template, they get told whether audio can actually stream.
   */
  onAudioPortChange?: (exposed: boolean) => void;
  /**
   * kasmvnc only: the admin-managed configuration shown read-only —
   * {content, variant} per KasmVNCConfigView. When absent (remote
   * machines, other protocols) the form keeps its usual behavior,
   * including the no-tunable-params message.
   */
  kasmvncConfig?: { content: string; variant: 'template' | 'effective' };
}) {
  const { t } = useTranslation();
  const [openSections, setOpenSections] = useState<Record<string, boolean>>({});
  const sections = sectionedParams(meta, protocol, allowList);
  // First cross-field conditional rendering in the param forms: the
  // audio-port section only exists while enable-audio resolves to true.
  // Deliberately a hardcoded `enable-audio && <section>` — one registry
  // param gating one CR field — NOT a generic inter-field dependency
  // mechanism; revisit if more of these appear.
  const showAudioPort = audioEnabled(protocol, values, placeholders);

  const renderField = (pm: ParamMeta) => (
    <div key={pm.name} className="space-y-1">
      <ParamField
        meta={placeholders?.[pm.name] ? { ...pm, default: placeholders[pm.name] } : pm}
        value={values[pm.name] ?? ''}
        onChange={(value) => onChange(pm.name, value)}
      />
      {renderParamExtra?.(pm)}
    </div>
  );
  const gridClass = columns === 2 ? 'grid grid-cols-2 gap-3' : 'space-y-3';

  return (
    <div className="space-y-4">
      {protocol === 'kasmvnc' && kasmvncConfig !== undefined ? (
        // Not "no parameters": the kasmvnc config exists and applies, it
        // just does not travel through userParams/guacd — show it.
        <KasmVNCConfigView config={kasmvncConfig.content} variant={kasmvncConfig.variant} />
      ) : sections.length > 0 ? (
        sections.map((section) => {
          const open = openSections[section.category] ?? false;
          return (
            <section key={section.category} className="space-y-2">
              <div className="flex items-center justify-between gap-2">
                <h4 className="text-xs font-semibold uppercase tracking-wide text-slate-500 dark:text-slate-400">
                  {t(`protocolTabs.paramCategory.${section.category}`, section.category)}
                </h4>
                {renderSectionExtra?.(section.category)}
              </div>
              {section.simple.length > 0 && (
                <div className={gridClass}>{section.simple.map(renderField)}</div>
              )}
              {open && section.advanced.length > 0 && (
                <div className={gridClass}>{section.advanced.map(renderField)}</div>
              )}
              {section.advanced.length > 0 && (
                <label className="flex items-center gap-2 text-xs text-slate-500 dark:text-slate-400">
                  <input
                    type="checkbox"
                    checked={open}
                    onChange={(e) =>
                      setOpenSections((s) => ({ ...s, [section.category]: e.target.checked }))
                    }
                  />
                  {t('portal.showAdvancedParams')}
                </label>
              )}
            </section>
          );
        })
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
    </div>
  );
}
