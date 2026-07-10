/**
 * Helpers over a template's RAW userParams list — the list as configured
 * on the CR, mixing exact parameter names and `cat:X` category selectors
 * (cat:audio = every non-platform parameter of the category, resolved
 * server-side). Only the template EDITOR manipulates this raw shape;
 * connect-time forms consume the flat `resolvedUserParams` list the
 * api-server exposes and never parse `cat:` themselves.
 *
 * Entries are purely additive: `cat:audio` next to `audio-servername`
 * grants exactly the same thing as `cat:audio` alone, so the toggles
 * below only ever ADD or REMOVE entries — there is no priority rule.
 */

/** Mirror of Go params.CategorySelectorPrefix. */
export const CATEGORY_SELECTOR_PREFIX = 'cat:';

export function categorySelector(category: string): string {
  return `${CATEGORY_SELECTOR_PREFIX}${category}`;
}

/** True when the raw list delegates the whole category (`cat:X` present
 * literally — names all ticked one by one is functionally equivalent but
 * renders as individual ticks, not as a "full category"). */
export function categoryDelegated(userParams: string[] | undefined, category: string): boolean {
  return userParams?.includes(categorySelector(category)) ?? false;
}

/** Per-param delegation state in the editor: delegated when its category
 * is delegated wholesale or the name is listed itself. */
export function paramDelegated(
  userParams: string[] | undefined,
  name: string,
  category: string,
): boolean {
  return categoryDelegated(userParams, category) || (userParams?.includes(name) ?? false);
}

/** Next raw list after toggling a whole category. Delegating absorbs the
 * category's individual names (redundant duplicates once `cat:X` is
 * there); revoking only removes the selector — names never sneak back. */
export function toggleCategory(
  userParams: string[] | undefined,
  category: string,
  categoryNames: string[],
  delegate: boolean,
): string[] {
  const selector = categorySelector(category);
  const without = (userParams ?? []).filter((entry) => entry !== selector);
  if (!delegate) return without;
  return [...without.filter((entry) => !categoryNames.includes(entry)), selector];
}

/** Next raw list after toggling one exact name. */
export function toggleName(
  userParams: string[] | undefined,
  name: string,
  delegate: boolean,
): string[] {
  const without = (userParams ?? []).filter((entry) => entry !== name);
  return delegate ? [...without, name] : without;
}
