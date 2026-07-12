import { useCallback, useEffect, useMemo, useRef } from 'react';
import Guacamole from 'guacamole-common-js';
import { canReadSystemClipboard, ClipboardSync, hasClipboardApi } from '@/lib/clipboard';

/**
 * The slice of Guacamole.Client the clipboard bridge needs — keeping the
 * surface this narrow is what makes the hook testable without a tunnel.
 */
export interface ClipboardClient {
  onclipboard:
    ((stream: InstanceType<typeof Guacamole.InputStream>, mimetype: string) => void) | null;
  createClipboardStream(mimetype: string): InstanceType<typeof Guacamole.OutputStream>;
}

/**
 * Bidirectional clipboard bridge for one desktop pane. The echo-guard
 * state (ClipboardSync) lives in the hook and SURVIVES reconnects — the
 * desktop's copy is still the latest known one; attach() binds the
 * sender/listeners to the live client, detach() unbinds them.
 *
 * Policy enforcement stays server-side (wwt drops forbidden streams);
 * this hook only owns the browser wiring: guac clipboard streams in both
 * directions, the DOM paste safety net, and the focus re-sync.
 */
export function useClipboardBridge() {
  const syncRef = useRef(new ClipboardSync());
  const sendRef = useRef<(text: string, force?: boolean) => void>(() => {});
  const detachRef = useRef<(() => void) | null>(null);

  // Seamless local→remote path (Chromium + HTTPS): re-read the system
  // clipboard so the desktop already holds it when the user pastes
  // inside the session. No-op while detached (sendRef is a no-op).
  const syncFromSystem = useCallback(() => {
    if (!canReadSystemClipboard() || !document.hasFocus()) return;
    navigator.clipboard
      .readText()
      .then((text) => sendRef.current(text))
      .catch(() => {}); // permission denied: paste event still works
  }, []);

  const detach = useCallback(() => {
    detachRef.current?.();
    detachRef.current = null;
  }, []);

  const attach = useCallback(
    (client: ClipboardClient, container: HTMLElement) => {
      detachRef.current?.();
      const sync = syncRef.current;

      // ---- clipboard: desktop → browser --------------------------------
      // wwt already dropped this stream when policy forbids copy; anything
      // arriving here is allowed. Non-text clipboards are not relayed.
      client.onclipboard = (stream, mimetype) => {
        if (!mimetype.startsWith('text/')) {
          stream.sendAck('unsupported clipboard type', 0x0100);
          return;
        }
        const reader = new Guacamole.StringReader(stream);
        let data = '';
        reader.ontext = (text) => {
          data += text;
        };
        reader.onend = () => {
          sync.receive(data);
          // Best effort: writeText needs a secure context AND document
          // focus. The overlay's manual exchange covers the rest.
          if (hasClipboardApi() && document.hasFocus()) {
            void navigator.clipboard.writeText(data).catch(() => {});
          }
        };
      };

      // ---- clipboard: browser → desktop --------------------------------
      // force bypasses the echo guard for explicit user actions (paste
      // event, overlay send button).
      const sendClipboardText = (text: string, force = false) => {
        if (text === '' || (!force && !sync.shouldSend(text))) return;
        const stream = client.createClipboardStream('text/plain');
        const writer = new Guacamole.StringWriter(stream);
        writer.sendText(text);
        writer.sendEnd();
        sync.sent(text);
      };
      sendRef.current = sendClipboardText;

      // DOM paste event: a safety net only — it does NOT fire on a real
      // Ctrl+V in the pane, because Guacamole.Keyboard preventDefaults
      // every keydown it relays, which suppresses the browser's native
      // paste action (verified live). Letting Ctrl+V's default through
      // would race the relayed keystroke against the clipboard stream and
      // paste stale content. Seamless local→remote is the focus sync
      // below; without it, the overlay's manual exchange is the fallback.
      const onPaste = (e: ClipboardEvent) => {
        const text = e.clipboardData?.getData('text/plain');
        if (text) sendClipboardText(text, true);
      };
      container.addEventListener('paste', onPaste);
      window.addEventListener('focus', syncFromSystem);

      detachRef.current = () => {
        container.removeEventListener('paste', onPaste);
        window.removeEventListener('focus', syncFromSystem);
        client.onclipboard = null;
        sendRef.current = () => {};
      };
    },
    [syncFromSystem],
  );

  // Unmount: drop the listeners if the owner did not detach explicitly.
  useEffect(() => detach, [detach]);

  const sendClipboard = useCallback((text: string, force = false) => {
    sendRef.current(text, force);
  }, []);

  const readRemoteClipboard = useCallback(() => syncRef.current.lastReceived, []);

  // Stable object so consumers can list it in effect deps safely.
  return useMemo(
    () => ({
      /** Bind the bridge to a live client + pane container. */
      attach,
      /** Unbind (reconnect/unmount); the echo-guard state is kept. */
      detach,
      /** Push text to the desktop clipboard; force bypasses the echo guard. */
      sendClipboard,
      /** Last text the desktop clipboard sent us (overlay manual fallback). */
      readRemoteClipboard,
      /** Prime/refresh the desktop with the current system clipboard. */
      syncFromSystem,
    }),
    [attach, detach, sendClipboard, readRemoteClipboard, syncFromSystem],
  );
}
