import type { NavigateFunction } from 'react-router';

// openWorkspace applies the user's tab preference for one workspace.
// A same-tab open asks for a view transition (the clicked card morphs
// into the connect screen — see index.css `workspace-open`) when the
// browser supports it and the user allows motion; new-tab opens and
// non-supporting browsers stay instant. Feature-detecting here (not
// only in react-router) also keeps unsupported browsers warning-free.
export function openWorkspace(url: string, newTab: boolean, navigate: NavigateFunction) {
  if (newTab) {
    window.open(url, '_blank');
    return;
  }
  const animate =
    typeof document.startViewTransition === 'function' &&
    !window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  void navigate(url, { viewTransition: animate });
}
