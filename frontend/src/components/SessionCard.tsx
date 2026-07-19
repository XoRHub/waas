import { useState, type ReactNode, type Ref } from 'react';
import { useTranslation } from 'react-i18next';
import { AppIcon } from '@/components/ImageOptionCard';
import { StatusBadge } from '@/components/StatusBadge';
import { useReloadWorkspace, useUpdateProfile } from '@/hooks/useApi';
import { useEscape } from '@/hooks/useEscape';
import { useProtocolSwitch } from '@/hooks/useProtocolSwitch';
import { useAuthStore } from '@/stores/authStore';
import type { EffectivePhase } from '@/lib/lifecycle';
import type { SessionTarget } from '@/lib/target';

/**
 * THE portal card, for in-cluster and remote targets alike. Everything a
 * card can do lives here once — title/subtitle, lifecycle badge (when the
 * target has a phase), protocol quick-switch chips, the ⋯ menu with the
 * folder section (folders apply to every kind), Escape handling, delete
 * with confirmation. Kind-specific actions (open/pause vs connect/wake/
 * edit) come in as slots, gated by the target's capabilities where they
 * are rendered.
 */
export function SessionCard({
  target,
  phase,
  message,
  footerNote,
  menuItems,
  buttons,
  onDelete,
  deletePending,
  deleteConfirm,
  ref,
}: {
  target: SessionTarget;
  /** Root-element ref — the open flow tags the clicked card with a
   * view-transition-name so it morphs into the connect screen. */
  ref?: Ref<HTMLDivElement>;
  /** Effective lifecycle phase; only shown when capabilities.hasPhase. */
  phase?: EffectivePhase;
  message?: string;
  footerNote?: ReactNode;
  /** Kind-specific menu entries, rendered above the folder section. */
  menuItems?: { label: string; onClick: () => void }[];
  /** Kind-specific action row; the shared delete button is appended. */
  buttons: ReactNode;
  onDelete: () => void;
  deletePending: boolean;
  /** Confirmation text (window.confirm). Omit when the caller opens its
   * own richer dialog in onDelete (e.g. the volume-retention choice). */
  deleteConfirm?: string;
}) {
  const { t } = useTranslation();
  const user = useAuthStore((s) => s.user);
  const updateProfile = useUpdateProfile();
  const reload = useReloadWorkspace();
  const [menuOpen, setMenuOpen] = useState(false);
  useEscape(menuOpen, () => setMenuOpen(false));
  const { active, switchTo, pending: switching } = useProtocolSwitch(target);
  // The drift badge triggers the manual reload only where it can work:
  // reload-capable targets (operator-managed compute) that are Running —
  // a down workspace converges at its next start anyway.
  const canReload = target.capabilities.reload && phase === 'Running';

  // Folders are a per-target preference keyed by id — they apply to
  // remote machines exactly like in-cluster workspaces.
  const folders = user?.preferences?.workspaceFolders ?? {};
  const currentFolder = folders[target.id];
  const existingFolders = [...new Set(Object.values(folders))].sort();

  const moveToFolder = (folder: string | null) => {
    setMenuOpen(false);
    const next = { ...folders };
    if (folder) {
      next[target.id] = folder;
    } else {
      delete next[target.id];
    }
    updateProfile.mutate({
      preferences: { ...user?.preferences, workspaceFolders: next },
    });
  };

  const onNewFolder = () => {
    const name = window.prompt(t('portal.newFolderPrompt'))?.trim();
    if (name) moveToFolder(name);
    else setMenuOpen(false);
  };

  return (
    <div
      ref={ref}
      className="flex flex-col gap-3 rounded-xl bg-white p-5 shadow-sm dark:bg-slate-800"
    >
      <div className="flex items-start justify-between">
        <div className="flex min-w-0 items-center gap-3">
          <AppIcon icon={target.icon} os={target.os} size={32} />
          <div className="min-w-0">
            <h2 className="truncate font-medium text-slate-900 dark:text-white">
              {target.displayName}
            </h2>
            {/* Flex row, not one truncating <p>: truncate is
                overflow-hidden and would clip the "?" tooltip bubble —
                only the subtitle span ellipsizes. */}
            <p className="flex items-center gap-1 text-xs text-slate-500 dark:text-slate-400">
              <span className="min-w-0 truncate">{target.subtitle}</span>
              {target.description && (
                <span className="group relative inline-flex shrink-0">
                  {/* Same "?" affordance as the picker cards, but with
                      the card's styled hover bubble (the drift-badge
                      tooltip pattern above) — instant, multi-line. */}
                  <span
                    aria-hidden="true"
                    className="inline-flex h-3.5 w-3.5 cursor-help items-center justify-center rounded-full bg-slate-200 text-[9px] font-semibold text-slate-600 dark:bg-slate-600 dark:text-slate-200"
                  >
                    ?
                  </span>
                  <span
                    role="tooltip"
                    className="pointer-events-none absolute left-0 top-full z-30 mt-1 hidden w-64 rounded-md bg-slate-800 px-3 py-2 text-xs text-white shadow-lg group-hover:block dark:bg-slate-700"
                  >
                    {target.description}
                  </span>
                </span>
              )}
              {currentFolder && <span className="shrink-0">📁 {currentFolder}</span>}
            </p>
          </div>
        </div>
        <div className="flex items-center gap-1">
          {target.templateDrifted && (
            <span className="group relative inline-flex">
              <button
                type="button"
                aria-label={t('portal.drift.full')}
                disabled={reload.isPending}
                onClick={() => {
                  if (!canReload) return;
                  if (window.confirm(t('portal.drift.reloadConfirm'))) reload.mutate(target.id);
                }}
                className={`rounded-full bg-amber-100 px-2 py-0.5 text-xs font-medium text-amber-800 disabled:opacity-40 dark:bg-amber-900/50 dark:text-amber-200 ${
                  canReload
                    ? 'cursor-pointer hover:bg-amber-200 dark:hover:bg-amber-900'
                    : 'cursor-help'
                }`}
              >
                {t('portal.drift.badge')}
              </button>
              <span
                role="tooltip"
                className="pointer-events-none absolute left-1/2 top-full z-30 mt-1 hidden w-64 -translate-x-1/2 rounded-md bg-slate-800 px-3 py-2 text-xs text-white shadow-lg group-hover:block dark:bg-slate-700"
              >
                <ul className="list-disc space-y-1 pl-4 text-left">
                  <li>{t('portal.drift.why')}</li>
                  <li>{t('portal.drift.when')}</li>
                  {canReload && <li>{t('portal.drift.how')}</li>}
                </ul>
              </span>
            </span>
          )}
          <div className="relative">
            <button
              onClick={() => setMenuOpen((v) => !v)}
              className="rounded px-1.5 text-slate-500 hover:bg-slate-100 dark:text-slate-400 dark:hover:bg-slate-700"
              aria-haspopup="menu"
              aria-expanded={menuOpen}
            >
              ⋯
            </button>
            {menuOpen && (
              <>
                {/* Mouse-only backdrop: keyboard users close with Escape (useEscape). */}
                <div
                  role="presentation"
                  className="fixed inset-0 z-10"
                  onClick={() => setMenuOpen(false)}
                />
                <div
                  role="menu"
                  className="absolute right-0 z-20 mt-1 w-52 overflow-hidden rounded-lg bg-white py-1 text-sm shadow-lg ring-1 ring-slate-200 dark:bg-slate-800 dark:ring-slate-700"
                >
                  {(menuItems ?? []).map((item) => (
                    <CardMenuItem
                      key={item.label}
                      onClick={() => {
                        setMenuOpen(false);
                        item.onClick();
                      }}
                    >
                      {item.label}
                    </CardMenuItem>
                  ))}
                  {(menuItems?.length ?? 0) > 0 && (
                    <div className="my-1 border-t border-slate-200 dark:border-slate-700" />
                  )}
                  <p className="px-4 py-1 text-xs text-slate-400">{t('portal.moveToFolder')}</p>
                  {existingFolders
                    .filter((f) => f !== currentFolder)
                    .map((f) => (
                      <CardMenuItem key={f} onClick={() => moveToFolder(f)}>
                        📁 {f}
                      </CardMenuItem>
                    ))}
                  <CardMenuItem onClick={onNewFolder}>{t('portal.newFolder')}</CardMenuItem>
                  {currentFolder && (
                    <CardMenuItem onClick={() => moveToFolder(null)}>
                      {t('portal.removeFromFolder')}
                    </CardMenuItem>
                  )}
                </div>
              </>
            )}
          </div>
        </div>
      </div>

      {/* Targets without protocols shouldn't exist, but the phase badge must
          not vanish with them — hence the OR guard. */}
      {(target.protocols.length > 0 || (target.capabilities.hasPhase && phase)) && (
        <div className="flex items-center gap-1">
          {target.protocols.length > 0 && (
            <div className="flex items-center gap-1" title={t('portal.switchProtocolHint')}>
              {target.protocols.map((p) => (
                <button
                  key={p.name}
                  onClick={() => switchTo(p.name)}
                  disabled={switching}
                  className={`rounded-full px-2 py-0.5 text-[11px] font-medium uppercase ${
                    p.name === active
                      ? 'bg-blue-100 text-blue-700 dark:bg-blue-900/50 dark:text-blue-300'
                      : 'bg-slate-100 text-slate-500 hover:bg-slate-200 dark:bg-slate-700 dark:text-slate-400 dark:hover:bg-slate-600'
                  }`}
                >
                  {p.name}
                </button>
              ))}
            </div>
          )}
          {target.capabilities.hasPhase && phase && (
            <span className="ml-auto">
              <StatusBadge phase={phase} />
            </span>
          )}
        </div>
      )}

      {message && <p className="text-xs text-slate-500 dark:text-slate-400">{message}</p>}
      {footerNote}

      <div className="mt-auto flex gap-2">
        {buttons}
        <button
          onClick={() => {
            if (!deleteConfirm || window.confirm(deleteConfirm)) onDelete();
          }}
          disabled={deletePending}
          className="rounded-md border border-slate-300 px-3 py-1.5 text-sm text-red-600 hover:bg-red-50 disabled:opacity-40 dark:border-slate-600 dark:hover:bg-slate-700"
        >
          {t('app.delete')}
        </button>
      </div>
    </div>
  );
}

function CardMenuItem({ onClick, children }: { onClick: () => void; children: ReactNode }) {
  return (
    <button
      role="menuitem"
      onClick={onClick}
      className="block w-full px-4 py-1.5 text-left text-slate-700 hover:bg-slate-50 dark:text-slate-200 dark:hover:bg-slate-700"
    >
      {children}
    </button>
  );
}

/**
 * FolderedGrid groups any card list by the user's folders — shared by the
 * workspaces and remote tabs so grouping behaves identically everywhere.
 */
export function FolderedGrid<T extends { id: string }>({
  items,
  renderCard,
}: {
  items: T[];
  renderCard: (item: T) => ReactNode;
}) {
  const { t } = useTranslation();
  const user = useAuthStore((s) => s.user);
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({});

  const folderOf = user?.preferences?.workspaceFolders ?? {};
  const groups = new Map<string, T[]>();
  for (const item of items) {
    const folder = folderOf[item.id] ?? '';
    groups.set(folder, [...(groups.get(folder) ?? []), item]);
  }
  const folderNames = [...groups.keys()].filter((f) => f !== '').sort();

  const grid = (list: T[]) => (
    <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">{list.map(renderCard)}</div>
  );

  return (
    <div className="space-y-6">
      {folderNames.map((folder) => (
        <section key={folder}>
          <button
            onClick={() => setCollapsed((c) => ({ ...c, [folder]: !c[folder] }))}
            className="mb-3 flex items-center gap-2 text-sm font-semibold text-slate-700 dark:text-slate-200"
          >
            <span className="text-xs">{collapsed[folder] ? '▶' : '▼'}</span>
            <span>📁 {folder}</span>
            <span className="font-normal text-slate-400">({groups.get(folder)!.length})</span>
          </button>
          {!collapsed[folder] && grid(groups.get(folder)!)}
        </section>
      ))}
      {groups.has('') && (
        <section>
          {folderNames.length > 0 && (
            <h2 className="mb-3 text-sm font-semibold text-slate-500 dark:text-slate-400">
              {t('portal.unfiled')}
            </h2>
          )}
          {grid(groups.get('')!)}
        </section>
      )}
    </div>
  );
}
