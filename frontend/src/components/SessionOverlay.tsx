import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import { useProtocolMeta, useSaveRemoteWorkspace, useUpdateProfile } from '@/hooks/useApi';
import { useAuthStore } from '@/stores/authStore';
import { useEscape } from '@/hooks/useEscape';
import { ParamField, paramsFor } from '@/components/ParamField';
import type { DesktopPaneHandle } from '@/components/DesktopPane';
import type { RemoteWorkspace, SessionCapabilities, Workspace } from '@/types';

/**
 * In-session overlay: the SINGLE session menu for every desktop kind —
 * in-cluster workspaces and remote machines share this exact component,
 * parameterized by the session's capabilities. Options that do not apply
 * to a kind are disabled with the reason, never silently different.
 * Toggle with the floating button or Ctrl+Alt+M; Escape closes it.
 *
 * Every control only REFLECTS rights: clipboard toggles are clamped by
 * wwt to the policy grant, params are re-validated server-side at
 * connect time.
 */
export function SessionOverlay({
  workspace,
  remote,
  capabilities,
  pane,
}: {
  workspace?: Workspace;
  remote?: RemoteWorkspace;
  capabilities: SessionCapabilities | null;
  pane: React.RefObject<DesktopPaneHandle | null>;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const meta = useProtocolMeta();
  const user = useAuthStore((s) => s.user);
  const updateProfile = useUpdateProfile();
  const saveRemote = useSaveRemoteWorkspace();
  const [open, setOpen] = useState(false);
  // Live clipboard state starts at the grant (wwt starts there too).
  const [copyOn, setCopyOn] = useState<boolean | null>(null);
  const [pasteOn, setPasteOn] = useState<boolean | null>(null);
  const [paramDraft, setParamDraft] = useState<Record<string, string>>({});

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.ctrlKey && e.altKey && (e.key === 'm' || e.key === 'M')) {
        e.preventDefault();
        setOpen((o) => !o);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);
  useEscape(open, () => setOpen(false));

  if (!workspace && !remote) return null;
  const isRemote = !!remote;

  // ---- unified session descriptor -------------------------------------
  const saved = workspace ? user?.preferences?.workspaceSettings?.[workspace.id] : undefined;
  const protocols = workspace?.protocols ?? [];
  const defaultProtocol = protocols.find((p) => p.default)?.name ?? workspace?.protocol ?? '';
  const protocol = isRemote
    ? remote.protocol
    : (saved?.protocol ?? defaultProtocol);
  const selected = protocols.find((p) => p.name === protocol);
  const isAdmin = user?.role === 'admin';
  // Remote machines belong to the user: every non-platform parameter is
  // tunable. Workspaces follow the template allow-list (admins bypass).
  const allowList = isRemote || isAdmin ? undefined : (selected?.userParams ?? []);
  const savedParams = isRemote ? (remote.params ?? {}) : (saved?.params ?? {});
  // Reconnect-scoped tunables (live ones — the clipboard — have their own
  // switches above).
  const reconnectParams = paramsFor(meta.data?.data, protocol, ['ui', 'advanced'], allowList).filter(
    (p) => !p.live,
  );

  const effCopy = copyOn ?? capabilities?.clipboardCopy ?? false;
  const effPaste = pasteOn ?? capabilities?.clipboardPaste ?? false;

  const toggleClipboard = (direction: 'copy' | 'paste', enabled: boolean) => {
    pane.current?.setClipboard(direction, enabled);
    if (direction === 'copy') setCopyOn(enabled);
    else setPasteOn(enabled);
  };

  const applyAndReconnect = () => {
    const merged = { ...savedParams, ...paramDraft };
    const cleaned = Object.fromEntries(Object.entries(merged).filter(([, v]) => v !== ''));
    if (isRemote) {
      // Remote params live server-side on the entry itself; omitting the
      // credentials keeps the stored Secret untouched.
      saveRemote.mutate(
        {
          id: remote.id,
          input: {
            name: remote.name,
            hostname: remote.hostname,
            port: remote.port,
            protocol: remote.protocol,
            macAddress: remote.macAddress,
            params: Object.keys(cleaned).length > 0 ? cleaned : undefined,
          },
        },
        {
          onSuccess: () => {
            setParamDraft({});
            pane.current?.reconnect();
          },
        },
      );
      return;
    }
    const settings = { ...user?.preferences?.workspaceSettings };
    settings[workspace!.id] = {
      ...saved,
      params: cleaned,
      paramsByProtocol: { ...saved?.paramsByProtocol, [protocol]: cleaned },
    };
    updateProfile.mutate(
      { preferences: { ...user?.preferences, workspaceSettings: settings } },
      {
        onSuccess: () => {
          setParamDraft({});
          pane.current?.reconnect();
        },
      },
    );
  };

  // Protocol quick-switch (workspaces with several protocols): saving the
  // preference re-runs the pane's connection effect — that IS the
  // reconnect, hence the explicit confirmation while a session is open.
  const switchProtocol = (next: string) => {
    if (!workspace || next === protocol) return;
    if (!window.confirm(t('overlay.switchConfirm', { protocol: next.toUpperCase() }))) return;
    const settings = { ...user?.preferences?.workspaceSettings };
    const byProto = { ...saved?.paramsByProtocol };
    if (saved?.params) byProto[protocol] = saved.params;
    settings[workspace.id] = {
      protocol: next !== defaultProtocol ? next : undefined,
      params: byProto[next],
      paramsByProtocol: byProto,
    };
    updateProfile.mutate({ preferences: { ...user?.preferences, workspaceSettings: settings } });
  };

  const pending = updateProfile.isPending || saveRemote.isPending;

  return (
    <>
      {/* Floating toggle: discreet until hovered. */}
      <button
        onClick={() => setOpen((o) => !o)}
        title={`${t('overlay.title')} (Ctrl+Alt+M)`}
        className="absolute bottom-4 right-4 z-20 flex h-9 w-9 items-center justify-center rounded-full bg-slate-900/70 text-white opacity-40 shadow-lg backdrop-blur transition hover:opacity-100"
      >
        ⚙
      </button>

      {open && (
        <div className="absolute bottom-16 right-4 z-20 max-h-[80vh] w-80 space-y-4 overflow-y-auto rounded-xl bg-slate-900/95 p-4 text-sm text-slate-100 shadow-2xl backdrop-blur">
          <div className="flex items-center justify-between">
            <h3 className="font-semibold">{t('overlay.title')}</h3>
            <span className="text-xs text-slate-400">Ctrl+Alt+M · Esc</span>
          </div>

          {/* -------- view -------- */}
          <section className="space-y-2">
            <h4 className="text-xs uppercase tracking-wide text-slate-400">
              {t('overlay.view')}
            </h4>
            <button
              onClick={() => workspace && navigate(`/view?ws=${workspace.id}`)}
              disabled={isRemote}
              title={isRemote ? t('overlay.notAvailableRemote') : undefined}
              className="w-full rounded-md bg-slate-700/70 px-3 py-1.5 text-left hover:bg-slate-600 disabled:cursor-not-allowed disabled:opacity-40"
            >
              {t('overlay.openSplitView')}
              {isRemote && <span className="ml-1 text-xs">🔒</span>}
            </button>
          </section>

          {/* -------- protocol (quick switch) -------- */}
          {!isRemote && protocols.length > 1 && (
            <section className="space-y-2">
              <h4 className="text-xs uppercase tracking-wide text-slate-400">
                {t('overlay.protocol')}
              </h4>
              <div className="flex gap-1">
                {protocols.map((p) => (
                  <button
                    key={p.name}
                    onClick={() => switchProtocol(p.name)}
                    disabled={pending}
                    className={`flex-1 rounded-md px-2 py-1.5 text-xs font-medium uppercase ${
                      p.name === protocol
                        ? 'bg-blue-600 text-white'
                        : 'bg-slate-700/70 text-slate-300 hover:bg-slate-600'
                    }`}
                  >
                    {p.name}
                  </button>
                ))}
              </div>
              <p className="text-[11px] text-slate-500">{t('overlay.switchHint')}</p>
            </section>
          )}

          {/* -------- clipboard (live) -------- */}
          <section className="space-y-2">
            <h4 className="text-xs uppercase tracking-wide text-slate-400">
              {t('overlay.clipboard')}
            </h4>
            <OverlayToggle
              label={t('overlay.clipboardCopy')}
              live
              checked={effCopy}
              allowed={capabilities?.clipboardCopy ?? false}
              deniedReason={t('overlay.deniedByPolicy')}
              onChange={(v) => toggleClipboard('copy', v)}
            />
            <OverlayToggle
              label={t('overlay.clipboardPaste')}
              live
              checked={effPaste}
              allowed={capabilities?.clipboardPaste ?? false}
              deniedReason={t('overlay.deniedByPolicy')}
              onChange={(v) => toggleClipboard('paste', v)}
            />
          </section>

          {/* -------- protocol params (reconnect scope) -------- */}
          {reconnectParams.length > 0 && (
            <section className="space-y-2">
              <h4 className="text-xs uppercase tracking-wide text-slate-400">
                {t('overlay.protocolParams', { protocol: protocol.toUpperCase() })}
              </h4>
              {reconnectParams.map((pm) => (
                <ParamField
                  key={pm.name}
                  meta={pm}
                  value={paramDraft[pm.name] ?? savedParams[pm.name] ?? ''}
                  onChange={(value) => setParamDraft((d) => ({ ...d, [pm.name]: value }))}
                />
              ))}
              <button
                onClick={applyAndReconnect}
                disabled={Object.keys(paramDraft).length === 0 || pending}
                className="w-full rounded-md bg-blue-600 px-3 py-1.5 font-medium text-white hover:bg-blue-700 disabled:opacity-40"
              >
                {t('overlay.applyReconnect')}
              </button>
              <p className="text-[11px] text-slate-500">{t('overlay.reconnectHint')}</p>
            </section>
          )}
        </div>
      )}
    </>
  );
}

function OverlayToggle({
  label,
  live,
  checked,
  allowed,
  deniedReason,
  onChange,
}: {
  label: string;
  live?: boolean;
  checked: boolean;
  allowed: boolean;
  deniedReason: string;
  onChange: (value: boolean) => void;
}) {
  return (
    <label
      className={`flex items-center justify-between gap-2 rounded-md bg-slate-800/60 px-3 py-1.5 ${
        allowed ? '' : 'opacity-50'
      }`}
      title={allowed ? undefined : deniedReason}
    >
      <span className="flex items-center gap-1.5">
        {label}
        {live && allowed && (
          <span className="rounded bg-emerald-900/60 px-1 text-[10px] uppercase text-emerald-300">
            live
          </span>
        )}
        {!allowed && <span className="text-xs">🔒</span>}
      </span>
      <input
        type="checkbox"
        className="accent-blue-500"
        checked={checked}
        disabled={!allowed}
        onChange={(e) => onChange(e.target.checked)}
      />
    </label>
  );
}
