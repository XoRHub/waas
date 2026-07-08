import { useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { Dialog } from '@/components/Dialog';
import { ProtocolParamsForm, ProtocolTabs } from '@/components/ProtocolTabs';
import { useProtocolMeta, useSaveRemoteWorkspace } from '@/hooks/useApi';
import { targetFromRemote } from '@/lib/target';
import type { RemoteProtocol, RemoteWorkspace, RemoteWorkspaceInput } from '@/types';

// RemoteWorkspaceDialog: register/edit one external machine. Credentials
// are write-only (stored in a Kubernetes Secret server-side, never echoed
// back); the per-protocol endpoints use the SAME tabs + registry-driven
// form as every other protocol surface.
const REMOTE_DEFAULT_PORTS: Record<string, number> = { ssh: 22, vnc: 5900, rdp: 3389 };

export function RemoteWorkspaceDialog({
  remote,
  onClose,
}: {
  remote: RemoteWorkspace | null;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const meta = useProtocolMeta();
  const save = useSaveRemoteWorkspace();
  const [name, setName] = useState(remote?.name ?? '');
  const [hostname, setHostname] = useState(remote?.hostname ?? '');
  const [macAddress, setMacAddress] = useState(remote?.macAddress ?? '');
  // One endpoint per protocol, same rules as the admin template editor:
  // unique names, one default. Legacy rows are synthesized by the target
  // adapter (single entry).
  const [protocols, setProtocols] = useState<RemoteProtocol[]>(() =>
    remote
      ? targetFromRemote(remote).protocols.map((p) => ({
          name: p.name,
          port: p.port ?? REMOTE_DEFAULT_PORTS[p.name] ?? 0,
          default: p.default,
          params: p.params,
        }))
      : [{ name: 'ssh', port: 22, default: true }],
  );
  const [tab, setTab] = useState(() => protocols[0]?.name ?? 'ssh');
  const [creds, setCreds] = useState({
    username: '',
    password: '',
    privateKey: '',
    passphrase: '',
  });

  const names = protocols.map((p) => p.name);
  const current = protocols.find((p) => p.name === tab);
  const unused = ['ssh', 'vnc', 'rdp'].filter((p) => !names.includes(p));
  const patchCurrent = (patch: Partial<RemoteProtocol>) =>
    setProtocols((prev) => prev.map((p) => (p.name === tab ? { ...p, ...patch } : p)));
  const addProtocol = (name: string) => {
    setProtocols((prev) => [
      ...prev,
      { name, port: REMOTE_DEFAULT_PORTS[name] ?? 0, default: prev.length === 0 },
    ]);
    setTab(name);
  };
  const removeProtocol = (name: string) => {
    setProtocols((prev) => {
      const next = prev.filter((p) => p.name !== name);
      if (next.length > 0 && !next.some((p) => p.default)) next[0] = { ...next[0], default: true };
      setTab(next[0]?.name ?? '');
      return next;
    });
  };

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    const cleanedProtocols = protocols.map((p) => {
      const cleaned = Object.fromEntries(
        Object.entries(p.params ?? {}).filter(([, v]) => v !== ''),
      );
      return { ...p, params: Object.keys(cleaned).length > 0 ? cleaned : undefined };
    });
    // Empty fields are omitted = "keep the stored value" on edit.
    const credentials = Object.fromEntries(
      Object.entries(creds).filter(([, v]) => v !== ''),
    ) as RemoteWorkspaceInput['credentials'];
    const input: RemoteWorkspaceInput = {
      name,
      hostname,
      protocols: cleanedProtocols,
      macAddress: macAddress.trim() || undefined,
      credentials: credentials && Object.keys(credentials).length > 0 ? credentials : undefined,
    };
    save.mutate({ id: remote?.id, input }, { onSuccess: onClose });
  };

  const credField = (key: keyof typeof creds, label: string, type = 'text') => (
    <label className="block">
      <span className="text-xs text-slate-500 dark:text-slate-400">{label}</span>
      <input
        type={type}
        autoComplete="off"
        className="mt-0.5 w-full rounded-md border border-slate-300 px-3 py-1.5 text-sm dark:border-slate-600 dark:bg-slate-700 dark:text-white"
        placeholder={remote ? t('remote.keepStored') : ''}
        value={creds[key]}
        onChange={(e) => setCreds((c) => ({ ...c, [key]: e.target.value }))}
      />
    </label>
  );

  return (
    <Dialog
      title={remote ? t('remote.edit') : t('remote.add')}
      onClose={onClose}
      onSubmit={onSubmit}
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
            {remote ? t('app.save') : t('app.create')}
          </button>
        </>
      }
    >
      <label className="block">
        <span className="text-sm text-slate-600 dark:text-slate-300">{t('remote.name')}</span>
        <input
          required
          className="mt-1 w-full rounded-md border border-slate-300 px-3 py-2 dark:border-slate-600 dark:bg-slate-700 dark:text-white"
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
      </label>
      <label className="block">
        <span className="text-sm text-slate-600 dark:text-slate-300">{t('remote.hostname')}</span>
        <input
          required
          className="mt-1 w-full rounded-md border border-slate-300 px-3 py-2 font-mono text-sm dark:border-slate-600 dark:bg-slate-700 dark:text-white"
          placeholder="203.0.113.10"
          value={hostname}
          onChange={(e) => setHostname(e.target.value)}
        />
      </label>
      <label className="block">
        <span className="text-sm text-slate-600 dark:text-slate-300">{t('remote.mac')}</span>
        <input
          className="mt-1 w-full rounded-md border border-slate-300 px-3 py-2 font-mono text-sm dark:border-slate-600 dark:bg-slate-700 dark:text-white"
          placeholder="aa:bb:cc:dd:ee:ff"
          value={macAddress}
          onChange={(e) => setMacAddress(e.target.value)}
        />
        <span className="mt-0.5 block text-xs text-slate-400 dark:text-slate-500">
          {t('remote.macHint')}
        </span>
      </label>

      {/* One tab per endpoint the machine serves — the same tabs and
            registry-driven form as connection settings and the admin
            template editor. The owner may tune any non-platform param. */}
      <fieldset className="space-y-3 rounded-lg border border-slate-200 p-3 dark:border-slate-700">
        <legend className="px-1 text-sm text-slate-600 dark:text-slate-300">
          {t('portal.connection')}
        </legend>
        <ProtocolTabs
          protocols={names}
          active={tab}
          onSelect={setTab}
          badge={(p) =>
            protocols.find((x) => x.name === p)?.default ? (
              <span className="text-[10px]" title={t('portal.protocolDefault')}>
                ●
              </span>
            ) : null
          }
          addable={unused}
          onAdd={addProtocol}
          onRemove={removeProtocol}
        />
        {current && (
          <>
            <div className="flex items-end gap-3">
              <label className="block w-28">
                <span className="text-xs text-slate-500 dark:text-slate-400">
                  {t('portal.port')}
                </span>
                <input
                  type="number"
                  min={1}
                  max={65535}
                  required
                  className="mt-0.5 w-full rounded-md border border-slate-300 px-3 py-1.5 text-sm dark:border-slate-600 dark:bg-slate-700 dark:text-white"
                  value={current.port || ''}
                  onChange={(e) => patchCurrent({ port: Number(e.target.value) })}
                />
              </label>
              <label className="flex items-center gap-1.5 pb-2 text-sm text-slate-600 dark:text-slate-300">
                <input
                  type="radio"
                  name="remote-default-protocol"
                  checked={!!current.default}
                  onChange={() =>
                    setProtocols((prev) => prev.map((p) => ({ ...p, default: p.name === tab })))
                  }
                />
                {t('portal.protocolDefault')}
              </label>
            </div>
            <ProtocolParamsForm
              meta={meta.data?.data}
              protocol={current.name}
              values={current.params ?? {}}
              onChange={(name, value) => {
                const params = { ...current.params };
                if (value === '') delete params[name];
                else params[name] = value;
                patchCurrent({ params });
              }}
            />
          </>
        )}
      </fieldset>

      <fieldset className="space-y-2 rounded-lg border border-slate-200 p-3 dark:border-slate-700">
        <legend className="px-1 text-sm text-slate-600 dark:text-slate-300">
          {t('remote.credentials')}
        </legend>
        <p className="text-xs text-slate-400 dark:text-slate-500">{t('remote.credentialsHint')}</p>
        {credField('username', t('remote.username'))}
        {credField('password', t('remote.password'), 'password')}
        {names.includes('ssh') && (
          <>
            <label className="block">
              <span className="text-xs text-slate-500 dark:text-slate-400">
                {t('remote.privateKey')}
              </span>
              <textarea
                rows={3}
                autoComplete="off"
                spellCheck={false}
                className="mt-0.5 w-full rounded-md border border-slate-300 px-3 py-1.5 font-mono text-xs dark:border-slate-600 dark:bg-slate-700 dark:text-white"
                placeholder={
                  remote ? t('remote.keepStored') : '-----BEGIN OPENSSH PRIVATE KEY-----'
                }
                value={creds.privateKey}
                onChange={(e) => setCreds((c) => ({ ...c, privateKey: e.target.value }))}
              />
            </label>
            {credField('passphrase', t('remote.passphrase'), 'password')}
          </>
        )}
      </fieldset>
    </Dialog>
  );
}
