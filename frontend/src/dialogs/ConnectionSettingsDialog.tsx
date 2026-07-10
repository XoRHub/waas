import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Dialog } from '@/components/Dialog';
import { ProtocolParamsForm, ProtocolTabs } from '@/components/ProtocolTabs';
import {
  useProtocolMeta,
  useUpdateProfile,
  useUpdateWorkspaceOverrides,
  useWorkspaceKasmVNCConfig,
} from '@/hooks/useApi';
import { WorkspaceRuntimeForm } from '@/dialogs/WorkspaceRuntimeForm';
import { useAuthStore } from '@/stores/authStore';
import type { Workspace } from '@/types';

const RUNTIME_FORM_ID = 'workspace-runtime-form';

// ConnectionSettingsDialog: two top-level tabs. "Connection" tunes the
// guacd parameters per protocol (VNC/RDP/SSH) and the connection choice,
// saved in the profile and re-validated server-side at connect time.
// "Workspace" reconfigures the instantiated workspace itself (env, node
// placement, sizing) through PATCH /workspaces/{id}/overrides — applied
// at the next stop/start boundary or via the drift badge reload.
export function ConnectionSettingsDialog({
  workspace,
  onClose,
}: {
  workspace: Workspace;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const user = useAuthStore((s) => s.user);
  const updateProfile = useUpdateProfile();
  const updateOverrides = useUpdateWorkspaceOverrides();
  const meta = useProtocolMeta();
  const [topTab, setTopTab] = useState<'connection' | 'workspace'>('connection');
  const saved = user?.preferences?.workspaceSettings?.[workspace.id];
  const protocols = workspace.protocols ?? [];
  const names = protocols.map((p) => p.name);
  const defaultProtocol = protocols.find((p) => p.default)?.name ?? workspace.protocol ?? '';
  const initialChosen = saved?.protocol || defaultProtocol;
  const [chosen, setChosen] = useState(initialChosen);
  const [tab, setTab] = useState(names.includes(initialChosen) ? initialChosen : (names[0] ?? ''));
  // Params kept per protocol so switching tabs never loses edits.
  const [paramsByProto, setParamsByProto] = useState<Record<string, Record<string, string>>>(
    () => ({
      ...saved?.paramsByProtocol,
      ...(saved?.params ? { [initialChosen]: saved.params } : {}),
    }),
  );

  const selected = protocols.find((p) => p.name === tab);
  const isAdmin = user?.role === 'admin';
  // Existing workspace: the value that matters is the EFFECTIVE config
  // the operator materialized (template + policy clipboard layer), read
  // from its per-workspace ConfigMap through the API — not the template's
  // raw text.
  const kasmCfg = useWorkspaceKasmVNCConfig(
    workspace.id,
    protocols.some((p) => p.name === 'kasmvnc'),
  );

  const onSave = () => {
    const cleanedByProto: Record<string, Record<string, string>> = {};
    for (const [proto, params] of Object.entries(paramsByProto)) {
      const cleaned = Object.fromEntries(Object.entries(params).filter(([, v]) => v !== ''));
      if (Object.keys(cleaned).length > 0) cleanedByProto[proto] = cleaned;
    }
    const chosenParams = cleanedByProto[chosen];
    const settings = { ...user?.preferences?.workspaceSettings };
    if (chosen === defaultProtocol && Object.keys(cleanedByProto).length === 0) {
      delete settings[workspace.id];
    } else {
      settings[workspace.id] = {
        protocol: chosen !== defaultProtocol ? chosen : undefined,
        params: chosenParams,
        paramsByProtocol: Object.keys(cleanedByProto).length > 0 ? cleanedByProto : undefined,
      };
    }
    updateProfile.mutate(
      { preferences: { ...user?.preferences, workspaceSettings: settings } },
      { onSuccess: onClose },
    );
  };

  const cancelButton = (
    <button
      onClick={onClose}
      className="rounded-md border border-slate-300 px-4 py-2 text-sm dark:border-slate-600 dark:text-slate-200"
    >
      {t('app.cancel')}
    </button>
  );

  return (
    <Dialog
      title={t('portal.connectionSettings')}
      onClose={onClose}
      footer={
        topTab === 'connection' ? (
          <>
            {updateProfile.isError && (
              <p className="mr-auto text-sm text-red-600">{updateProfile.error.message}</p>
            )}
            {cancelButton}
            <button
              onClick={onSave}
              disabled={updateProfile.isPending}
              className="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
            >
              {t('app.save')}
            </button>
          </>
        ) : (
          <>
            {updateOverrides.isError && (
              <p className="mr-auto text-sm text-red-600">{updateOverrides.error.message}</p>
            )}
            {cancelButton}
            {/* Submits the runtime form living in the scrollable body. */}
            <button
              type="submit"
              form={RUNTIME_FORM_ID}
              disabled={updateOverrides.isPending}
              className="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
            >
              {t('app.apply')}
            </button>
          </>
        )
      }
    >
      {/* Top-level sections (same look as the protocol tabs below, but
          these are labelled sections, not protocol names). */}
      <div className="flex items-center gap-1 border-b border-slate-200 dark:border-slate-700">
        {(
          [
            ['connection', t('portal.settingsTabConnection')],
            ['workspace', t('portal.settingsTabWorkspace')],
          ] as const
        ).map(([section, label]) => (
          <button
            key={section}
            type="button"
            onClick={() => setTopTab(section)}
            className={`-mb-px rounded-t-md border-x border-t px-3 py-1.5 text-sm font-medium ${
              section === topTab
                ? 'border-slate-200 bg-white text-blue-600 dark:border-slate-700 dark:bg-slate-800 dark:text-blue-400'
                : 'border-transparent text-slate-500 hover:text-slate-700 dark:text-slate-400 dark:hover:text-slate-200'
            }`}
          >
            {label}
          </button>
        ))}
      </div>
      {topTab === 'workspace' ? (
        <WorkspaceRuntimeForm
          workspace={workspace}
          formId={RUNTIME_FORM_ID}
          onApply={(input) => {
            if (!input) {
              onClose();
              return;
            }
            updateOverrides.mutate({ id: workspace.id, input }, { onSuccess: onClose });
          }}
        />
      ) : names.length > 0 ? (
        <>
          <ProtocolTabs
            protocols={names}
            active={tab}
            onSelect={setTab}
            badge={(p) => (p === chosen ? <span className="text-[10px]">●</span> : null)}
          />
          {selected && (
            <div className="space-y-3">
              <label className="flex items-center gap-2 text-sm text-slate-600 dark:text-slate-300">
                <input
                  type="radio"
                  name="chosen-protocol"
                  checked={chosen === tab}
                  onChange={() => setChosen(tab)}
                />
                {t('portal.useThisProtocol')}
                {tab === defaultProtocol && (
                  <span className="text-xs text-slate-400">({t('portal.protocolDefault')})</span>
                )}
              </label>
              <ProtocolParamsForm
                key={tab}
                meta={meta.data?.data}
                protocol={tab}
                values={paramsByProto[tab] ?? {}}
                onChange={(name, value) =>
                  setParamsByProto((prev) => ({
                    ...prev,
                    [tab]: { ...prev[tab], [name]: value },
                  }))
                }
                allowList={isAdmin ? undefined : (selected.resolvedUserParams ?? [])}
                placeholders={selected.params}
                columns={1}
                audioPortExposed={selected.exposeAudioPort ?? false}
                kasmvncConfig={
                  tab === 'kasmvnc'
                    ? { content: kasmCfg.data?.data.config ?? '', variant: 'effective' }
                    : undefined
                }
              />
            </div>
          )}
        </>
      ) : (
        <p className="text-sm text-slate-500 dark:text-slate-400">
          {t('portal.protocol')}: {(workspace.protocol || 'vnc').toUpperCase()}
        </p>
      )}
    </Dialog>
  );
}
