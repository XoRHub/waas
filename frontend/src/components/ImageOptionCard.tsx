import { useTranslation } from 'react-i18next';
import { osFallbackIcon, resolveIcon } from '@/lib/icon';

/** The logo of a template or catalog entry: its icon reference
 * resolved by resolveIcon (https URL, file:<path>, or dashboard-icons
 * slug), else the OS fallback (empty/unknown os = linux) — also
 * swapped in when the resolved image fails to load. */
export function AppIcon({
  icon,
  os,
  size = 40,
  className = '',
}: {
  icon?: string;
  os?: string;
  size?: number;
  className?: string;
}) {
  const fallback = osFallbackIcon(os);
  return (
    <img
      src={resolveIcon(icon, os)}
      onError={(e) => {
        // Never re-assign the fallback to itself: if even the local
        // fallback fails to load, this handler must not loop.
        if (e.currentTarget.getAttribute('src') !== fallback) {
          e.currentTarget.src = fallback;
        }
      }}
      alt=""
      aria-hidden
      width={size}
      height={size}
      className={`shrink-0 ${className}`}
    />
  );
}

export interface ImageOptionCardProps {
  /** Icon reference (https URL, file:<path>, or dashboard-icons
   * slug); absent = OS fallback. */
  icon?: string;
  os?: string;
  title: string;
  subtitle?: string;
  /** Full description (catalog entry or template): surfaces as a "?"
   * affordance on the title line whose native tooltip carries the
   * text — the two-line layout stays compact. */
  description?: string;
  disabled?: boolean;
  /** Shown on the card when disabled — never silently dropped. */
  disabledReason?: string;
  /** Catalog deployment-recommendation badge ("hardened"/"normal");
   * absent shows no pill. Purely cosmetic — see docs/image-catalog.md. */
  profile?: string;
  selected?: boolean;
  onSelect?: () => void;
}

/**
 * One selectable row of the image/template picker — compact two-line
 * layout (logo | title / os · protocols), disabled-with-reason. Long
 * text truncates; the native title bubble carries the full text.
 * Rendered inside ImagePicker's listbox (role=option). Shared brick:
 * the template picker uses it today, the catalog picker of the
 * direct-deploy flow will only have to feed it data.
 */
export function ImageOptionCard({
  icon,
  os,
  title,
  subtitle,
  description,
  disabled,
  disabledReason,
  profile,
  selected,
  onSelect,
}: ImageOptionCardProps) {
  const { t } = useTranslation();
  // Truncated text stays reachable: the full lines ride in the native
  // tooltip of the whole row.
  const tooltip = [title, subtitle, description, disabled ? disabledReason : undefined]
    .filter(Boolean)
    .join('\n');
  return (
    <button
      type="button"
      role="option"
      aria-selected={!!selected}
      disabled={disabled}
      onClick={onSelect}
      title={tooltip}
      className={`flex w-full items-center gap-2.5 border-l-2 px-2.5 py-1.5 text-left transition-colors ${
        selected
          ? 'border-blue-600 bg-blue-50 dark:bg-blue-950/40'
          : 'border-transparent hover:bg-slate-50 dark:hover:bg-slate-700/50'
      } ${disabled ? 'cursor-not-allowed opacity-60 hover:bg-transparent dark:hover:bg-transparent' : ''}`}
    >
      <AppIcon icon={icon} os={os} size={26} />
      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-1.5">
          <span className="truncate text-sm font-medium text-slate-900 dark:text-white">
            {title}
          </span>
          {/* "?" description affordance: a plain span, never a nested
              interactive element (the whole row already is a button) —
              aria-hidden so the long text stays out of the option's
              accessible name; the row-level tooltip below keeps the
              description reachable on the rest of the card too. Its own
              title makes the bubble pop right where the eye is. */}
          {description && (
            <span
              aria-hidden="true"
              title={description}
              className="inline-flex h-4 w-4 shrink-0 cursor-help items-center justify-center rounded-full bg-slate-200 text-[10px] font-semibold text-slate-600 dark:bg-slate-600 dark:text-slate-200"
            >
              ?
            </span>
          )}
          {/* Only the two known values ever render a badge: this prop is
              synced-but-untrusted catalog data (see
              docs/image-catalog.md), and a row synced before a given
              sync pass rewrites it can still hold an arbitrary string —
              anything else silently shows nothing rather than a
              confidently wrong label. */}
          {(profile === 'hardened' || profile === 'normal') && (
            <span
              className={`shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium ${
                profile === 'hardened'
                  ? 'bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-300'
                  : 'bg-slate-100 text-slate-600 dark:bg-slate-700 dark:text-slate-300'
              }`}
            >
              {profile === 'hardened'
                ? t('admin.templatesPage.profileHardened')
                : t('admin.templatesPage.profileNormal')}
            </span>
          )}
        </span>
        <span className="block truncate text-xs text-slate-500 dark:text-slate-400">
          {subtitle}
          {disabled && disabledReason && (
            <span className="text-amber-700 dark:text-amber-400">
              {subtitle ? ' — ' : ''}
              {disabledReason}
            </span>
          )}
        </span>
      </span>
    </button>
  );
}
