/**
 * formatDateTime renders an ISO timestamp with the browser locale's
 * default date+time format — the one idiom for every table cell showing
 * an instant (audit, fleet, users, volumes). Empty/absent input renders
 * the fallback (an em dash unless the caller has a better word, e.g.
 * "never").
 */
export function formatDateTime(
  iso: string | null | undefined,
  opts?: { fallback?: string },
): string {
  if (!iso) return opts?.fallback ?? '—';
  return new Date(iso).toLocaleString();
}
