import { osFallbackIcon, resolveIcon } from '@/lib/icon';

/** The logo of a template or catalog entry: its dashboard-icons CDN
 * icon when a valid slug is present, else the OS fallback
 * (empty/unknown os = linux) — also swapped in when the CDN load
 * fails. */
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
  /** dashboard-icons slug from the catalog entry; absent = OS fallback. */
  icon?: string;
  os?: string;
  title: string;
  subtitle?: string;
  disabled?: boolean;
  /** Shown on the card when disabled — never silently dropped. */
  disabledReason?: string;
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
  disabled,
  disabledReason,
  selected,
  onSelect,
}: ImageOptionCardProps) {
  // Truncated text stays reachable: the full lines ride in the native
  // tooltip of the whole row.
  const tooltip = [title, subtitle, disabled ? disabledReason : undefined]
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
        <span className="block truncate text-sm font-medium text-slate-900 dark:text-white">
          {title}
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
