import { forwardRef, useCallback, useEffect, useImperativeHandle, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import Guacamole from 'guacamole-common-js';
import { useClipboardBridge } from '@/hooks/useClipboardBridge';
import { useSessionResize } from '@/hooks/useSessionResize';
import { api, ApiError } from '@/lib/api';
import { detectServerLayout } from '@/lib/keyboard';
import { useAuthStore } from '@/stores/authStore';
import type { ConnectResult, SessionCapabilities, WorkspaceConnectionPrefs } from '@/types';

export type ConnectionState = 'connecting' | 'connected' | 'disconnected' | 'failed';

// Guacamole.Client state 3 = CONNECTED, 5 = DISCONNECTED.
const STATE_CONNECTED = 3;
const STATE_DISCONNECTED = 5;

// KasmVNC client connection_state → pane state. 'disconnected' is final:
// embedded, the client auto-reconnects on its own ('reconnecting') and
// only reports 'disconnected' once it gave up or the close was clean.
const KASM_STATES: Record<string, ConnectionState> = {
  connecting: 'connecting',
  reconnecting: 'connecting',
  connected: 'connected',
  disconnected: 'disconnected',
};

// Keystrokes inside the KasmVNC iframe never reach the portal window:
// replay each one there so the portal's shortcuts (Ctrl+Alt+M, Escape)
// keep working, and keep from the desktop the ones the portal consumed.
function forwardKey(e: KeyboardEvent) {
  if (!window.dispatchEvent(new KeyboardEvent(e.type, e))) {
    e.preventDefault();
    e.stopPropagation();
  }
}

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
  /** KasmVNC only: video quality preset (the client's video_quality). */
  setQuality: (level: number) => void;
  readQuality: () => number;
}

/**
 * One embedded remote desktop. Keyboard input is scoped to the pane (the
 * container grabs focus on click), so several panes can coexist in a split
 * view without fighting over keystrokes. The desktop image is scaled to
 * fit the pane and re-scaled when the pane is resized or the remote
 * desktop size changes.
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
  // RFC 7807 detail of a failed /connect call — the api-server explains
  // WHY (quota, policy, phase); swallowing it left users with a bare
  // "connection failed".
  const [failDetail, setFailDetail] = useState<string | null>(null);
  // kasmvnc sessions embed KasmVNC's own web client instead of the guac
  // canvas: wwt reverse-proxies the whole app under /kasm/{session}.
  const [kasmUrl, setKasmUrl] = useState<string | null>(null);
  const kasmActiveRef = useRef(false);
  const iframeRef = useRef<HTMLIFrameElement>(null);
  const kasmCapsRef = useRef<SessionCapabilities | undefined>(undefined);
  // The client's own default preset (medium); handed over on every
  // (re)connect so a reconnect keeps the user's choice.
  const kasmQualityRef = useRef(2);
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

  const setBoth = useCallback(
    (s: ConnectionState) => {
      setState(s);
      onStateChange?.(s);
    },
    [onStateChange],
  );

  // Unmounting the iframe closes its WebSocket (the client disconnects
  // cleanly on unload): that is the kasm session's disconnect. Reconnect
  // = generation bump, which also mints a fresh token past its expiry.
  const endKasm = useCallback(
    (s: ConnectionState) => {
      kasmActiveRef.current = false;
      setKasmUrl(null);
      setBoth(s);
    },
    [setBoth],
  );

  // The client only trusts messages from its parent window; same origin
  // either way (wwt serves it under /kasm/), so never post to '*'.
  const postToKasm = (action: string, value: unknown) =>
    iframeRef.current?.contentWindow?.postMessage({ action, value }, window.location.origin);

  useImperativeHandle(ref, () => ({
    disconnect: () => {
      if (kasmActiveRef.current) {
        endKasm('disconnected');
        return;
      }
      clientRef.current?.disconnect();
    },
    reconnect: () => setGeneration((g) => g + 1),
    setClipboard: (direction, enabled) => {
      tunnelRef.current?.sendMessage('', 'waas-clipboard', direction, enabled ? '1' : '0');
    },
    sendClipboard: (text) => {
      if (!kasmActiveRef.current) {
        clipboard.sendClipboard(text, true);
      } else if (kasmCapsRef.current?.clipboardPaste) {
        postToKasm('clipboardsnd', text);
      }
    },
    readRemoteClipboard: () => clipboard.readRemoteClipboard(),
    setQuality: (level) => {
      kasmQualityRef.current = level;
      postToKasm('setvideoquality', level);
    },
    readQuality: () => kasmQualityRef.current,
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
    // Each (re)connect attempt starts clean: a later guac-level failure
    // must not resurface a stale API detail, and a Retry from the
    // failed/closed screen must read as connecting (this runs after the
    // previous attempt's cleanup, so it also wins over the 'disconnected'
    // a guac client reports while being torn down).
    setFailDetail(null);
    setBoth('connecting');

    // Messages from the KasmVNC client (same-origin iframe). Anything not
    // posted by THIS pane's iframe document is ignored: another pane, a
    // popup or a foreign origin must not drive this session.
    const onKasmMessage = (e: MessageEvent) => {
      if (e.source !== iframeRef.current?.contentWindow || e.origin !== window.location.origin) {
        return;
      }
      const { action, value } = (e.data ?? {}) as { action?: string; value?: unknown };
      const next = action === 'connection_state' && typeof value === 'string' && KASM_STATES[value];
      if (next === 'disconnected' || action === 'idle_session_timeout') {
        // idle_session_timeout comes on idle AND once auto-reconnect gave
        // up; the client then navigates itself away — the session is over.
        endKasm('disconnected');
      } else if (next) {
        setBoth(next);
        if (next === 'connected' && autoFocus) iframeRef.current?.focus();
      } else if (
        action === 'clipboardrx' &&
        typeof value === 'string' &&
        kasmCapsRef.current?.clipboardCopy
      ) {
        clipboard.receive(value);
      }
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
      } catch (e) {
        if (!cancelled) {
          setFailDetail(e instanceof ApiError ? e.message : null);
          setBoth('failed');
        }
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
          video_quality: String(kasmQualityRef.current),
          token: result.connectionToken,
        });
        kasmCapsRef.current = result.capabilities;
        window.addEventListener('message', onKasmMessage);
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

      // The remote size can change without a pane resize — guacd's first
      // `size` instruction (width/height are still 0 at STATE_CONNECTED,
      // see rescale() above) or a server-side RandR resize — so the
      // display's own resize hook must rescale too, not only the
      // ResizeObserver wired further down.
      client.getDisplay().onresize = rescale;

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
      // in-cluster vnc/rdp — kasmvnc/remote never call the endpoint.
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
      window.removeEventListener('message', onKasmMessage);
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
    setBoth,
    endKasm,
    clipboard,
    sessionResize,
  ]);

  return (
    <div
      ref={containerRef}
      // A remote-desktop surface: focusable on purpose (keyboard input
      // is forwarded to the desktop) — role application tells assistive
      // tech that keys are consumed here, not by the page. isolate: the
      // status overlay's z-index stays inside the pane, under the page's
      // own controls (Retry).
      role="application"
      tabIndex={0}
      className="relative isolate h-full w-full overflow-hidden bg-black outline-none focus:ring-1 focus:ring-blue-500/60"
    >
      <div ref={displayRef} className={state === 'connected' ? 'h-full w-full' : 'hidden'} />
      {kasmUrl && (
        // Never hidden: the client sizes the remote desktop (resize=remote)
        // from the pane's real dimensions and focuses itself on connect,
        // which fails on a visibility:hidden document. The status text
        // below covers it until 'connected'.
        <iframe
          ref={iframeRef}
          src={kasmUrl}
          title={t('connect.desktopFrame', 'Remote desktop')}
          className="absolute inset-0 h-full w-full border-0"
          onLoad={(e) => {
            const frame = e.currentTarget;
            // The client reports its own state (connection_state). A
            // non-HTML document is wwt refusing the request instead
            // (expired token, ended session: a plain-text http.Error).
            if (frame.contentDocument?.contentType !== 'text/html') {
              endKasm('failed');
              return;
            }
            // Bound to this document's window: it dies with it, and the
            // next load (reload, disconnected.html) binds the new one.
            frame.contentWindow?.addEventListener('keydown', forwardKey, true);
          }}
        />
      )}
      {state !== 'connected' && (
        <div className="absolute inset-0 z-10 flex flex-col items-center justify-center gap-3 bg-black text-sm text-white">
          <p>
            {state === 'connecting' && t('connect.connecting')}
            {state === 'disconnected' && t('connect.disconnected')}
            {state === 'failed' && t('connect.failed')}
          </p>
          {state === 'failed' && failDetail && (
            <p className="max-w-md px-4 text-center text-xs text-slate-400">{failDetail}</p>
          )}
        </div>
      )}
    </div>
  );
});
