import { forwardRef, useEffect, useImperativeHandle, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import Guacamole from 'guacamole-common-js';
import { api } from '@/lib/api';
import { detectServerLayout } from '@/lib/keyboard';
import { useAuthStore } from '@/stores/authStore';
import type { ConnectResult, SessionCapabilities, WorkspaceConnectionPrefs } from '@/types';

export type ConnectionState = 'connecting' | 'connected' | 'disconnected' | 'failed';

// Guacamole.Client state 3 = CONNECTED, 5 = DISCONNECTED.
const STATE_CONNECTED = 3;
const STATE_DISCONNECTED = 5;

export interface DesktopPaneHandle {
  disconnect: () => void;
  /** Tear down and re-open the session (applies reconnect-scoped params). */
  reconnect: () => void;
  /** Live clipboard toggle, enforced by wwt (clamped to the policy grant). */
  setClipboard: (direction: 'copy' | 'paste', enabled: boolean) => void;
}

/**
 * One embedded remote desktop. Keyboard input is scoped to the pane (the
 * container grabs focus on click), so several panes can coexist in a split
 * view without fighting over keystrokes. The desktop image is scaled to
 * fit the pane and re-scaled when the pane is resized.
 */
export const DesktopPane = forwardRef<
  DesktopPaneHandle,
  {
    workspaceId: string;
    /** 'remote' targets a registered out-of-cluster machine. */
    kind?: 'workspace' | 'remote';
    /** Saved protocol/params override; defaults to the profile preference. */
    connection?: WorkspaceConnectionPrefs;
    onStateChange?: (state: ConnectionState) => void;
    /** Reports what the session token actually allows (overlay toggles). */
    onCapabilities?: (caps: SessionCapabilities) => void;
    autoFocus?: boolean;
  }
>(function DesktopPane(
  { workspaceId, kind = 'workspace', connection, onStateChange, onCapabilities, autoFocus },
  ref,
) {
  const { t } = useTranslation();
  const containerRef = useRef<HTMLDivElement>(null);
  const displayRef = useRef<HTMLDivElement>(null);
  const tunnelRef = useRef<InstanceType<typeof Guacamole.WebSocketTunnel> | null>(null);
  const clientRef = useRef<InstanceType<typeof Guacamole.Client> | null>(null);
  const [state, setState] = useState<ConnectionState>('connecting');
  // Bumping the generation re-runs the connection effect: that IS the
  // reconnect (used by the overlay to apply reconnect-scoped params).
  const [generation, setGeneration] = useState(0);
  const prefs = useAuthStore((s) => s.user?.preferences?.workspaceSettings?.[workspaceId]);
  // Remote machines carry their protocol/params server-side; workspace
  // preferences do not apply to them.
  const effective = connection ?? (kind === 'workspace' ? prefs : undefined);
  // The connection must not restart when unrelated preferences change.
  const effectiveJSON = JSON.stringify(effective ?? {});

  useImperativeHandle(ref, () => ({
    disconnect: () => clientRef.current?.disconnect(),
    reconnect: () => setGeneration((g) => g + 1),
    setClipboard: (direction, enabled) => {
      tunnelRef.current?.sendMessage('', 'waas-clipboard', direction, enabled ? '1' : '0');
    },
  }));

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
        const connectPath =
          kind === 'remote'
            ? `/api/v1/remote-workspaces/${workspaceId}/connect`
            : `/api/v1/workspaces/${workspaceId}/connect`;
        const response = await api.post<ConnectResult>(
          connectPath,
          Object.keys(body).length > 0 ? body : undefined,
        );
        result = response.data;
      } catch {
        if (!cancelled) setBoth('failed');
        return;
      }
      if (cancelled) return;
      if (result.capabilities) onCapabilities?.(result.capabilities);

      const params = new URLSearchParams({
        token: result.connectionToken,
        width: String(container.clientWidth || window.innerWidth),
        height: String(container.clientHeight || window.innerHeight),
        dpi: '96',
        // Auto keyboard layout (client display characteristic): wwt uses
        // it as the RDP server-layout default unless the template/user
        // set one explicitly.
        layout: detectServerLayout(),
      });
      const tunnel = new Guacamole.WebSocketTunnel(`/ws?${params.toString()}`);
      client = new Guacamole.Client(tunnel);
      tunnelRef.current = tunnel;
      clientRef.current = client;

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
      // The display is scaled to fit the pane: pointer coordinates are in
      // scaled pixels and must be mapped back to desktop pixels.
      const sendMouse = (mouseState: InstanceType<typeof Guacamole.Mouse.State>) => {
        if (!client) return;
        const scale = client.getDisplay().getScale() || 1;
        client.sendMouseState(
          new Guacamole.Mouse.State(
            mouseState.x / scale,
            mouseState.y / scale,
            mouseState.left,
            mouseState.middle,
            mouseState.right,
            mouseState.up,
            mouseState.down,
          ),
        );
      };
      mouse.onmousedown = (mouseState) => {
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
      tunnelRef.current = null;
      clientRef.current = null;
      displayHost.replaceChildren();
    };
  }, [workspaceId, kind, effectiveJSON, onStateChange, onCapabilities, autoFocus, generation]);

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
});
