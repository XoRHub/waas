import type { Theme } from '@/types';

// Local mirror of the profile preference so the login page (no user yet)
// and the very first paint keep the last chosen theme.
const STORAGE_KEY = 'waas-theme';

const media = window.matchMedia('(prefers-color-scheme: dark)');

export function storedTheme(): Theme {
  const raw = localStorage.getItem(STORAGE_KEY);
  return raw === 'light' || raw === 'dark' ? raw : 'system';
}

/** Applies the theme to the document and remembers it locally. */
export function applyTheme(theme: Theme) {
  localStorage.setItem(STORAGE_KEY, theme);
  const dark = theme === 'dark' || (theme === 'system' && media.matches);
  document.documentElement.classList.toggle('dark', dark);
  document.documentElement.style.colorScheme = dark ? 'dark' : 'light';
}

/** Re-applies on OS theme changes while in system mode. */
export function watchSystemTheme(current: () => Theme): () => void {
  const onChange = () => {
    if (current() === 'system') applyTheme('system');
  };
  media.addEventListener('change', onChange);
  return () => media.removeEventListener('change', onChange);
}
