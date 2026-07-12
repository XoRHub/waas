import { useTranslation } from 'react-i18next';
import type { TemplateProtocolInput } from '@/hooks/useApi';
import { ProtocolParamsForm, ProtocolTabs } from '@/components/ProtocolTabs';
import { categoryDelegated, toggleCategory, toggleName } from '@/lib/userParams';
import type { ProtocolMeta } from '@/types';
import { fieldSm } from './fields';

/**
 * The protocols section of the template editor: one tab per configured
 * protocol (port, default radio, credentials secret, registry-driven
 * params with the admin delegation toggles). The protocol LIST and its
 * invariants (unique names, kasmvnc exclusivity, one default) are owned
 * by TemplateDialog — this component only renders and reports edits.
 */
export function ProtocolsFieldset({
  protocols,
  meta,
  active,
  onSelect,
  addable,
  onAdd,
  onRemove,
  onPatchActive,
  onMakeDefault,
}: {
  protocols: TemplateProtocolInput[];
  meta: ProtocolMeta[] | undefined;
  active: string;
  onSelect: (name: string) => void;
  /** Registry protocols not configured yet (exclusivity already applied). */
  addable: string[];
  onAdd: (name: string) => void;
  onRemove: (name: string) => void;
  /** Patch the ACTIVE protocol entry (dialog state stays authoritative). */
  onPatchActive: (patch: Partial<TemplateProtocolInput>) => void;
  /** Make the active protocol the template default. */
  onMakeDefault: () => void;
}) {
  const { t } = useTranslation();
  const currentProto = protocols.find((p) => p.name === active);

  return (
    <fieldset className="space-y-3 rounded-lg border border-slate-200 p-3 dark:border-slate-700">
      <legend className="px-1 text-sm font-medium text-slate-700 dark:text-slate-200">
        {t('admin.templatesPage.protocols')}
      </legend>
      <p className="text-xs text-slate-400 dark:text-slate-500">
        {t('admin.templatesPage.protocolsHint')}
      </p>
      <ProtocolTabs
        protocols={protocols.map((p) => p.name)}
        active={active}
        onSelect={onSelect}
        badge={(p) =>
          protocols.find((x) => x.name === p)?.default ? (
            <span className="text-[10px]" title={t('portal.protocolDefault')}>
              ●
            </span>
          ) : null
        }
        addable={addable}
        onAdd={onAdd}
        onRemove={onRemove}
        // Zero protocols is a valid template (legacy OS-derived
        // fallback) — also the only way out of a kasmvnc-only
        // template, whose exclusivity blocks any addition.
        allowEmpty
      />
      {protocols.length === 0 && (
        <p className="text-xs text-slate-400 dark:text-slate-500">
          {t('admin.templatesPage.noProtocolsYet')}
        </p>
      )}
      {currentProto ? (
        <div className="space-y-3">
          <div className="flex items-end gap-3">
            <label className="block w-24">
              <span className="text-xs text-slate-500 dark:text-slate-400">
                {t('admin.templatesPage.port')}
              </span>
              <input
                type="number"
                className={fieldSm}
                value={currentProto.port || ''}
                min={1}
                max={65535}
                onChange={(e) => onPatchActive({ port: Number(e.target.value) })}
                required
              />
            </label>
            <label className="flex items-center gap-1.5 pb-2 text-sm text-slate-600 dark:text-slate-300">
              <input
                type="radio"
                name="default-protocol"
                checked={!!currentProto.default}
                onChange={onMakeDefault}
              />
              {t('portal.protocolDefault')}
            </label>
          </div>

          <label className="block">
            <span className="text-xs text-slate-500 dark:text-slate-400">
              {t('admin.templatesPage.credentialsSecret')}
            </span>
            <input
              className={fieldSm}
              value={currentProto.credentialsSecretRef ?? ''}
              onChange={(e) => onPatchActive({ credentialsSecretRef: e.target.value })}
              placeholder={t('admin.templatesPage.credentialsSecretHint')}
            />
          </label>

          {/* Same registry-driven form as the user connection settings,
                with the admin extras: a per-param delegation toggle
                (locked / user) plus a per-section "allow the whole
                category" toggle that writes a cat:X selector into
                userParams (individual names of the category are then
                absorbed). Editor placement stays tier-driven; the raw
                list (cat: intact) is what gets edited — the api-server
                resolves it for the connect-time forms. */}
          <ProtocolParamsForm
            key={currentProto.name}
            meta={meta}
            protocol={currentProto.name}
            values={currentProto.params ?? {}}
            onChange={(name, value) => {
              const params = { ...currentProto.params };
              if (value === '') delete params[name];
              else params[name] = value;
              onPatchActive({ params });
            }}
            audioPortExposed={currentProto.exposeAudioPort ?? false}
            onAudioPortChange={(exposed) => onPatchActive({ exposeAudioPort: exposed })}
            renderSectionExtra={(category) => {
              const full = categoryDelegated(currentProto.userParams, category);
              return (
                <label
                  className={`flex items-center gap-1 text-[11px] ${
                    full
                      ? 'font-medium text-blue-600 dark:text-blue-400'
                      : 'text-slate-500 dark:text-slate-400'
                  }`}
                  title={t('admin.templatesPage.allowCategoryHint')}
                >
                  <input
                    type="checkbox"
                    checked={full}
                    onChange={(e) => {
                      const categoryNames = (meta ?? [])
                        .find((m) => m.name === currentProto.name)
                        ?.params?.filter((p) => p.category === category)
                        .map((p) => p.name);
                      onPatchActive({
                        userParams: toggleCategory(
                          currentProto.userParams,
                          category,
                          categoryNames ?? [],
                          e.target.checked,
                        ),
                      });
                    }}
                  />
                  {full
                    ? t('admin.templatesPage.categoryAllowed')
                    : t('admin.templatesPage.allowCategory')}
                </label>
              );
            }}
            renderParamExtra={(pm) => {
              // A cat:X selector delegates the whole section: the
              // per-param toggle goes inert (visibly delegated, not
              // hidden) until the category toggle is released.
              const viaCategory = categoryDelegated(currentProto.userParams, pm.category);
              const level: 'locked' | 'user' =
                viaCategory || currentProto.userParams?.includes(pm.name) ? 'user' : 'locked';
              return (
                <div className="flex items-center gap-1.5 text-[11px] text-slate-500 dark:text-slate-400">
                  <span
                    className={`inline-flex divide-x divide-slate-300 overflow-hidden rounded border border-slate-300 dark:divide-slate-600 dark:border-slate-600 ${
                      viaCategory ? 'opacity-50' : ''
                    }`}
                    title={viaCategory ? t('admin.templatesPage.categoryAllowed') : undefined}
                  >
                    {(['locked', 'user'] as const).map((lvl) => (
                      <button
                        key={lvl}
                        type="button"
                        aria-pressed={level === lvl}
                        disabled={viaCategory}
                        onClick={() =>
                          onPatchActive({
                            userParams: toggleName(
                              currentProto.userParams,
                              pm.name,
                              lvl === 'user',
                            ),
                          })
                        }
                        className={`px-1.5 py-0.5 text-[10px] ${
                          level === lvl
                            ? 'bg-blue-600 font-medium text-white'
                            : 'bg-white text-slate-600 hover:bg-slate-100 dark:bg-slate-700 dark:text-slate-300 dark:hover:bg-slate-600'
                        } ${viaCategory ? 'cursor-not-allowed' : ''}`}
                      >
                        {t(`admin.templatesPage.override${lvl[0].toUpperCase()}${lvl.slice(1)}`)}
                      </button>
                    ))}
                  </span>
                  {pm.tier === 'advanced' && (
                    <span className="rounded bg-amber-100 px-1 text-[10px] uppercase text-amber-700 dark:bg-amber-900/50 dark:text-amber-300">
                      {t('admin.templatesPage.advanced')}
                    </span>
                  )}
                </div>
              );
            }}
          />
        </div>
      ) : null}
    </fieldset>
  );
}
