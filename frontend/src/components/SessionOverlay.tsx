import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import { useProtocolMeta, useUpdateProfile } from '@/hooks/useApi';
import { useAuthStore } from '@/stores/authStore';
import { ParamField, paramsFor } from '@/components/ParamField';
import type { DesktopPaneHandle } from '@/components/DesktopPane';
import type { SessionCapabilities, Workspace } from '@/types';

/**
 * In-session overlay: the single entry point for WaaS options while a
 * desktop is open. Toggle with the floating button or Ctrl+Alt+M.
 *
 * Sections are deliberately additive — new toggles (file transfer, audio,
 * quality) plug in as either a live control (enforced by wwt, applied via
 * DesktopPaneHandle) or a registry param (reconnect scope, rendered
 * automatically from the platform manifest).
 *
 * Every control only REFLECTS rights: clipboard toggles are clamped by
 * wwt to the policy grant, params are re-validated server-side at
 * connect time.
 */
export function SessionOverlay({
  workspace,
  capabilities,
  pane,
}: {
  workspace?: Workspace;
  capabilities: SessionCapabilities | null;
  pane: React.RefObject<DesktopPaneHandle | null>;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const meta = useProtocolMeta();
  const user = useAuthStore((s) => s.user);
  const updateProfile = useUpdateProfile();
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

  if (!workspace) return null;

  const saved = user?.preferences?.workspaceSettings?.[workspace.id];
  const protocols = workspace.protocols ?? [];
  const protocol = saved?.protocol ?? protocols.find((p) => p.default)?.name ?? workspace.protocol ?? '';
  const selected = protocols.find((p) => p.name === protocol);
  // Reconnect-scoped tunables the template delegates (live ones — the
  // clipboard — have their own switches above).
  const reconnectParams = paramsFor(
    meta.data?.data,
    protocol,
    ['ui', 'advanced'],
    selected?.userParams ?? [],
  ).filter((p) => !p.live);

  const effCopy = copyOn ?? capabilities?.clipboardCopy ?? false;
  const effPaste = pasteOn ?? capabilities?.clipboardPaste ?? false;

  const toggleClipboard = (direction: 'copy' | 'paste', enabled: boolean) => {
    pane.current?.setClipboard(direction, enabled);
    if (direction === 'copy') setCopyOn(enabled);
    else setPasteOn(enabled);
  };

  const applyAndReconnect = () => {
    const merged = { ...saved?.params, ...paramDraft };
    const cleaned = Object.fromEntries(Object.entries(merged).filter(([, v]) => v !== ''));
    const settings = { ...user?.preferences?.workspaceSettings };
    settings[workspace.id] = { ...saved, params: cleaned };
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
        <div className="absolute bottom-16 right-4 z-20 w-80 space-y-4 rounded-xl bg-slate-900/95 p-4 text-sm text-slate-100 shadow-2xl backdrop-blur">
          <div className="flex items-center justify-between">
            <h3 className="font-semibold">{t('overlay.title')}</h3>
            <span className="text-xs text-slate-400">Ctrl+Alt+M</span>
          </div>

          {/* -------- view -------- */}
          <section className="space-y-2">
            <h4 className="text-xs uppercase tracking-wide text-slate-400">
              {t('overlay.view')}
            </h4>
            <button
              onClick={() => navigate(`/view?ws=${workspace.id}`)}
              className="w-full rounded-md bg-slate-700/70 px-3 py-1.5 text-left hover:bg-slate-600"
            >
              {t('overlay.openSplitView')}
            </button>
          </section>

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
                  value={paramDraft[pm.name] ?? saved?.params?.[pm.name] ?? ''}
                  onChange={(value) => setParamDraft((d) => ({ ...d, [pm.name]: value }))}
                />
              ))}
              <button
                onClick={applyAndReconnect}
                disabled={Object.keys(paramDraft).length === 0 || updateProfile.isPending}
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
