import { describe, expect, it } from 'vitest';
import { parseYaml } from './YamlEditor';

describe('parseYaml', () => {
  it('returns the JS value for valid YAML', () => {
    const { value, issues } = parseYaml('priority: 100\nimages:\n  - xfce\n');
    expect(issues).toEqual([]);
    expect(value).toEqual({ priority: 100, images: ['xfce'] });
  });

  it('anchors syntax errors to a line', () => {
    const { value, issues } = parseYaml('list:\n  - a\n  - [unclosed\n');
    expect(value).toBeUndefined();
    expect(issues.length).toBeGreaterThan(0);
    // Line-anchored (1-based), never the 0 "document-level" fallback.
    expect(issues[0].line).toBeGreaterThanOrEqual(1);
  });

  it('runs the semantic validator on parsed values', () => {
    const { issues } = parseYaml('priority: "high"\n', (v) => {
      const doc = v as { priority?: unknown };
      return typeof doc.priority === 'number'
        ? []
        : [{ line: 1, message: 'priority must be a number' }];
    });
    expect(issues).toEqual([{ line: 1, message: 'priority must be a number' }]);
  });
});
