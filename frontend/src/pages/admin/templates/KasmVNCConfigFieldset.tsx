import { useMemo, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { useAdminImages, useKasmVNCManagedKeys } from '@/hooks/useApi';
import { BoolField, ParamField, Segmented } from '@/components/ParamField';
import { TabbedPanels } from '@/components/TabbedPanels';
import { parseYaml, YamlEditor } from '@/components/YamlEditor';
import {
  coerce,
  leaves,
  parseDefaults,
  removePath,
  scalarText,
  setPath,
  toText,
  type KasmOption,
  type Tree,
} from '@/lib/kasmvnc';
import { findDiscovered } from '@/lib/templates';
import type { ParamMeta } from '@/types';
import { fieldSm } from './fields';
import { validateKasmVNCConfig } from './validate';

/**
 * The ONLY KasmVNC knowledge WaaS maintains: which keys the everyday
 * controls map to, and the values each choice writes (one per key, in
 * key order). Everything else — the option list, control types,
 * defaults, hints — comes from the image's own kasmvnc_defaults.yaml.
 * An essential whose key the image lacks is not offered. Units checked
 * against KasmVNC 1.5.1's vncserver: idle_timeout in seconds (or
 * `never`), clipboard size in bytes (or `unlimited`).
 */
const ESSENTIALS: {
  /** Suffix of the admin.templatesPage.kasmvnc* label key. */
  id: string;
  keys: string[];
  /** Omitted = tri-state bool on the single key. A choice is labelled
   * verbatim (`label`) or by an i18n suffix (`labelKey`). */
  choices?: { label?: string; labelKey?: string; values: unknown[] }[];
  segmented?: boolean;
}[] = [
  {
    id: 'Resolution',
    keys: ['desktop.resolution.width', 'desktop.resolution.height'],
    choices: [
      { label: '1280×720', values: [1280, 720] },
      { label: '1920×1080', values: [1920, 1080] },
      { label: '2560×1440', values: [2560, 1440] },
    ],
  },
  { id: 'AutoResize', keys: ['desktop.allow_resize'] },
  {
    id: 'MaxFrameRate',
    keys: ['encoding.max_frame_rate'],
    choices: [30, 24, 15].map((fps) => ({ label: String(fps), values: [fps] })),
  },
  {
    id: 'Quality',
    keys: ['encoding.rect_encoding_mode.min_quality', 'encoding.rect_encoding_mode.max_quality'],
    choices: [
      { labelKey: 'QualityEconomy', values: [4, 6] },
      { labelKey: 'QualityHigh', values: [8, 9] },
    ],
    segmented: true,
  },
  {
    id: 'IdleTimeout',
    keys: ['user_session.idle_timeout'],
    choices: [
      { labelKey: 'Idle15', values: [900] },
      { labelKey: 'Idle30', values: [1800] },
      { labelKey: 'Idle60', values: [3600] },
    ],
  },
  { id: 'IgnoreNumlock', keys: ['keyboard.ignore_numlock'] },
  { id: 'GameMode', keys: ['pointer.game_mode'] },
  {
    id: 'ClipboardSize',
    keys: [
      'data_loss_prevention.clipboard.server_to_client.size',
      'data_loss_prevention.clipboard.client_to_server.size',
    ],
    choices: [
      { label: '1 MB', values: [1048576, 1048576] },
      { label: '10 MB', values: [10485760, 10485760] },
    ],
  },
];

/** Label + control + hint, laid out like ParamField (which can't host
 * a friendly label: its label IS the parameter name). */
function Row({ label, hint, children }: { label: string; hint: string; children: ReactNode }) {
  return (
    <div className="block">
      <span className="text-xs text-slate-500 dark:text-slate-400">{label}</span>
      {children}
      <span className="mt-0.5 block font-mono text-[11px] leading-tight text-slate-400 dark:text-slate-500">
        {hint}
      </span>
    </div>
  );
}

/**
 * Template-level kasmvnc.yaml override editor. The caller gates this on
 * the whole protocol list, not the active tab — same guard the webhook
 * enforces ("kasmvncConfig requires a kasmvnc protocol entry"). This
 * edits the admin OVERRIDE layer only: KasmVNC merges it over the image
 * defaults, and the clipboard DLP keys are policy-owned (the webhook
 * rejects them here).
 *
 * The YAML text stays the single source of truth (what the CR stores);
 * the Essentials/Advanced tabs read it as a tree and write it back on
 * each edit, so the three tabs never disagree and anything they cannot
 * represent (a nested list…) survives untouched. The option schema is
 * the catalog's `kasmvnc.defaults` of the template's image.
 */
export function KasmVNCConfigFieldset({
  value,
  onChange,
  image,
}: {
  value: string;
  onChange: (text: string) => void;
  /** The template's image reference — looked up in the catalogs. */
  image: string;
}) {
  const { t } = useTranslation();
  const images = useAdminImages();
  const managedKeys = useKasmVNCManagedKeys().data?.data ?? [];
  const info = findDiscovered(images.data?.data, image)?.kasmvnc;
  const options = useMemo(() => parseDefaults(info?.defaults ?? ''), [info?.defaults]);
  const known = options.length > 0;
  const byKey = new Map(options.map((o) => [o.key, o]));

  const parsed = useMemo(() => parseYaml(value, validateKasmVNCConfig), [value]);
  // null = the YAML is broken or not a mapping: the structured tabs
  // must not rewrite what the admin is in the middle of fixing.
  const tree = parsed.issues.length > 0 ? null : ((parsed.value ?? {}) as Tree);
  const current = tree ? leaves(tree) : {};
  const update = (edit: (next: Tree) => void) => {
    if (!tree) return;
    const next = structuredClone(tree);
    edit(next);
    onChange(toText(next));
  };
  const setKey = (next: Tree, key: string, text: string) => {
    if (text === '') removePath(next, key);
    else setPath(next, key, coerce(text, byKey.get(key)?.kind ?? 'string'));
  };

  const orphans = known ? Object.keys(current).filter((k) => !byKey.has(k)) : [];
  const changed = Object.keys(current).length;
  const defaultLabel = (keys: string[]) => {
    const texts = [...new Set(keys.map((k) => scalarText(byKey.get(k)?.default)))].filter(Boolean);
    return texts.length
      ? t('admin.templatesPage.kasmvncImageDefaultValue', { value: texts.join(' × ') })
      : t('admin.templatesPage.kasmvncImageDefault');
  };

  const essentials = (
    <div className="space-y-3">
      {ESSENTIALS.map(({ id, keys, choices, segmented }) => {
        const label = t(`admin.templatesPage.kasmvnc${id}`);
        const hint = keys.join(', ');
        if (known && !keys.every((k) => byKey.has(k))) {
          return (
            <Row key={id} label={label} hint="">
              <span className="mt-0.5 block text-xs text-slate-400 dark:text-slate-500">
                {t('admin.templatesPage.kasmvncUnavailable', {
                  key: keys.find((k) => !byKey.has(k)),
                })}
              </span>
            </Row>
          );
        }
        if (!choices) {
          const key = keys[0];
          return (
            <Row key={id} label={label} hint={hint}>
              <BoolField
                defaultValue={scalarText(byKey.get(key)?.default)}
                value={key in current ? scalarText(current[key]) : ''}
                onChange={(text) => update((next) => setKey(next, key, text))}
              />
            </Row>
          );
        }
        // The selected choice is the one whose values ALL keys carry;
        // a partial or foreign value reads as "image default" (the
        // Advanced/YAML tabs show the detail).
        const selected = String(
          choices.findIndex((c) => c.values.every((v, i) => current[keys[i]] === v)),
        );
        const segments = [
          { value: '-1', label: defaultLabel(keys) },
          ...choices.map((c, i) => ({
            value: String(i),
            label: c.labelKey ? t(`admin.templatesPage.kasmvnc${c.labelKey}`) : (c.label ?? ''),
          })),
        ];
        const pick = (index: string) =>
          update((next) =>
            keys.forEach((key, i) =>
              setKey(next, key, index === '-1' ? '' : scalarText(choices[Number(index)].values[i])),
            ),
          );
        return (
          <Row key={id} label={label} hint={hint}>
            {segmented ? (
              <Segmented segments={segments} value={selected} onChange={pick} />
            ) : (
              <select
                className={fieldSm}
                value={selected}
                onChange={(e) => pick(e.target.value)}
                aria-label={label}
              >
                {segments.map((s) => (
                  <option key={s.value} value={s.value}>
                    {s.label}
                  </option>
                ))}
              </select>
            )}
          </Row>
        );
      })}
      <Row label={t('admin.templatesPage.kasmvncClipboard')} hint={managedKeys.join(', ')}>
        <span className="mt-0.5 block rounded-md border border-dashed border-slate-300 px-3 py-1.5 text-xs text-slate-500 dark:border-slate-600 dark:text-slate-400">
          🔒 {t('admin.templatesPage.kasmvncClipboardManaged')}
        </span>
      </Row>
    </div>
  );

  const [query, setQuery] = useState('');
  const needle = query.trim().toLowerCase();
  const sections = new Map<string, KasmOption[]>();
  for (const o of options) {
    if (needle && !o.key.includes(needle) && !o.hint?.toLowerCase().includes(needle)) continue;
    const section = o.key.split('.')[0];
    sections.set(section, [...(sections.get(section) ?? []), o]);
  }
  const advanced = !known ? (
    <p className="text-xs text-slate-400 dark:text-slate-500">
      {t('admin.templatesPage.kasmvncSchemaUnknown')}
    </p>
  ) : (
    <div className="space-y-3">
      <p className="text-xs text-slate-400 dark:text-slate-500">
        {t('admin.templatesPage.kasmvncAdvancedHint')}
      </p>
      <input
        type="search"
        className={fieldSm}
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        placeholder={t('admin.templatesPage.kasmvncSearch')}
        aria-label={t('admin.templatesPage.kasmvncSearch')}
      />
      {sections.size === 0 && (
        <p className="text-xs text-slate-400 dark:text-slate-500">
          {t('admin.templatesPage.kasmvncNoMatch', { query })}
        </p>
      )}
      {[...sections].map(([section, opts]) => (
        <div key={section} className="space-y-2">
          <h4 className="font-mono text-xs font-medium text-slate-700 dark:text-slate-200">
            {section}
          </h4>
          {opts.map((o) => {
            const managed = managedKeys.includes(o.key);
            const modified = o.key in current;
            // ParamField's widgets follow the guacd registry's kinds;
            // a list is free text here (comma-separated).
            const meta: ParamMeta = {
              name: o.key.slice(section.length + 1),
              kind: o.kind === 'bool' || o.kind === 'int' ? o.kind : 'string',
              default: scalarText(o.default),
              description: o.hint ?? '',
              protocols: ['kasmvnc'],
              tier: 'ui',
              category: 'display',
              live: false,
            };
            return (
              <div
                key={o.key}
                data-key={o.key}
                className={`rounded-md p-2 ${modified ? 'bg-blue-50 dark:bg-blue-900/20' : ''}`}
              >
                {managed && (
                  <span className="float-right text-[11px] text-amber-600 dark:text-amber-400">
                    🔒 {t('admin.templatesPage.kasmvncManaged')}
                  </span>
                )}
                <ParamField
                  meta={meta}
                  value={modified ? scalarText(current[o.key]) : ''}
                  onChange={(text) => update((next) => setKey(next, o.key, text))}
                  disabled={managed}
                />
              </div>
            );
          })}
        </div>
      ))}
    </div>
  );

  const fixFirst = (
    <p className="text-xs text-amber-600 dark:text-amber-400">
      {t('admin.templatesPage.kasmvncFixYaml')}
    </p>
  );

  return (
    <fieldset className="space-y-2 rounded-lg border border-slate-200 p-3 dark:border-slate-700">
      <legend className="px-1 text-sm font-medium text-slate-700 dark:text-slate-200">
        {t('admin.templatesPage.kasmvncConfig')}
      </legend>
      <p className="text-xs text-slate-400 dark:text-slate-500">
        {t('admin.templatesPage.kasmvncConfigHint')}{' '}
        <a
          href="https://kasmweb.com/kasmvnc/docs/latest/configuration.html"
          target="_blank"
          rel="noreferrer"
          className="text-blue-600 underline dark:text-blue-400"
        >
          {t('admin.templatesPage.kasmvncConfigDocLink')}
        </a>
      </p>
      <p className="text-xs text-slate-500 dark:text-slate-400">
        {known
          ? t('admin.templatesPage.kasmvncSchema', {
              version: info?.version,
              count: options.length,
            })
          : t('admin.templatesPage.kasmvncSchemaUnknown')}
      </p>
      {orphans.length > 0 && (
        <div className="space-y-1 rounded-md border border-amber-300 bg-amber-50 p-2 text-xs text-amber-700 dark:border-amber-700 dark:bg-amber-900/20 dark:text-amber-300">
          <p>{t('admin.templatesPage.kasmvncOrphans')}</p>
          {orphans.map((key) => (
            <div key={key} className="flex items-center justify-between gap-2">
              <code className="font-mono">
                {key}: {scalarText(current[key])}
              </code>
              <button
                type="button"
                onClick={() => update((next) => removePath(next, key))}
                className="rounded border border-amber-400 px-2 py-0.5 hover:bg-amber-100 dark:border-amber-600 dark:hover:bg-amber-900/40"
              >
                {t('admin.templatesPage.kasmvncRemove')}
              </button>
            </div>
          ))}
        </div>
      )}
      <TabbedPanels
        tabs={[
          {
            id: 'kasm-essentials',
            label: t('admin.templatesPage.kasmvncTabEssentials'),
            content: tree ? essentials : fixFirst,
          },
          {
            id: 'kasm-advanced',
            label: t('admin.templatesPage.kasmvncTabAdvanced'),
            content: tree ? advanced : fixFirst,
          },
          {
            id: 'kasm-yaml',
            label: t('admin.templatesPage.kasmvncTabYaml'),
            content: (
              <YamlEditor
                value={value}
                onChange={onChange}
                rows={8}
                validate={validateKasmVNCConfig}
                placeholder={t('admin.templatesPage.kasmvncConfigPlaceholder')}
              />
            ),
          },
        ]}
      />
      <div className="flex items-center justify-between gap-2 text-xs text-slate-500 dark:text-slate-400">
        <span>
          {changed === 0
            ? t('admin.templatesPage.kasmvncUnchanged')
            : t('admin.templatesPage.kasmvncChanged', { count: changed })}
        </span>
        <button
          type="button"
          onClick={() => onChange('')}
          disabled={value === ''}
          className="rounded-md border border-slate-300 px-2 py-1 disabled:opacity-50 dark:border-slate-600 dark:text-slate-200"
        >
          {t('admin.templatesPage.kasmvncResetAll')}
        </button>
      </div>
    </fieldset>
  );
}
