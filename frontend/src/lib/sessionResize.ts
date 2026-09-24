import { api } from '@/lib/api';

/**
 * Server-side session resize, debounced.
 *
 * WaaS-specific mechanism, NOT guacd's native resize: Guacamole's VNC
 * client never pushes a resize mid-session — the api-server instead
 * execs the image's waas-resize helper (RandR) inside the pod
 * (docs/session-resize.md).
 *
 * Only in-cluster vnc sessions qualify: kasmvnc resizes natively in its
 * own client, an in-cluster rdp desktop is a windows VM with no pod to
 * exec into, remote machines have no pod either.
 * Browser resizes fire dozens of ResizeObserver events per second, so
 * the POST goes out `delayMs` after the LAST change only.
 */
export function createSessionResizer({
  workspaceId,
  kind,
  protocol,
  delayMs = 500,
}: {
  workspaceId: string;
  kind: 'workspace' | 'remote';
  protocol: string;
  delayMs?: number;
}) {
  const active = kind === 'workspace' && protocol === 'vnc';
  let timer: ReturnType<typeof setTimeout> | undefined;
  let lastSent = '';
  return {
    /** Report the pane's current size; POSTs delayMs after the last change. */
    report(width: number, height: number): void {
      if (!active) return;
      const w = Math.round(width);
      const h = Math.round(height);
      if (w <= 0 || h <= 0) return;
      const mode = `${w}x${h}`;
      if (mode === lastSent) return;
      clearTimeout(timer);
      timer = setTimeout(() => {
        lastSent = mode;
        api.post(`/api/v1/workspaces/${workspaceId}/resize`, { width: w, height: h }).catch(() => {
          lastSent = ''; // failed: allow the next change to retry
        });
      }, delayMs);
    },
    /** Drop any pending POST (pane unmount / reconnect). */
    cancel(): void {
      clearTimeout(timer);
    },
  };
}
