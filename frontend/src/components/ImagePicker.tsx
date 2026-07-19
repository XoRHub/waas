import { useState } from 'react';
import { useEscape } from '@/hooks/useEscape';
import { AppIcon, ImageOptionCard } from '@/components/ImageOptionCard';

export interface ImagePickerOption {
  id: string;
  /** dashboard-icons slug from the catalog entry; absent = OS fallback. */
  icon?: string;
  os?: string;
  title: string;
  subtitle?: string;
  /** Full description — the "?" tooltip affordance on the option card. */
  description?: string;
  disabled?: boolean;
  disabledReason?: string;
}

/**
 * Select-like dropdown over ImageOptionCard rows: the closed trigger
 * shows the current choice with its logo on the same compact two-line
 * layout, the popover lists every option (scrollable) — a long catalog
 * never inflates the form. Same dismiss mechanics as the other menus
 * (mouse backdrop + useEscape).
 */
export function ImagePicker({
  options,
  value,
  onChange,
  placeholder,
  label,
}: {
  options: ImagePickerOption[];
  value: string;
  onChange: (id: string) => void;
  /** Trigger text while nothing is selected. */
  placeholder: string;
  /** Accessible name of the trigger and its listbox. */
  label: string;
}) {
  const [open, setOpen] = useState(false);
  useEscape(open, () => setOpen(false));
  const selected = options.find((o) => o.id === value);

  return (
    <div className="relative">
      <button
        type="button"
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-label={label}
        onClick={() => setOpen((o) => !o)}
        title={
          selected ? [selected.title, selected.subtitle].filter(Boolean).join('\n') : undefined
        }
        className="flex w-full items-center gap-2.5 rounded-md border border-slate-300 px-2.5 py-1.5 text-left dark:border-slate-600 dark:bg-slate-700"
      >
        {selected ? (
          <>
            <AppIcon icon={selected.icon} os={selected.os} size={26} />
            <span className="min-w-0 flex-1">
              <span className="block truncate text-sm font-medium text-slate-900 dark:text-white">
                {selected.title}
              </span>
              <span className="block truncate text-xs text-slate-500 dark:text-slate-400">
                {selected.subtitle}
              </span>
            </span>
          </>
        ) : (
          <span className="flex-1 py-1 text-sm text-slate-500 dark:text-slate-400">
            {placeholder}
          </span>
        )}
        <svg
          aria-hidden
          viewBox="0 0 20 20"
          fill="currentColor"
          className={`h-4 w-4 shrink-0 text-slate-400 transition-transform ${open ? 'rotate-180' : ''}`}
        >
          <path
            fillRule="evenodd"
            d="M5.23 7.21a.75.75 0 0 1 1.06.02L10 11.17l3.71-3.94a.75.75 0 1 1 1.08 1.04l-4.25 4.5a.75.75 0 0 1-1.08 0l-4.25-4.5a.75.75 0 0 1 .02-1.06z"
            clipRule="evenodd"
          />
        </svg>
      </button>
      {open && (
        <>
          {/* Mouse-only backdrop: keyboard users close with Escape (useEscape). */}
          <div role="presentation" className="fixed inset-0 z-10" onClick={() => setOpen(false)} />
          {/* In-flow, NOT absolutely positioned: dialog bodies are
              overflow-y-auto scrollers and would clip an absolute
              popover to a sliver. Expanding in place pushes the form
              down instead and scrolls internally past max-h. */}
          <div
            role="listbox"
            aria-label={label}
            className="relative z-20 mt-1 max-h-60 overflow-y-auto rounded-lg bg-white py-1 shadow-lg ring-1 ring-slate-200 dark:bg-slate-800 dark:ring-slate-700"
          >
            {options.map((o) => (
              <ImageOptionCard
                key={o.id}
                icon={o.icon}
                os={o.os}
                title={o.title}
                subtitle={o.subtitle}
                description={o.description}
                disabled={o.disabled}
                disabledReason={o.disabledReason}
                selected={o.id === value}
                onSelect={() => {
                  onChange(o.id);
                  setOpen(false);
                }}
              />
            ))}
          </div>
        </>
      )}
    </div>
  );
}
