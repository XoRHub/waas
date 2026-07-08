import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Dialog } from '@/components/Dialog';
import { ProtocolParamsForm, ProtocolTabs } from '@/components/ProtocolTabs';
import { useProtocolMeta, useUpdateProfile } from '@/hooks/useApi';
import { useAuthStore } from '@/stores/authStore';
import type { Workspace } from '@/types';

// ConnectionSettingsDialog: one tab per configured protocol (VNC/RDP/SSH)
// instead of a single endless form; each tab tunes that protocol's guacd
// parameters and one protocol is marked as the connection choice. Saved
// in the profile; the server re-validates at connect time.
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
  const meta = useProtocolMeta();
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

  return (
    <Dialog
      title={t('portal.connectionSettings')}
      onClose={onClose}
      footer={
        <>
          {updateProfile.isError && (
            <p className="mr-auto text-sm text-red-600">{updateProfile.error.message}</p>
          )}
          <button
            onClick={onClose}
            className="rounded-md border border-slate-300 px-4 py-2 text-sm dark:border-slate-600 dark:text-slate-200"
          >
            {t('app.cancel')}
          </button>
          <button
            onClick={onSave}
            disabled={updateProfile.isPending}
            className="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
          >
            {t('app.save')}
          </button>
        </>
      }
    >
      {names.length > 0 ? (
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
                meta={meta.data?.data}
                protocol={tab}
                values={paramsByProto[tab] ?? {}}
                onChange={(name, value) =>
                  setParamsByProto((prev) => ({
                    ...prev,
                    [tab]: { ...prev[tab], [name]: value },
                  }))
                }
                allowList={isAdmin ? undefined : (selected.userParams ?? [])}
                placeholders={selected.params}
                columns={1}
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
