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
