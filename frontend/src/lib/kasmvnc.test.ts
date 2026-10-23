import { describe, expect, it } from 'vitest';
import { coerce, leaves, parseDefaults, removePath, scalarText, setPath, toText } from './kasmvnc';

// An excerpt of kasmweb/terminal:1.19.0's kasmvnc_defaults.yaml
// (KasmVNC 1.5.1), shaped to hit every attachment the yaml lib uses for
// comments and every scalar kind.
const defaults = `---
desktop:
  resolution:
    width: 1024
    height: 768
  allow_resize: true

user_session:
  # session_type: shared
  idle_timeout: never

keyboard:
  remap_keys:
  # - 0x22->0x40
  ignore_numlock: false

# Mouse, trackpad, etc.
pointer:
  enabled: true
  # Force all clients into game mode (direct-drive relative mouse) on connect.
  # Useful for game-focused workspace images where pointer lock is always desired.
  game_mode: false

logging:
  level: 30 # 0 - minimal verbosity, 100 - most verbose

data_loss_prevention:
  clipboard:
    allow_mimetypes:
      - text/html
      - image/png
    server_to_client:
      size: unlimited
`;

describe('parseDefaults', () => {
  const byKey = Object.fromEntries(parseDefaults(defaults).map((o) => [o.key, o]));

  it('flattens nested mappings to dotted leaves with the kind inferred from the default', () => {
    expect(byKey['desktop.resolution.width']).toEqual({
      key: 'desktop.resolution.width',
      kind: 'int',
      default: 1024,
    });
    expect(byKey['desktop.allow_resize']).toMatchObject({ kind: 'bool', default: true });
    expect(byKey['user_session.idle_timeout']).toMatchObject({ kind: 'string', default: 'never' });
    expect(byKey['data_loss_prevention.clipboard.allow_mimetypes']).toMatchObject({
      kind: 'list',
      default: ['text/html', 'image/png'],
    });
    // An empty value is still an option (free text, no default).
    expect(byKey['keyboard.remap_keys']).toMatchObject({ kind: 'string', default: '' });
    // Intermediate mappings are never options.
    expect(byKey['desktop.resolution']).toBeUndefined();
    expect(Object.keys(byKey)).toHaveLength(11);
  });

  it('keeps the comment attached to a key as its hint, wherever the yaml lib hangs it', () => {
    // Before a later key: on the key.
    expect(byKey['pointer.game_mode'].hint).toBe(
      'Force all clients into game mode (direct-drive relative mouse) on connect. Useful for game-focused workspace images where pointer lock is always desired.',
    );
    // Before the FIRST key of a mapping: on the mapping.
    expect(byKey['user_session.idle_timeout'].hint).toBe('session_type: shared');
    // Inline: on the value.
    expect(byKey['logging.level'].hint).toBe('0 - minimal verbosity, 100 - most verbose');
    // A section comment is not a leaf hint.
    expect(byKey['pointer.enabled'].hint).toBeUndefined();
  });

  it('yields no schema for empty, broken or non-mapping text', () => {
    expect(parseDefaults('')).toEqual([]);
    expect(parseDefaults('- a\n- b')).toEqual([]);
    expect(parseDefaults('{a: [')).toEqual([]);
  });
});

describe('coerce / scalarText', () => {
  it('writes the YAML type the key expects', () => {
    expect(coerce('true', 'bool')).toBe(true);
    expect(coerce('false', 'bool')).toBe(false);
    expect(coerce('30', 'int')).toBe(30);
    // Union keys (never|seconds, unlimited|bytes, auto|n): a number is a
    // number whatever the default's type, a word stays a word.
    expect(coerce('900', 'string')).toBe(900);
    expect(coerce('auto', 'int')).toBe('auto');
    expect(coerce('text/html, image/png', 'list')).toEqual(['text/html', 'image/png']);
  });

  it('renders every leaf as input text', () => {
    expect(scalarText(true)).toBe('true');
    expect(scalarText(60)).toBe('60');
    expect(scalarText(['a', 'b'])).toBe('a, b');
    expect(scalarText(null)).toBe('');
  });
});

describe('override tree', () => {
  it('sets and removes dotted keys, pruning emptied parents and keeping siblings', () => {
    const tree = {};
    setPath(tree, 'encoding.rect_encoding_mode.min_quality', 4);
    setPath(tree, 'encoding.rect_encoding_mode.max_quality', 6);
    setPath(tree, 'desktop.allow_resize', false);
    expect(leaves(tree)).toEqual({
      'encoding.rect_encoding_mode.min_quality': 4,
      'encoding.rect_encoding_mode.max_quality': 6,
      'desktop.allow_resize': false,
    });
    removePath(tree, 'encoding.rect_encoding_mode.min_quality');
    expect(tree).toEqual({
      encoding: { rect_encoding_mode: { max_quality: 6 } },
      desktop: { allow_resize: false },
    });
    removePath(tree, 'encoding.rect_encoding_mode.max_quality');
    removePath(tree, 'desktop.allow_resize');
    expect(tree).toEqual({});
    expect(toText(tree)).toBe('');
  });

  it('leaves a nested list override untouched as one leaf', () => {
    const tree = { network: { udp: { peers: [{ host: 'a', port: 1 }] } } };
    setPath(tree, 'network.protocol', 'http');
    expect(leaves(tree)['network.udp.peers']).toEqual([{ host: 'a', port: 1 }]);
    expect(toText(tree)).toContain('- host: a');
  });
});
