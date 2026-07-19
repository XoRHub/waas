import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ImagePicker } from '@/components/ImagePicker';
import { ImageOptionCard } from '@/components/ImageOptionCard';
import { useEscape } from '@/hooks/useEscape';
import { useAdminImages } from '@/hooks/useApi';
import { fuzzyFilter } from '@/lib/fuzzy';
import type { CatalogImage, DeploymentRecommendation, DiscoveredImage } from '@/types';
import { field } from './fields';

/** Sentinel picker id for "no catalog — free input": resets the local
 * selection without touching the image value. Cannot collide with a
 * catalog name (K8s names never start with an underscore). */
const NONE = '__none__';

/** Everything a discovered entry can be searched by, in one string. */
function searchText(d: DiscoveredImage): string {
  return [d.image, d.displayName, d.app, d.version].filter(Boolean).join(' ');
}

/**
 * Catalog-assisted image field of the admin template form. The
 * template's `image` stays a free string end to end: the picker above
 * the field is an input helper, and the image input itself doubles as
 * the search box — with a registry-mode catalog selected, whatever is
 * typed both IS the value and fuzzy-filters the catalog's discovered
 * entries in a combobox dropdown (one field, not a search box plus a
 * result field). A single-image catalog fills the field right on
 * selection. Typing or pasting a raw reference always works,
 * catalog or not.
 *
 * Which catalog is being browsed is purely local UI state (never part
 * of TemplateInput). Only enabled catalogs are offered — a disabled
 * one is not a valid suggestion source, and the field stays free-form
 * for anything else.
 */
export function CatalogImageField({
  image,
  onChange,
  onApplyRecommendation,
  onArchitectures,
  onIdentity,
}: {
  image: string;
  onChange: (image: string) => void;
  /** Explicit "apply the catalog's recommendation" action — never
   * triggered by onSelect/onChange, only by the button rendered when
   * the currently selected discovered image carries one. Passes the
   * catalog ENTRY's supported protocols alongside (protocols are
   * entry-level, not per discovered image) so the recommendation can
   * be applied protocol-aware. */
  onApplyRecommendation?: (recommended: DeploymentRecommendation, imageProtocols: string[]) => void;
  /** Fired on explicit picker selections (a discovered card, or a
   * single-image catalog) with the architectures the pick is published
   * for — the per-image list when the manifest carries one, else the
   * entry-level one; [] = unknown. Never fired on free typing: a pasted
   * reference has no arch info to offer. */
  onArchitectures?: (architectures: string[]) => void;
  /** Fired when a discovered card is picked, with the entry's
   * displayName/description ('' when the manifest omits them) — the
   * dialog's non-destructive identity prefill. Never fired on free
   * typing or on a single-image catalog: neither carries discovered
   * metadata. */
  onIdentity?: (displayName: string, description: string) => void;
}) {
  const { t } = useTranslation();
  const images = useAdminImages();
  const [catalogName, setCatalogName] = useState('');
  const [open, setOpen] = useState(false);
  useEscape(open, () => setOpen(false));

  const catalogs: CatalogImage[] = (images.data?.data ?? []).filter((c) => c.enabled);
  const selected = catalogs.find((c) => c.name === catalogName);
  const discovered = selected?.discovered ?? [];

  const options = [
    { id: NONE, title: t('admin.templatesPage.imageCatalogNone') },
    ...catalogs.map((c) => ({
      id: c.name,
      icon: c.discovered?.[0]?.icon,
      os: c.discovered?.[0]?.os,
      title: c.displayName,
      subtitle: c.discovered?.length
        ? t('admin.templatesPage.imageCatalogCount', { count: c.discovered.length })
        : c.image,
    })),
  ];

  const selectCatalog = (id: string) => {
    if (id === NONE) {
      setCatalogName('');
      setOpen(false);
      return;
    }
    setCatalogName(id);
    const catalog = catalogs.find((c) => c.name === id);
    if (catalog?.image && !catalog.discovered?.length) {
      // Single-image mode: there is exactly one image to offer, no
      // suggestions to browse — fill the field right away.
      onChange(catalog.image);
      onArchitectures?.(catalog.architectures ?? []);
      setOpen(false);
      return;
    }
    // Registry mode: drop the suggestions open so the catalog is
    // browsable before anything is typed.
    setOpen(true);
  };

  // The field value is the query: an empty field lists the whole
  // catalog (fuzzyFilter passes it through untouched).
  const results = fuzzyFilter(discovered, image, searchText);

  // The discovered entry backing the CURRENT image value, if any — the
  // apply-recommendation action reads from it, not from the last click,
  // so it stays correct if the admin types over the field afterward.
  const selectedDiscovered = discovered.find((d) => d.image === image);

  return (
    <div className="space-y-2">
      <div>
        <span className="text-sm text-slate-600 dark:text-slate-300">
          {t('admin.templatesPage.imageCatalog')}
        </span>
        <div className="mt-1">
          <ImagePicker
            label={t('admin.templatesPage.imageCatalog')}
            placeholder={t('admin.templatesPage.imageCatalogNone')}
            value={catalogName}
            onChange={selectCatalog}
            options={options}
          />
        </div>
      </div>

      {/* No backdrop here, unlike ImagePicker: a backdrop would eat
          the first click on anything else (the catalog picker sits
          right above). Combobox semantics instead — the suggestions
          close when focus leaves the input+list group, on Escape
          (useEscape), or on selection. The option rows are <button>s,
          so clicking one keeps focus inside the group. */}
      <div
        onBlur={(e) => {
          if (!e.currentTarget.contains(e.relatedTarget)) setOpen(false);
        }}
      >
        {/* Not a single <label>: the apply-recommendation button must
            sit beside the field without becoming part of the input's
            accessible name (a control nested inside a <label> gets
            folded into the label text otherwise). aria-label on the
            input replaces the implicit label association. */}
        <div className="flex items-center justify-between gap-2">
          <span className="text-sm text-slate-600 dark:text-slate-300">
            {t('admin.templatesPage.image')}
          </span>
          {selectedDiscovered?.recommended && onApplyRecommendation && (
            <button
              type="button"
              onClick={() =>
                onApplyRecommendation(selectedDiscovered.recommended!, selected?.protocols ?? [])
              }
              title={t('admin.templatesPage.applyRecommendationHint')}
              className="inline-flex items-center gap-1 rounded-md bg-blue-600 px-2.5 py-1 text-xs font-medium text-white hover:bg-blue-700"
            >
              <span aria-hidden="true">✨</span>
              {t('admin.templatesPage.applyRecommendation')}
            </button>
          )}
        </div>
        <input
          aria-label={t('admin.templatesPage.image')}
          className={field}
          value={image}
          onChange={(e) => {
            onChange(e.target.value);
            if (discovered.length > 0) setOpen(true);
          }}
          onFocus={() => {
            if (discovered.length > 0) setOpen(true);
          }}
          placeholder={discovered.length > 0 ? t('admin.templatesPage.imageSearch') : undefined}
          required
        />
        {open && discovered.length > 0 && (
          /* In-flow like ImagePicker's listbox: dialog bodies are
             overflow-y-auto scrollers and would clip an absolute
             popover — expanding in place pushes the form down. */
          <div
            role="listbox"
            aria-label={t('admin.templatesPage.image')}
            className="mt-1 max-h-60 overflow-y-auto rounded-lg bg-white py-1 shadow-lg ring-1 ring-slate-200 dark:bg-slate-800 dark:ring-slate-700"
          >
            {results.map((d) => (
              <ImageOptionCard
                key={d.image}
                icon={d.icon}
                os={d.os}
                title={d.displayName || d.app || d.image}
                subtitle={d.version || d.image}
                description={d.description}
                profile={d.profile}
                selected={d.image === image}
                onSelect={() => {
                  onChange(d.image);
                  onArchitectures?.(
                    d.architectures?.length ? d.architectures : (selected?.architectures ?? []),
                  );
                  onIdentity?.(d.displayName ?? '', d.description ?? '');
                  setOpen(false);
                }}
              />
            ))}
            {results.length === 0 && (
              <p className="px-2.5 py-1.5 text-sm text-slate-500 dark:text-slate-400">
                {t('admin.templatesPage.imageSearchEmpty')}
              </p>
            )}
          </div>
        )}
        <p className="mt-1 text-xs text-slate-500 dark:text-slate-400">
          {t('admin.templatesPage.imageFreeHint')}
        </p>
      </div>
    </div>
  );
}
