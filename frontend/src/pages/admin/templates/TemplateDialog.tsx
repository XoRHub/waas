import { useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { stringify as yamlStringify } from 'yaml';
import {
  useOverrideFields,
  usePlaceholders,
  useProtocolMeta,
  useSaveTemplate,
  type TemplateInput,
  type TemplateProtocolInput,
} from '@/hooks/useApi';
import { Dialog } from '@/components/Dialog';
import { parseYaml } from '@/components/YamlEditor';
import type { DeploymentRecommendation, RecommendedEnvVar } from '@/types';
import { EnvFieldset } from './EnvFieldset';
import { IdentityFields } from './IdentityFields';
import { KasmVNCConfigFieldset } from './KasmVNCConfigFieldset';
import { OverridesFieldset } from './OverridesFieldset';
import { PlacementFieldset } from './PlacementFieldset';
import { ProtocolsFieldset } from './ProtocolsFieldset';
import { ResourcesFieldset } from './ResourcesFieldset';
import { ScheduleFieldset } from './ScheduleFieldset';
import { WorkloadSection } from './WorkloadSection';
import { validateWorkload } from './validate';

export const DEFAULT_PORTS: Record<string, number> = {
  vnc: 5901,
  rdp: 3389,
  ssh: 2222,
  kasmvnc: 6901,
};

/**
 * The template create/edit form. This component owns the SINGLE source
 * of truth (input/workloadText/activeProto and the protocol-list
 * invariants) plus the submit flow; each section below renders one
 * facet and reports edits through callbacks — no section keeps state.
 */
export function TemplateDialog({
  isNew,
  initial,
  onClose,
}: {
  isNew: boolean;
  initial: TemplateInput;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const save = useSaveTemplate();
  const meta = useProtocolMeta();
  const placeholders = usePlaceholders();
  // The overridable fields come from the server registry (single source
  // shared with the policy editor and the enforcement) — this page holds
  // no local copy of the list.
  const overrideFields = useOverrideFields();
  const [input, setInput] = useState(initial);
  // Workload edited as YAML (converted transparently: the API/CR still
  // stores the structured value).
  const [workloadText, setWorkloadText] = useState(
    initial.workload ? yamlStringify(initial.workload) : '',
  );
  const [workloadError, setWorkloadError] = useState('');
  const [workloadOpen, setWorkloadOpen] = useState(false);

  const set = (patch: Partial<TemplateInput>) => setInput((prev) => ({ ...prev, ...patch }));

  // Env hints the catalog recommends WITHOUT a default: offered as
  // greyed suggestions in EnvFieldset instead of real env rows, so an
  // empty var never supersedes the operator's own injection — the
  // admin adopts one explicitly (adoptSuggestion). Transient UI state:
  // never persisted, gone on dialog reopen.
  const [envSuggestions, setEnvSuggestions] = useState<
    { name: string; description?: string; adopted: boolean }[]
  >([]);
  const adoptSuggestion = (name: string) => {
    setEnvSuggestions((prev) => prev.map((s) => (s.name === name ? { ...s, adopted: true } : s)));
    set({ env: [...(input.env ?? []), { name, value: '' }] });
  };

  // Explicit "apply catalog recommendation" action (CatalogImageField's
  // button) — never triggered by selecting an image. Unlike a silent
  // auto-fill, an explicit click overwriting existing YAML is not a
  // surprise, so podSecurityContext/securityContext/volumes are set
  // unconditionally; env is merged by name so an already-configured
  // var is never clobbered (see EnvFieldset/mergeEnv doctrine).
  // Protocol-aware: on a template with no protocols yet, the image's
  // supported protocols are added first; env hints are then filtered
  // to the protocols the template actually uses.
  const applyRecommendation = (recommended: DeploymentRecommendation, imageProtocols: string[]) => {
    const { value } = parseYaml(workloadText);
    const base = (
      value && typeof value === 'object' && !Array.isArray(value) ? value : {}
    ) as Record<string, unknown>;
    const next: Record<string, unknown> = { ...base };
    if (recommended.podSecurityContext) next.podSecurityContext = recommended.podSecurityContext;
    if (recommended.securityContext) next.securityContext = recommended.securityContext;
    if (recommended.volumes?.length) {
      // RecommendedVolume (name/mountPath/readOnly) expands to the
      // corev1.Volume/VolumeMount pair the workload actually needs —
      // always a plain emptyDir (see shared/catalog.RecommendedVolume).
      next.volumes = recommended.volumes.map((v) => ({ name: v.name, emptyDir: {} }));
      next.volumeMounts = recommended.volumes.map((v) => ({
        name: v.name,
        mountPath: v.mountPath,
        readOnly: v.readOnly,
      }));
    }
    setWorkloadText(yamlStringify(next));

    let nextProtocols = protocols;
    if (protocols.length === 0 && imageProtocols.length > 0) {
      // kasmvnc exclusivity (webhook-enforced): a mixed supported list
      // keeps only the guacd protocols; kasmvnc-only adds kasmvnc.
      const guacd = imageProtocols.filter((p) => p !== 'kasmvnc');
      const names = [...new Set(guacd.length > 0 ? guacd : imageProtocols)];
      nextProtocols = names.map((name, i) => ({
        name,
        port: DEFAULT_PORTS[name] ?? 0,
        default: i === 0,
      }));
      setActiveProto(names[0]);
    }
    const targetSet = new Set(nextProtocols.map((p) => p.name));

    // A hint is relevant when protocol-unscoped or intersecting the
    // target protocols (none at all ⇒ everything applies). Relevance
    // then closes over `requires`: a required sibling is pulled in
    // regardless of its own protocols.
    const hints = recommended.env ?? [];
    const byName = new Map(hints.map((h) => [h.name, h]));
    const applied = new Map<string, RecommendedEnvVar>();
    const queue = hints.filter(
      (h) =>
        !h.protocols?.length || targetSet.size === 0 || h.protocols.some((p) => targetSet.has(p)),
    );
    while (queue.length > 0) {
      const hint = queue.shift()!;
      if (applied.has(hint.name)) continue;
      applied.set(hint.name, hint);
      for (const req of hint.requires ?? []) {
        const sibling = byName.get(req);
        if (sibling) queue.push(sibling);
      }
    }

    const existingNames = new Set((input.env ?? []).map((e) => e.name));
    const additions = [...applied.values()]
      .filter((h) => h.default != null && !existingNames.has(h.name))
      .map((h) => ({ name: h.name, value: h.default! }));
    const suggested = [...applied.values()]
      .filter((h) => h.default == null && !existingNames.has(h.name))
      .map((h) => ({ name: h.name, description: h.description, adopted: false }));
    set({
      ...(nextProtocols !== protocols ? { protocols: nextProtocols } : {}),
      ...(additions.length > 0 ? { env: [...(input.env ?? []), ...additions] } : {}),
    });
    // Re-deriving drops stale non-adopted suggestions; adopted ones
    // stay recorded so their description keeps feeding the value
    // placeholder of the row they became.
    setEnvSuggestions((prev) => [
      ...prev.filter((s) => s.adopted),
      ...suggested.filter((s) => !prev.some((p) => p.adopted && p.name === s.name)),
    ]);

    setWorkloadOpen(true);
  };

  /** The standard architecture scheduling label (kubernetes.io/arch). */
  const ARCH_LABEL = 'kubernetes.io/arch';
  // Arch prefill on catalog selection: exactly one published
  // architecture stamps the standard label into the workload's
  // nodeSelector; a multi-arch (or unknown) pick removes a stale
  // ARCH_LABEL instead — other nodeSelector keys are never touched.
  // Narrow, deliberate exception to the no-silent-autofill doctrine
  // above: the picker click is itself the explicit action, and only
  // this single platform-derived key is ever written. (The operator
  // already constrains scheduling from the ENTRY-level architectures
  // via node affinity; this makes the per-image constraint visible and
  // editable on the template.)
  const applyArchitectures = (archs: string[]) => {
    const { value, issues } = parseYaml(workloadText);
    // Never rewrite YAML the admin has left unparseable.
    if (issues.length > 0) return;
    const base = (
      value && typeof value === 'object' && !Array.isArray(value) ? value : {}
    ) as Record<string, unknown>;
    const rawSel = base.nodeSelector;
    const sel = {
      ...((rawSel && typeof rawSel === 'object' && !Array.isArray(rawSel) ? rawSel : {}) as Record<
        string,
        unknown
      >),
    };
    if (archs.length === 1) {
      if (sel[ARCH_LABEL] === archs[0]) return;
      sel[ARCH_LABEL] = archs[0];
    } else {
      if (!(ARCH_LABEL in sel)) return;
      delete sel[ARCH_LABEL];
    }
    const next = { ...base };
    if (Object.keys(sel).length > 0) next.nodeSelector = sel;
    else delete next.nodeSelector;
    setWorkloadText(Object.keys(next).length > 0 ? yamlStringify(next) : '');
    setWorkloadOpen(true);
  };
  const protocols = input.protocols ?? [];
  const [activeProto, setActiveProto] = useState(protocols[0]?.name ?? '');
  const patchActive = (patch: Partial<TemplateProtocolInput>) => {
    set({ protocols: protocols.map((p) => (p.name === activeProto ? { ...p, ...patch } : p)) });
  };

  const availableProtocols = (meta.data?.data ?? []).map((m) => m.name);
  // A template declares each protocol at most once (webhook-enforced):
  // the shared "+" menu offers only the registry protocols not
  // configured yet — the admin picks explicitly which one to add.
  // kasmvnc is exclusive (it bypasses guacd; the webhook rejects any
  // combination with vnc/rdp/ssh): once present nothing else is
  // addable, and it is only offered while the protocol list is empty.
  const unusedProtocols = availableProtocols.filter((p) => {
    if (protocols.some((x) => x.name === p)) return false;
    if (protocols.some((x) => x.name === 'kasmvnc')) return false;
    if (p === 'kasmvnc' && protocols.length > 0) return false;
    return true;
  });
  const addProtocol = (name: string) => {
    set({
      protocols: [
        ...protocols,
        { name, port: DEFAULT_PORTS[name] ?? 0, default: protocols.length === 0 },
      ],
    });
    setActiveProto(name);
  };
  const removeProtocol = (name: string) => {
    const next = protocols.filter((p) => p.name !== name);
    // Keep exactly one default among the survivors.
    if (next.length > 0 && !next.some((p) => p.default)) next[0] = { ...next[0], default: true };
    // kasmvncConfig only means something to a kasmvnc endpoint (the
    // server rejects the leftover) and its editor is hidden without
    // one: a stale value would block the save invisibly.
    set({ protocols: next, ...(name === 'kasmvnc' ? { kasmvncConfig: '' } : {}) });
    setActiveProto(next[0]?.name ?? '');
  };

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    let workload: Record<string, unknown> | undefined;
    if (workloadText.trim() !== '') {
      const { value, issues } = parseYaml(workloadText, validateWorkload);
      if (issues.length > 0 || value === undefined) {
        setWorkloadError(t('admin.templatesPage.workloadInvalid'));
        return;
      }
      workload = value as Record<string, unknown>;
    }
    setWorkloadError('');
    save.mutate({ isNew, input: { ...input, workload } }, { onSuccess: onClose });
  };

  return (
    <Dialog
      title={
        isNew ? t('admin.templatesPage.new') : t('admin.templatesPage.edit', { name: input.name })
      }
      onClose={onClose}
      onSubmit={onSubmit}
      maxWidth="max-w-2xl"
      footer={
        <>
          {save.isError && <p className="mr-auto text-sm text-red-600">{save.error.message}</p>}
          <button
            type="button"
            onClick={onClose}
            className="rounded-md border border-slate-300 px-4 py-2 text-sm dark:border-slate-600 dark:text-slate-200"
          >
            {t('app.cancel')}
          </button>
          <button
            type="submit"
            disabled={save.isPending}
            className="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
          >
            {t('app.save')}
          </button>
        </>
      }
    >
      <IdentityFields
        input={input}
        isNew={isNew}
        onPatch={set}
        onApplyRecommendation={applyRecommendation}
        onArchitectures={applyArchitectures}
      />

      <ResourcesFieldset requests={input.requests} limits={input.limits} onPatch={set} />

      <ProtocolsFieldset
        protocols={protocols}
        meta={meta.data?.data}
        active={activeProto}
        onSelect={setActiveProto}
        addable={unusedProtocols}
        onAdd={addProtocol}
        onRemove={removeProtocol}
        onPatchActive={patchActive}
        onMakeDefault={() =>
          set({ protocols: protocols.map((p) => ({ ...p, default: p.name === activeProto })) })
        }
      />

      {/* Gated on the whole protocol list, not the active tab — same
          guard the webhook enforces ("kasmvncConfig requires a kasmvnc
          protocol entry"). */}
      {protocols.some((p) => p.name === 'kasmvnc') && (
        <KasmVNCConfigFieldset
          value={input.kasmvncConfig ?? ''}
          onChange={(text) => set({ kasmvncConfig: text })}
        />
      )}

      <EnvFieldset
        env={input.env}
        onChange={(env) => set({ env })}
        suggestions={envSuggestions.filter((s) => !s.adopted)}
        onAdopt={adoptSuggestion}
        valuePlaceholders={Object.fromEntries(
          envSuggestions.filter((s) => s.description).map((s) => [s.name, s.description!]),
        )}
      />

      <OverridesFieldset
        overrides={input.overrides}
        fields={overrideFields.data?.data ?? []}
        onChange={(overrides) => set({ overrides })}
      />

      <PlacementFieldset
        placement={input.placement}
        placeholders={placeholders.data?.data ?? []}
        onChange={(placement) => set({ placement })}
      />

      <ScheduleFieldset value={input.schedule} onChange={(schedule) => set({ schedule })} />

      <WorkloadSection
        text={workloadText}
        onChange={setWorkloadText}
        error={workloadError}
        open={workloadOpen}
        onToggle={setWorkloadOpen}
      />
    </Dialog>
  );
}
