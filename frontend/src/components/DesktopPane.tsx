import { forwardRef, useEffect, useImperativeHandle, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import Guacamole from 'guacamole-common-js';
import { useClipboardBridge } from '@/hooks/useClipboardBridge';
import { useSessionResize } from '@/hooks/useSessionResize';
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
  /** Push text to the desktop clipboard (overlay manual fallback). */
  sendClipboard: (text: string) => void;
  /** Last text the desktop clipboard sent us (overlay manual fallback). */
  readRemoteClipboard: () => string;
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
  // Clipboard echo-guard state lives in the hook and survives reconnects;
  // the effect binds it to the live client via attach().
  const clipboard = useClipboardBridge();
  const sessionResize = useSessionResize();
  const [state, setState] = useState<ConnectionState>('connecting');
  // kasmvnc sessions embed KasmVNC's own web client instead of the guac
  // canvas: wwt reverse-proxies the whole app under /kasm/{session}.
  const [kasmUrl, setKasmUrl] = useState<string | null>(null);
  const kasmActiveRef = useRef(false);
  // Bumping the generation re-runs the connection effect: that IS the
  // reconnect (used by the overlay to apply reconnect-scoped params).
  const [generation, setGeneration] = useState(0);
  const prefs = useAuthStore((s) => s.user?.preferences?.workspaceSettings?.[workspaceId]);
  // The protocol CHOICE is a preference for both kinds (that's what the
  // quick-switch writes). Params only travel for in-cluster workspaces —
  // remote params live server-side on the chosen endpoint.
  const effective =
    connection ??
    (kind === 'workspace' ? prefs : prefs?.protocol ? { protocol: prefs.protocol } : undefined);
  // The connection must not restart when unrelated preferences change.
  const effectiveJSON = JSON.stringify(effective ?? {});

  useImperativeHandle(ref, () => ({
    disconnect: () => {
      if (kasmActiveRef.current) {
        // No client object to ask: unmounting the iframe closes its
        // WebSocket, which is what ends the session server-side.
        kasmActiveRef.current = false;
        setKasmUrl(null);
        setState('disconnected');
        onStateChange?.('disconnected');
        return;
      }
      clientRef.current?.disconnect();
    },
    reconnect: () => setGeneration((g) => g + 1),
    setClipboard: (direction, enabled) => {
      tunnelRef.current?.sendMessage('', 'waas-clipboard', direction, enabled ? '1' : '0');
    },
    sendClipboard: (text) => clipboard.sendClipboard(text, true),
    readRemoteClipboard: () => clipboard.readRemoteClipboard(),
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

      if (result.protocol === 'kasmvnc') {
        // KasmVNC ships its own web client; wwt reverse-proxies the app
        // (assets + WebSocket) under /kasm/{session}. The client builds
        // its WebSocket URL from the origin ROOT, so the proxied path is
        // handed over as a query setting. The token authenticates the
        // first request; wwt answers with a session-scoped cookie that
        // covers the page's asset and WebSocket requests.
        const q = new URLSearchParams({
          autoconnect: '1',
          resize: 'remote',
          // Embedded (iframe) KasmVNC defaults clipboard_up/down/seamless
          // to OFF when show_control_bar is absent, killing copy-paste in
          // both directions. Enable them client-side; the kasmvnc.yaml DLP
          // keys stamped from WorkspacePolicy remain the enforcement that
          // actually blocks a denied direction in the container.
          clipboard_up: '1',
          clipboard_down: '1',
          clipboard_seamless: '1',
          path: `kasm/${result.sessionId}/websockify`,
          token: result.connectionToken,
        });
        kasmActiveRef.current = true;
        setKasmUrl(`/kasm/${result.sessionId}/vnc.html?${q.toString()}`);
        return;
      }

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
          // Prime the desktop with the current local clipboard, so the
          // first in-session Ctrl+V pastes what the user last copied.
          clipboard.syncFromSystem();
        }
        if (clientState === STATE_DISCONNECTED) {
          setBoth('disconnected');
        }
      };
      client.onerror = () => setBoth('failed');

      // Clipboard wiring (both directions + paste/focus listeners) lives
      // in the hook; the echo-guard state survives reconnects there.
      clipboard.attach(client, container);

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

      // CSS rescale on every event; the SERVER-side resize (exec of
      // waas-resize in the pod) is debounced and self-gated to
      // in-cluster vnc/rdp — kasmvnc/ssh/remote never call the endpoint.
      sessionResize.attach(container, {
        workspaceId,
        kind,
        protocol: result.protocol,
        onResize: rescale,
      });
      if (autoFocus) container.focus();
    };

    void connect();

    return () => {
      cancelled = true;
      sessionResize.detach();
      clipboard.detach();
      if (keyboard) {
        keyboard.onkeydown = null;
        keyboard.onkeyup = null;
      }
      client?.disconnect();
      tunnelRef.current = null;
      clientRef.current = null;
      // Unmounting the iframe closes the KasmVNC WebSocket: that is the
      // kasm session's disconnect.
      kasmActiveRef.current = false;
      setKasmUrl(null);
      displayHost.replaceChildren();
    };
  }, [
    workspaceId,
    kind,
    effectiveJSON,
    onStateChange,
    onCapabilities,
    autoFocus,
    generation,
    clipboard,
    sessionResize,
  ]);

  return (
    <div
      ref={containerRef}
      // A remote-desktop surface: focusable on purpose (keyboard input
      // is forwarded to the desktop) — role application tells assistive
      // tech that keys are consumed here, not by the page.
      role="application"
      tabIndex={0}
      className="relative h-full w-full overflow-hidden bg-black outline-none focus:ring-1 focus:ring-blue-500/60"
    >
      <div ref={displayRef} className={state === 'connected' ? 'h-full w-full' : 'hidden'} />
      {kasmUrl && (
        // display:none iframes still load, so the "connecting" overlay
        // below covers the pane until the KasmVNC page is up.
        <iframe
          src={kasmUrl}
          title={t('connect.desktopFrame', 'Remote desktop')}
          className={state === 'connected' ? 'h-full w-full border-0' : 'hidden'}
          onLoad={() => {
            setState('connected');
            onStateChange?.('connected');
          }}
        />
      )}
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
