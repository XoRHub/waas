import { useCallback, useEffect, useRef, useState } from 'react';
import { useNavigate, useParams } from 'react-router';
import { useTranslation } from 'react-i18next';
import {
  DesktopPane,
  type ConnectionState,
  type DesktopPaneHandle,
} from '@/components/DesktopPane';
import { SessionOverlay } from '@/components/SessionOverlay';
import {
  useRemoteWorkspaces,
  useWakeRemoteWorkspace,
  useWorkspaceAction,
  useWorkspaces,
} from '@/hooks/useApi';
import type { RemoteWorkspace, SessionCapabilities, Workspace } from '@/types';

// How long we wait for a woken workspace to become Running before giving
// up with a clear error (no infinite spinner).
const WAKE_TIMEOUT_MS = 180_000;
// After a Wake-on-LAN, how long to let a remote machine boot before the
// automatic connection retry.
const WOL_BOOT_WAIT_MS = 20_000;

// Full-screen single-desktop view. The split view (/view) reuses the same
// DesktopPane with several workspaces side by side. kind="remote" drives
// a registered out-of-cluster machine through the same pane.
export function ConnectPage({ kind = 'workspace' }: { kind?: 'workspace' | 'remote' }) {
  const { id } = useParams<{ id: string }>();
  if (!id) return null;
  if (kind === 'remote') {
    return <RemoteConnect id={id} />;
  }
  return <WorkspaceConnect id={id} />;
}

// RemoteConnect drives an external machine. If the connection fails and
// the machine has a MAC address, it tries Wake-on-LAN once, waits for the
// machine to boot, then retries the connection.
function RemoteConnect({ id }: { id: string }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const remotes = useRemoteWorkspaces(true);
  const wake = useWakeRemoteWorkspace();
  const [attempt, setAttempt] = useState(0);
  const [waking, setWaking] = useState(false);
  const autoWokeRef = useRef(false);

  const remote = remotes.data?.data.find((r) => r.id === id);

  const onFailed = useCallback(() => {
    // Auto Wake-on-LAN once on first failure, if the machine has a MAC.
    if (!remote?.macAddress || autoWokeRef.current) return;
    autoWokeRef.current = true;
    setWaking(true);
    wake.mutate(id, {
      onSettled: () => {
        setTimeout(() => {
          setWaking(false);
          setAttempt((a) => a + 1);
        }, WOL_BOOT_WAIT_MS);
      },
    });
  }, [remote, id, wake]);

  if (remotes.isPending) return <WaitScreen title={t('connect.connecting')} />;
  if (!remote) {
    return (
      <ErrorScreen
        message={t('connect.notFound')}
        actionLabel={t('connect.back')}
        onAction={() => navigate('/')}
      />
    );
  }
  if (waking) {
    return <WaitScreen title={t('remote.waking')} subtitle={t('remote.wakeWaitHint')} />;
  }
  return (
    <DesktopView
      key={attempt}
      id={id}
      kind="remote"
      workspace={undefined}
      remote={remote}
      onFailed={onFailed}
    />
  );
}

// WorkspaceConnect wakes a stopped/paused workspace on open, shows a
// waiting screen until it is Running, then connects the session.
function WorkspaceConnect({ id }: { id: string }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const workspaces = useWorkspaces();
  const resume = useWorkspaceAction();
  const wokeRef = useRef(false);
  const [startedAt] = useState(() => Date.now());
  const [timedOut, setTimedOut] = useState(false);

  const workspace = workspaces.data?.data.find((ws) => ws.id === id);
  const phase = workspace?.phase;
  const isRunning = phase === 'Running';
  const isDown = phase === 'Paused' || phase === 'Stopped';
  const isFailed = phase === 'Failed' || phase === 'Terminating';

  // Auto-resume a down workspace once, when the page is opened on it.
  useEffect(() => {
    if (isDown && !wokeRef.current && !resume.isPending) {
      wokeRef.current = true;
      resume.mutate({ id, action: 'resume' });
    }
  }, [isDown, id, resume]);

  // While waking/starting, poll the workspace fast and enforce the timeout.
  useEffect(() => {
    if (isRunning || isFailed || timedOut) return;
    if (!workspace) return;
    const timer = setInterval(() => {
      if (Date.now() - startedAt > WAKE_TIMEOUT_MS) {
        setTimedOut(true);
      } else {
        void workspaces.refetch();
      }
    }, 2500);
    return () => clearInterval(timer);
  }, [isRunning, isFailed, timedOut, workspace, workspaces, startedAt]);

  const retry = () => {
    setTimedOut(false);
    wokeRef.current = false;
    void workspaces.refetch();
  };

  // Loading the list, or the workspace vanished.
  if (workspaces.isPending) {
    return <WaitScreen title={t('connect.connecting')} />;
  }
  if (!workspace) {
    return (
      <ErrorScreen
        message={t('connect.notFound')}
        actionLabel={t('connect.back')}
        onAction={() => navigate('/')}
      />
    );
  }

  if (isRunning) {
    return <DesktopView id={id} kind="workspace" workspace={workspace} />;
  }

  if (timedOut) {
    return (
      <ErrorScreen
        message={t('connect.wakeTimeout', { name: workspace.displayName || workspace.name })}
        actionLabel={t('app.retry')}
        onAction={retry}
        secondaryLabel={t('connect.back')}
        onSecondary={() => navigate('/')}
      />
    );
  }
  if (isFailed) {
    return (
      <ErrorScreen
        message={workspace.message || t('connect.failed')}
        actionLabel={t('connect.back')}
        onAction={() => navigate('/')}
      />
    );
  }

  // Down (being woken) or starting: waiting screen.
  const title = isDown || resume.isPending ? t('connect.waking') : t('connect.starting');
  return <WaitScreen title={title} subtitle={t('connect.wakeHint')} />;
}

// DesktopView is the actual desktop stream + overlay + leave bar.
function DesktopView({
  id,
  kind,
  workspace,
  remote,
  onFailed,
}: {
  id: string;
  kind: 'workspace' | 'remote';
  workspace: Workspace | undefined;
  remote?: RemoteWorkspace;
  onFailed?: () => void;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const pane = useRef<DesktopPaneHandle>(null);
  const [state, setState] = useState<ConnectionState>('connecting');
  const [capabilities, setCapabilities] = useState<SessionCapabilities | null>(null);
  const onStateChange = useCallback((s: ConnectionState) => setState(s), []);
  const onCapabilities = useCallback((caps: SessionCapabilities) => setCapabilities(caps), []);

  // Bubble a connection failure up (drives the remote Wake-on-LAN retry).
  useEffect(() => {
    if (state === 'failed') onFailed?.();
  }, [state, onFailed]);

  const leave = () => {
    if (window.opener) {
      window.close();
    }
    navigate('/');
  };

  return (
    <div className="relative h-screen bg-black">
      {state === 'connected' && (
        <div className="group absolute inset-x-0 top-0 z-10 flex justify-center">
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
        // Grabbing focus IS the feature: a remote desktop that does not
        // receive keystrokes on open is broken, not accessible.
        // eslint-disable-next-line jsx-a11y/no-autofocus
        autoFocus
      />
      {state === 'connected' && (
        <SessionOverlay
          workspace={workspace}
          remote={remote}
          capabilities={capabilities}
          pane={pane}
        />
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

function WaitScreen({ title, subtitle }: { title: string; subtitle?: string }) {
  return (
    <div className="flex h-screen flex-col items-center justify-center gap-4 bg-black text-white">
      <div className="h-10 w-10 animate-spin rounded-full border-2 border-white/20 border-t-white" />
      <p className="text-sm">{title}</p>
      {subtitle && <p className="max-w-sm text-center text-xs text-white/50">{subtitle}</p>}
    </div>
  );
}

function ErrorScreen({
  message,
  actionLabel,
  onAction,
  secondaryLabel,
  onSecondary,
}: {
  message: string;
  actionLabel: string;
  onAction: () => void;
  secondaryLabel?: string;
  onSecondary?: () => void;
}) {
  return (
    <div className="flex h-screen flex-col items-center justify-center gap-4 bg-black text-white">
      <p className="max-w-md text-center text-sm text-white/80">{message}</p>
      <div className="flex gap-2">
        {secondaryLabel && onSecondary && (
          <button
            onClick={onSecondary}
            className="rounded-md border border-white/30 px-4 py-2 text-sm text-white/80 hover:bg-white/10"
          >
            {secondaryLabel}
          </button>
        )}
        <button
          onClick={onAction}
          className="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700"
        >
          {actionLabel}
        </button>
      </div>
    </div>
  );
}
