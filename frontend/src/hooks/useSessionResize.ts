import { useCallback, useEffect, useMemo, useRef } from 'react';
import { createSessionResizer } from '@/lib/sessionResize';

/**
 * Owns the ResizeObserver + createSessionResizer lifecycle for one pane.
 * The debounce/gating logic stays in lib/sessionResize; this hook only
 * wires it to the DOM: attach() starts observing the container (calling
 * `onResize` on every event for the CSS rescale, and reporting the size
 * for the debounced server-side resize), detach() stops observing and
 * drops any pending POST.
 */
export function useSessionResize() {
  const detachRef = useRef<(() => void) | null>(null);

  const detach = useCallback(() => {
    detachRef.current?.();
    detachRef.current = null;
  }, []);

  const attach = useCallback(
    (
      container: HTMLElement,
      opts: {
        workspaceId: string;
        kind: 'workspace' | 'remote';
        protocol: string;
        /** Runs on EVERY resize event (unlike the debounced report). */
        onResize?: () => void;
      },
    ) => {
      detachRef.current?.();
      const resizer = createSessionResizer({
        workspaceId: opts.workspaceId,
        kind: opts.kind,
        protocol: opts.protocol,
      });
      const observer = new ResizeObserver(() => {
        opts.onResize?.();
        resizer.report(container.clientWidth, container.clientHeight);
      });
      observer.observe(container);
      detachRef.current = () => {
        observer.disconnect();
        resizer.cancel();
      };
    },
    [],
  );

  // Unmount: stop observing if the owner did not detach explicitly.
  useEffect(() => detach, [detach]);

  // Stable object so consumers can list it in effect deps safely.
  return useMemo(() => ({ attach, detach }), [attach, detach]);
}
