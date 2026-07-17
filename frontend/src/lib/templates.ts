import type { CatalogImage, WorkspaceTemplate } from '@/types';

export interface TemplateAvailability {
  template: WorkspaceTemplate;
  /** false = the user's policy does not allow the template's image. */
  available: boolean;
}

/**
 * Joins the template list with the user's catalog for the create dialog.
 *
 * EVERY template is returned, whatever its protocol (ssh/vnc/rdp) — the
 * ones whose image is missing from the caller's catalog are flagged
 * unavailable instead of being dropped. Silently dropping them is how
 * SSH templates "disappeared" from the portal when the default policy
 * seed forgot the dev-ssh image: the admission webhook stays the real
 * gate, the UI's job is to show WHY a template cannot be used.
 *
 * `catalog` undefined (still loading or failed) degrades to "everything
 * available" — the server re-validates at creation anyway.
 */
export function templateAvailability(
  templates: WorkspaceTemplate[],
  catalog: CatalogImage[] | undefined,
): TemplateAvailability[] {
  return templates.map((template) => ({
    template,
    available: !catalog || catalog.some((img) => img.templates?.includes(template.name)),
  }));
}

/**
 * Icon reference of a template: its explicit spec.logo, else the
 * catalog-sync icon of the discovered entry whose exact reference
 * matches the template's image. Discovered entries are searched across
 * the WHOLE catalog, not just the CatalogImage listing the template:
 * a template is attributed to the single-image entry approving it,
 * while the icons live on the registry-mode entry's discovered list —
 * scoping the lookup to the approving entry is how catalog-based
 * templates rendered the OS fallback. Both absent (or the template
 * gone) = undefined, and the caller's AppIcon falls back to the OS
 * icon. Shared by the create picker and the workspace cards so both
 * resolve the same logo.
 */
export function templateIcon(
  tpl: WorkspaceTemplate | undefined,
  catalog: CatalogImage[] | undefined,
): string | undefined {
  if (!tpl) return undefined;
  return (
    tpl.logo ||
    catalog?.flatMap((img) => img.discovered ?? []).find((d) => d.image === tpl.image && d.icon)
      ?.icon
  );
}
