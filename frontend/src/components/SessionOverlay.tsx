import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import { useProtocolMeta, useSaveRemoteWorkspace, useUpdateProfile } from '@/hooks/useApi';
import { useAuthStore } from '@/stores/authStore';
import { useEscape } from '@/hooks/useEscape';
import { useProtocolSwitch } from '@/hooks/useProtocolSwitch';
import { ParamField, paramsFor } from '@/components/ParamField';
import { targetFromRemote, targetFromWorkspace, type SessionTarget } from '@/lib/target';
import type { DesktopPaneHandle } from '@/components/DesktopPane';
import type { RemoteWorkspace, SessionCapabilities, Workspace } from '@/types';

// Placeholder keeping the hook order stable while neither prop is set.
const EMPTY_TARGET: SessionTarget = {
  id: '',
  kind: 'workspace',
  displayName: '',
  subtitle: '',
  connectUrl: '',
  protocols: [],
  defaultProtocol: '',
  capabilities: {
    pause: false,
    wake: false,
    splitView: false,
    connectionSettings: false,
    editEndpoint: false,
    hasPhase: false,
  },
};

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

  // ---- unified session descriptor: ONE shape for both kinds -----------
  // (built before the early return: hooks below must run unconditionally)
  const maybeTarget = workspace
    ? targetFromWorkspace(workspace)
    : remote
      ? targetFromRemote(remote)
      : null;
  const protoSwitch = useProtocolSwitch(maybeTarget ?? EMPTY_TARGET, { confirm: true });

  if (!maybeTarget) return null;
  const target = maybeTarget;
  const isRemote = !!remote;
  const saved = user?.preferences?.workspaceSettings?.[target.id];
  const entry = target.protocols.find((p) => p.name === protoSwitch.active);
  const protocol = protoSwitch.active;
  const isAdmin = user?.role === 'admin';
  // Remote machines belong to the user: every non-platform parameter is
  // tunable. Workspaces follow the template allow-list (admins bypass).
  const allowList = isRemote || isAdmin ? undefined : (entry?.userParams ?? []);
  // In-cluster tuning lives in the profile; remote tuning lives on the
  // chosen endpoint server-side.
  const savedParams = isRemote ? (entry?.params ?? {}) : (saved?.params ?? {});
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
      // credentials keeps the stored Secret untouched. The tweaks land on
      // the endpoint being used — other protocols keep their own params.
      saveRemote.mutate(
        {
          id: remote!.id,
          input: {
            name: remote!.name,
            hostname: remote!.hostname,
            protocols: target.protocols.map((p) => ({
              name: p.name,
              port: p.port ?? 0,
              default: p.default,
              params:
                p.name === protocol
                  ? Object.keys(cleaned).length > 0
                    ? cleaned
                    : undefined
                  : p.params,
            })),
            macAddress: remote!.macAddress,
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

  const pending = updateProfile.isPending || saveRemote.isPending || protoSwitch.pending;

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
              onClick={() => target.capabilities.splitView && navigate(`/view?ws=${target.id}`)}
              disabled={!target.capabilities.splitView}
              title={!target.capabilities.splitView ? t('overlay.notAvailableRemote') : undefined}
              className="w-full rounded-md bg-slate-700/70 px-3 py-1.5 text-left hover:bg-slate-600 disabled:cursor-not-allowed disabled:opacity-40"
            >
              {t('overlay.openSplitView')}
              {!target.capabilities.splitView && <span className="ml-1 text-xs">🔒</span>}
            </button>
          </section>

          {/* -------- protocol (quick switch, any kind) -------- */}
          {target.protocols.length > 1 && (
            <section className="space-y-2">
              <h4 className="text-xs uppercase tracking-wide text-slate-400">
                {t('overlay.protocol')}
              </h4>
              <div className="flex gap-1">
                {target.protocols.map((p) => (
                  <button
                    key={p.name}
                    onClick={() => protoSwitch.switchTo(p.name)}
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
