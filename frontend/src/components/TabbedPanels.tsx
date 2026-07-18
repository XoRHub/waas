import { useState, type FormEvent, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

export interface PanelTab {
  id: string;
  label: string;
  /** Read-only tab (right absent but a value is stored): 🔒 on the tab,
   *  content rendered as the consumer passed it (disabled fields are the
   *  consumer's job). Dropped entirely under hideLocked. */
  locked?: boolean;
  /** Optional extra marker inside the tab button. */
  badge?: ReactNode;
  content: ReactNode;
}

/**
 * The shared section/tab organizer of every settings-style dialog
 * (connection settings, workspace creation, admin template editor):
 * one tab row + one panel per tab, instead of an endless vertical form.
 *
 * - Panels are MOUNTED but hidden: editor drafts (KeyValueEditor,
 *   ScheduleEditor) and native form validation survive tab switches.
 * - A required field left invalid in an inactive panel auto-activates
 *   its tab on submit — without this the browser aborts on an
 *   unfocusable hidden control and the form fails silently.
 * - Unauthorized tabs are simply ABSENT from `tabs` (the consumer
 *   filters via canOverrideField; admins pass everything). `locked`
 *   covers the in-between: no right but a stored value to show.
 * - Controlled (`active`+`onSelect`, e.g. for a tab-dependent dialog
 *   footer) or uncontrolled.
 *
 * Nesting is supported (a tab whose content is another TabbedPanels);
 * keep tab ids distinct across levels — the invalid-capture walk stops
 * at the closest ancestor panel whose id belongs to THIS instance.
 */
export function TabbedPanels({
  tabs,
  active,
  onSelect,
  hideLocked,
}: {
  tabs: PanelTab[];
  /** Controlled active tab id; leave unset for internal state. */
  active?: string;
  onSelect?: (id: string) => void;
  /** Policy switch: drop locked tabs instead of showing them read-only.
   *  Default false — stored state stays visible. */
  hideLocked?: boolean;
}) {
  const { t } = useTranslation();
  const visible = hideLocked ? tabs.filter((tab) => !tab.locked) : tabs;
  const [internal, setInternal] = useState(visible[0]?.id ?? '');
  const current = active ?? internal;
  // Reconciliation: rights load async and tabs may disappear — an
  // active id no longer in the list falls back to the first tab.
  const effective = visible.some((tab) => tab.id === current) ? current : (visible[0]?.id ?? '');

  const select = (id: string) => {
    setInternal(id);
    onSelect?.(id);
  };

  // Native validation vs hidden panels: activate the panel owning the
  // invalid control, unhiding it IMMEDIATELY (before React re-renders)
  // so the browser can focus it and show its bubble.
  const onInvalidCapture = (event: FormEvent) => {
    let el = event.target as HTMLElement | null;
    while (el) {
      const panel = el.closest('[data-panel]');
      if (!(panel instanceof HTMLElement) || !panel.dataset.panel) return;
      if (visible.some((tab) => tab.id === panel.dataset.panel)) {
        if (panel.dataset.panel !== effective) {
          panel.hidden = false;
          select(panel.dataset.panel);
        }
        return;
      }
      // A nested TabbedPanels' panel: keep walking up to OUR level.
      el = panel.parentElement;
    }
  };

  if (visible.length === 0) return null;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-1 border-b border-slate-200 dark:border-slate-700">
        {visible.map((tab) => (
          <button
            key={tab.id}
            type="button"
            onClick={() => select(tab.id)}
            className={`-mb-px flex items-center gap-1.5 rounded-t-md border-x border-t px-3 py-1.5 text-sm font-medium ${
              tab.id === effective
                ? 'border-slate-200 bg-white text-blue-600 dark:border-slate-700 dark:bg-slate-800 dark:text-blue-400'
                : 'border-transparent text-slate-500 hover:text-slate-700 dark:text-slate-400 dark:hover:text-slate-200'
            }`}
          >
            {tab.label}
            {tab.locked && (
              // Admin dialogs never pass locked tabs, so the user-dialog
              // wording is the only one this title ever needs.
              <span className="text-[11px]" title={t('portal.runtime.locked')}>
                🔒
              </span>
            )}
            {tab.badge}
          </button>
        ))}
      </div>
      <div onInvalidCapture={onInvalidCapture}>
        {visible.map((tab) => (
          <div
            key={tab.id}
            data-panel={tab.id}
            hidden={tab.id !== effective}
            className="space-y-4"
          >
            {tab.content}
          </div>
        ))}
      </div>
    </div>
  );
}
