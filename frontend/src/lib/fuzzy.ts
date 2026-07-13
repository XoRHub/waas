// Dependency-free fuzzy matching for short in-memory lists (catalog
// entries, not documents): case-insensitive subsequence match with a
// simple relevance score. Deliberately not an npm dependency — the
// need is a few dozen strings, not a search engine.

/**
 * fuzzyScore matches `query` as a case-insensitive subsequence of
 * `text` and returns a relevance score, or null when it does not
 * match. Higher is better: contiguous runs beat scattered characters,
 * and among equal runs an earlier first hit wins (so a match at the
 * start of the string outranks the same match in the middle).
 */
export function fuzzyScore(text: string, query: string): number | null {
  const t = text.toLowerCase();
  const q = query.toLowerCase();
  let score = 0;
  let first = -1;
  // prev = -1 makes a hit at index 0 count as "contiguous", i.e. a
  // start-of-string bonus without a dedicated rule.
  let prev = -1;
  for (const ch of q) {
    const at = t.indexOf(ch, prev + 1);
    if (at === -1) {
      return null;
    }
    score += at === prev + 1 ? 3 : 1;
    if (first === -1) {
      first = at;
    }
    prev = at;
  }
  // Position only tie-breaks: the penalty stays below the smallest
  // score increment however long the text is.
  return score - first / (t.length + 1);
}

/**
 * fuzzyFilter returns the items whose text fuzzy-matches `query`,
 * best score first. An empty (or whitespace) query returns `items`
 * untouched — no filtering, no reordering.
 */
export function fuzzyFilter<T>(items: T[], query: string, getText: (item: T) => string): T[] {
  const q = query.trim();
  if (!q) {
    return items;
  }
  const scored: { item: T; score: number }[] = [];
  for (const item of items) {
    const score = fuzzyScore(getText(item), q);
    if (score !== null) {
      scored.push({ item, score });
    }
  }
  // Array.prototype.sort is stable: equal scores keep catalog order.
  scored.sort((a, b) => b.score - a.score);
  return scored.map((s) => s.item);
}
