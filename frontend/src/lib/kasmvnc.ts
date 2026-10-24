import { isMap, isScalar, isSeq, parseDocument, stringify, type Pair, type YAMLMap } from 'yaml';

/**
 * Schema of the KasmVNC options an image offers, derived from that
 * image's own kasmvnc_defaults.yaml (catalog hint `kasmvnc.defaults`).
 * WaaS keeps no option list of its own: the key set, the control type
 * and the default come from the file, so a new KasmVNC release changes
 * nothing here.
 */
export type OptionKind = 'bool' | 'int' | 'string' | 'list' | 'unknown';

export interface KasmOption {
  /** Dotted key path, e.g. `encoding.max_frame_rate`. */
  key: string;
  /** `unknown`: the file leaves the value null (`watermark:` with its
   * children, `remap_keys:` with its list, only in comments) — a
   * legitimate override root whose shape no control can offer. */
  kind: OptionKind;
  /** The image default (JS value), '' for a key the file leaves empty. */
  default: unknown;
  /** The YAML comment attached to the key, when the file has one. */
  hint?: string;
}

/** A parsed kasmvnc.yaml override: nested plain objects. */
export type Tree = Record<string, unknown>;

/** A comment line that is a commented-out key or list item, not prose. */
const commentedCode = /^([a-z_]+:|- )/;

function commentText(raw: string | null | undefined): string | undefined {
  const text = raw
    ?.split('\n')
    .map((line) => line.trim())
    .filter((line) => line && !commentedCode.test(line))
    .join(' ');
  return text || undefined;
}

/** Leaf names the defaults file only documents as comments
 * (`# session_type: shared`, the watermark block): valid keys for an
 * override, though the schema cannot type them. */
export function commentedKeys(text: string): Set<string> {
  return new Set([...text.matchAll(/^\s*#\s*([a-z_]+):/gm)].map((m) => m[1]));
}

function walk(map: YAMLMap, prefix: string, out: KasmOption[]) {
  map.items.forEach((pair: Pair, index) => {
    if (!isScalar(pair.key)) return;
    const key = prefix + String(pair.key.value);
    const value = pair.value;
    if (isMap(value)) {
      walk(value, key + '.', out);
      return;
    }
    // The yaml lib hangs a comment preceding the FIRST key of a mapping
    // on the mapping itself, later ones on the key, an inline one on the
    // value.
    const hint = commentText(
      pair.key.commentBefore ??
        (index === 0 ? map.commentBefore : undefined) ??
        (isScalar(value) || isSeq(value) ? value.comment : undefined),
    );
    let kind: OptionKind = 'string';
    let def: unknown = '';
    if (isSeq(value)) {
      kind = 'list';
      def = value.toJSON() as unknown[];
    } else if (isScalar(value)) {
      const v = value.value;
      if (v === null) kind = 'unknown';
      else if (typeof v === 'boolean') kind = 'bool';
      else if (typeof v === 'number' && Number.isInteger(v)) kind = 'int';
      def = v ?? '';
    }
    out.push({ key, kind, default: def, ...(hint ? { hint } : {}) });
  });
}

/** Flattens a kasmvnc_defaults.yaml into dotted leaf options (null
 * leaves included, as `unknown`); [] when the text is empty or not a
 * mapping (= no schema known). */
export function parseDefaults(text: string): KasmOption[] {
  const doc = parseDocument(text);
  if (doc.errors.length > 0 || !isMap(doc.contents)) return [];
  const out: KasmOption[] = [];
  walk(doc.contents, '', out);
  return out;
}

/** The text form of a leaf value, for inputs and default labels. */
export function scalarText(value: unknown): string {
  if (value === null || value === undefined) return '';
  if (Array.isArray(value)) return value.map(scalarText).join(', ');
  return String(value);
}

/**
 * Turns input text into the YAML value to write. The kind comes from
 * the image default, but KasmVNC's scalar keys are often unions
 * (`never|<seconds>`, `unlimited|<bytes>`, `auto|<n>`) whose default is
 * the word, so a number is written as a number whatever the default's
 * type; only bools and lists are shaped by the kind.
 */
export function coerce(text: string, kind: OptionKind): unknown {
  if (kind === 'bool') return text === 'true';
  if (kind === 'list')
    return text
      .split(',')
      .map((s) => s.trim())
      .filter(Boolean);
  // No leading zero: `0700` is a mode string, not seven hundred.
  return /^-?(0|[1-9]\d*)$/.test(text) ? Number(text) : text;
}

function isTree(value: unknown): value is Tree {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/** Every leaf of an override tree by dotted key (lists are leaves). */
export function leaves(tree: Tree, prefix = ''): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(tree)) {
    if (isTree(v)) Object.assign(out, leaves(v, prefix + k + '.'));
    else out[prefix + k] = v;
  }
  return out;
}

/** Sets a dotted key in place, creating the path. A scalar sitting on
 * the path is replaced by a mapping — the admin is reshaping that key. */
export function setPath(tree: Tree, key: string, value: unknown) {
  const parts = key.split('.');
  let node = tree;
  for (const part of parts.slice(0, -1)) {
    if (!isTree(node[part])) node[part] = {};
    node = node[part] as Tree;
  }
  node[parts[parts.length - 1]] = value;
}

/** Deletes a dotted key in place, pruning the parents it empties. */
export function removePath(tree: Tree, key: string) {
  const parts = key.split('.');
  const chain: Tree[] = [tree];
  for (const part of parts.slice(0, -1)) {
    const next = chain[chain.length - 1][part];
    if (!isTree(next)) return;
    chain.push(next);
  }
  delete chain[chain.length - 1][parts[parts.length - 1]];
  for (let i = chain.length - 1; i > 0; i--) {
    if (Object.keys(chain[i]).length > 0) break;
    delete chain[i - 1][parts[i - 1]];
  }
}

/** The override text for a tree; an empty tree is the empty field. */
export function toText(tree: Tree): string {
  return Object.keys(tree).length === 0 ? '' : stringify(tree);
}
