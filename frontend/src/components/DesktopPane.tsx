import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import Guacamole from 'guacamole-common-js';
import { api } from '@/lib/api';
import { useAuthStore } from '@/stores/authStore';
import type { ConnectResult, WorkspaceConnectionPrefs } from '@/types';

export type ConnectionState = 'connecting' | 'connected' | 'disconnected' | 'failed';

// Guacamole.Client state 3 = CONNECTED, 5 = DISCONNECTED.
const STATE_CONNECTED = 3;
const STATE_DISCONNECTED = 5;

export interface DesktopPaneHandle {
  disconnect: () => void;
}

/**
 * One embedded remote desktop. Keyboard input is scoped to the pane (the
 * container grabs focus on click), so several panes can coexist in a split
 * view without fighting over keystrokes. The desktop image is scaled to
 * fit the pane and re-scaled when the pane is resized.
 */
export function DesktopPane({
  workspaceId,
  connection,
  onStateChange,
  autoFocus,
}: {
  workspaceId: string;
  /** Saved protocol/params override; defaults to the profile preference. */
  connection?: WorkspaceConnectionPrefs;
  onStateChange?: (state: ConnectionState) => void;
  autoFocus?: boolean;
}) {
  const { t } = useTranslation();
  const containerRef = useRef<HTMLDivElement>(null);
  const displayRef = useRef<HTMLDivElement>(null);
  const [state, setState] = useState<ConnectionState>('connecting');
  const prefs = useAuthStore((s) => s.user?.preferences?.workspaceSettings?.[workspaceId]);
  const effective = connection ?? prefs;
  // The connection must not restart when unrelated preferences change.
  const effectiveJSON = JSON.stringify(effective ?? {});

  useEffect(() => {
    const container = containerRef.current;
    const displayHost = displayRef.current;
    if (!workspaceId || !container || !displayHost) {
      return;
    }
    const conn = JSON.parse(effectiveJSON) as WorkspaceConnectionPrefs;
    let client: InstanceType<typeof Guacamole.Client> | null = null;
    let keyboard: InstanceType<typeof Guacamole.Keyboard> | null = null;
    let observer: ResizeObserver | null = null;
    let cancelled = false;

    const setBoth = (s: ConnectionState) => {
      setState(s);
      onStateChange?.(s);
    };

    const rescale = () => {
      if (!client) return;
      const display = client.getDisplay();
      const w = display.getWidth();
      const h = display.getHeight();
      if (w > 0 && h > 0) {
        display.scale(Math.min(container.clientWidth / w, container.clientHeight / h));
      }
    };

    const connect = async () => {
      let result: ConnectResult;
      try {
        const body: Record<string, unknown> = {};
        if (conn.protocol) body.protocol = conn.protocol;
        if (conn.params && Object.keys(conn.params).length > 0) body.params = conn.params;
        const response = await api.post<ConnectResult>(
          `/api/v1/workspaces/${workspaceId}/connect`,
          Object.keys(body).length > 0 ? body : undefined,
        );
        result = response.data;
      } catch {
        if (!cancelled) setBoth('failed');
        return;
      }
      if (cancelled) return;

      const params = new URLSearchParams({
        token: result.connectionToken,
        width: String(container.clientWidth || window.innerWidth),
        height: String(container.clientHeight || window.innerHeight),
        dpi: '96',
      });
      const tunnel = new Guacamole.WebSocketTunnel(`/ws?${params.toString()}`);
      client = new Guacamole.Client(tunnel);

      client.onstatechange = (clientState) => {
        if (clientState === STATE_CONNECTED) {
          setBoth('connected');
          rescale();
        }
        if (clientState === STATE_DISCONNECTED) {
          setBoth('disconnected');
        }
      };
      client.onerror = () => setBoth('failed');

      displayHost.appendChild(client.getDisplay().getElement());
      client.connect();

      const mouse = new Guacamole.Mouse(displayHost);
      const sendMouse = (mouseState: unknown) => client?.sendMouseState(mouseState);
      mouse.onmousedown = (mouseState: unknown) => {
        container.focus();
        sendMouse(mouseState);
      };
      mouse.onmouseup = sendMouse;
      mouse.onmousemove = sendMouse;

      // Keyboard bound to the pane, not the document: only the focused
      // pane types into its desktop.
      keyboard = new Guacamole.Keyboard(container);
      keyboard.onkeydown = (keysym) => client?.sendKeyEvent(1, keysym);
      keyboard.onkeyup = (keysym) => client?.sendKeyEvent(0, keysym);

      observer = new ResizeObserver(rescale);
      observer.observe(container);
      if (autoFocus) container.focus();
    };

    void connect();

    return () => {
      cancelled = true;
      observer?.disconnect();
      if (keyboard) {
        keyboard.onkeydown = null;
        keyboard.onkeyup = null;
      }
      client?.disconnect();
      displayHost.replaceChildren();
    };
  }, [workspaceId, effectiveJSON, onStateChange, autoFocus]);

  return (
    <div
      ref={containerRef}
      tabIndex={0}
      className="relative h-full w-full overflow-hidden bg-black outline-none focus:ring-1 focus:ring-blue-500/60"
    >
      <div ref={displayRef} className={state === 'connected' ? 'h-full w-full' : 'hidden'} />
      {state !== 'connected' && (
        <div className="flex h-full flex-col items-center justify-center gap-3 text-sm text-white">
          <p>
            {state === 'connecting' && t('connect.connecting')}
            {state === 'disconnected' && t('connect.disconnected')}
            {state === 'failed' && t('connect.failed')}
          </p>
        </div>
      )}
    </div>
  );
}
