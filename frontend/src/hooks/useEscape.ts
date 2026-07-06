import { useEffect } from 'react';

/**
 * Global Escape behavior: EVERY dismissible surface (context menus,
 * dropdowns, dialogs, overlays) registers through this hook, so "Escape
 * closes it" is one mechanism, not a per-component afterthought.
 */
export function useEscape(active: boolean, onClose: () => void) {
  useEffect(() => {
    if (!active) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        onClose();
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [active, onClose]);
}
