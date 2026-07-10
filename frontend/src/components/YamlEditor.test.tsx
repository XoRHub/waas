// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import '@testing-library/jest-dom/vitest';
import { YamlEditor, parseYaml } from './YamlEditor';

afterEach(cleanup);

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

describe('YamlEditor readOnly', () => {
  it('sets the readOnly attribute and keeps the gutter and highlighting', () => {
    render(<YamlEditor value={'alpha: x\nbeta: y'} onChange={() => {}} readOnly />);
    expect(screen.getByRole('textbox')).toHaveAttribute('readonly');
    // Gutter still numbers both lines and the mirror still splits keys
    // into highlighted spans.
    expect(screen.getByText('2')).toBeInTheDocument();
    expect(screen.getByText('beta')).toBeInTheDocument();
  });

  it('blocks typing without touching the editable path', async () => {
    const onChange = vi.fn();
    render(<YamlEditor value="alpha: x" onChange={onChange} readOnly />);
    await userEvent.type(screen.getByRole('textbox'), 'z');
    expect(onChange).not.toHaveBeenCalled();
    cleanup();

    render(<YamlEditor value="" onChange={onChange} />);
    await userEvent.type(screen.getByRole('textbox'), 'z');
    expect(onChange).toHaveBeenCalledWith('z');
  });

  it('hides the issues panel in readOnly even on syntax errors', () => {
    render(<YamlEditor value={'list:\n  - [unclosed'} onChange={() => {}} readOnly />);
    expect(screen.queryByRole('list')).toBeNull();
  });
});
