import { useEffect, useRef } from 'react';

// Active surfaces, in opening order: Escape only ever closes the
// TOP-MOST one, so a dropdown inside a dialog closes without taking
// the dialog down with it (every surface registers on window; without
// the stack one keypress fired every handler at once).
const stack: (() => void)[] = [];

/**
 * Global Escape behavior: EVERY dismissible surface (context menus,
 * dropdowns, dialogs, overlays) registers through this hook, so "Escape
 * closes it" is one mechanism, not a per-component afterthought.
 * Layered surfaces close outside-in, one keypress each.
 */
export function useEscape(active: boolean, onClose: () => void) {
  // Ref'd so the registration effect runs once per active-transition —
  // the stack keeps the true opening order even when callers pass a
  // new closure every render.
  const closeRef = useRef(onClose);
  useEffect(() => {
    closeRef.current = onClose;
  });
  useEffect(() => {
    if (!active) return;
    const entry = () => closeRef.current();
    stack.push(entry);
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && stack[stack.length - 1] === entry) {
        e.preventDefault();
        entry();
      }
    };
    window.addEventListener('keydown', onKey);
    return () => {
      const i = stack.indexOf(entry);
      if (i >= 0) stack.splice(i, 1);
      window.removeEventListener('keydown', onKey);
    };
  }, [active]);
}
