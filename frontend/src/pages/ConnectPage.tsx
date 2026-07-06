import { useCallback, useRef, useState } from 'react';
import { useNavigate, useParams } from 'react-router';
import { useTranslation } from 'react-i18next';
import { DesktopPane, type ConnectionState, type DesktopPaneHandle } from '@/components/DesktopPane';
import { SessionOverlay } from '@/components/SessionOverlay';
import { useWorkspaces } from '@/hooks/useApi';
import type { SessionCapabilities } from '@/types';

// Full-screen single-desktop view. The split view (/view) reuses the same
// DesktopPane with several workspaces side by side. kind="remote" drives
// a registered out-of-cluster machine through the same pane.
export function ConnectPage({ kind = 'workspace' }: { kind?: 'workspace' | 'remote' }) {
  const { t } = useTranslation();
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const workspaces = useWorkspaces();
  const pane = useRef<DesktopPaneHandle>(null);
  const [state, setState] = useState<ConnectionState>('connecting');
  const [capabilities, setCapabilities] = useState<SessionCapabilities | null>(null);
  const onStateChange = useCallback((s: ConnectionState) => setState(s), []);
  const onCapabilities = useCallback((caps: SessionCapabilities) => setCapabilities(caps), []);

  // Back to the portal: close the tab when the portal opened us in a new
  // one (window.close is only honored for script-opened windows), else
  // navigate back in place.
  const leave = () => {
    if (window.opener) {
      window.close();
    }
    navigate('/');
  };

  if (!id) return null;
  const workspace =
    kind === 'workspace' ? workspaces.data?.data.find((ws) => ws.id === id) : undefined;

  return (
    <div className="relative h-screen bg-black">
      {state === 'connected' && (
        <div className="group absolute inset-x-0 top-0 z-10 flex justify-center">
          {/* Grab handle: keeps the bar out of the way until hovered. */}
          <div className="absolute top-0 h-2 w-40 rounded-b-md bg-white/20 transition group-hover:opacity-0" />
          <div className="-translate-y-full rounded-b-lg bg-slate-900/90 px-4 py-2 text-sm text-white shadow-lg backdrop-blur transition-transform duration-200 group-hover:translate-y-0">
            <button onClick={leave} className="font-medium text-blue-400 hover:text-blue-300">
              {t('connect.leave')}
            </button>
          </div>
        </div>
      )}
      <DesktopPane
        ref={pane}
        workspaceId={id}
        kind={kind}
        onStateChange={onStateChange}
        onCapabilities={onCapabilities}
        autoFocus
      />
      {state === 'connected' && (
        <SessionOverlay workspace={workspace} capabilities={capabilities} pane={pane} />
      )}
      {(state === 'disconnected' || state === 'failed') && (
        <div className="absolute inset-x-0 bottom-10 flex justify-center">
          <button
            onClick={() => navigate('/')}
            className="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700"
          >
            {t('connect.back')}
          </button>
        </div>
      )}
    </div>
  );
}
