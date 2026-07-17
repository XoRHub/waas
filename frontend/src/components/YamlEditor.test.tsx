// @vitest-environment jsdom
import { useState } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import '@testing-library/jest-dom/vitest';
import { YamlEditor, computeTabEdit, parseYaml } from './YamlEditor';

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

describe('computeTabEdit', () => {
  it('inserts two spaces at a collapsed caret', () => {
    expect(computeTabEdit('key: value', 4, 4, false)).toEqual({
      replaceStart: 4,
      replaceEnd: 4,
      insert: '  ',
      selStart: 6,
      selEnd: 6,
    });
  });

  it('replaces an intra-line selection with two spaces', () => {
    expect(computeTabEdit('key: value', 5, 10, false)).toEqual({
      replaceStart: 5,
      replaceEnd: 10,
      insert: '  ',
      selStart: 7,
      selEnd: 7,
    });
  });

  it('indents every line touched by a multi-line selection', () => {
    // 'aa\nbb\ncc', selection from inside line 1 to inside line 2.
    expect(computeTabEdit('aa\nbb\ncc', 1, 4, false)).toEqual({
      replaceStart: 0,
      replaceEnd: 5,
      insert: '  aa\n  bb',
      selStart: 3,
      selEnd: 8,
    });
  });

  it('dedents touched lines by up to two spaces each', () => {
    // Mixed leading spaces: 4, 1, 0.
    const value = '    aa\n b\ncc';
    expect(computeTabEdit(value, 5, value.length, true)).toEqual({
      replaceStart: 0,
      replaceEnd: value.length,
      insert: '  aa\nb\ncc',
      selStart: 3,
      selEnd: value.length - 3,
    });
  });

  it('dedents the current line at a collapsed caret', () => {
    expect(computeTabEdit('  key: value', 8, 8, true)).toEqual({
      replaceStart: 0,
      replaceEnd: 12,
      insert: 'key: value',
      selStart: 6,
      selEnd: 6,
    });
  });

  it('returns null when a dedent has nothing to remove', () => {
    expect(computeTabEdit('aa\nbb', 0, 5, true)).toBeNull();
  });
});

function Harness({ initial, readOnly }: { initial: string; readOnly?: boolean }) {
  const [value, setValue] = useState(initial);
  return <YamlEditor value={value} onChange={setValue} readOnly={readOnly} />;
}

describe('YamlEditor Tab handling', () => {
  it('indents with Tab and keeps the caret after the insertion', () => {
    render(<Harness initial="key: value" />);
    const textarea = screen.getByRole('textbox') as HTMLTextAreaElement;
    textarea.setSelectionRange(4, 4);
    fireEvent.keyDown(textarea, { key: 'Tab' });
    expect(textarea).toHaveValue('key:   value');
    expect(textarea.selectionStart).toBe(6);
    expect(textarea.selectionEnd).toBe(6);
  });

  it('indents a multi-line selection line by line', () => {
    render(<Harness initial={'aa\nbb\ncc'} />);
    const textarea = screen.getByRole('textbox') as HTMLTextAreaElement;
    textarea.setSelectionRange(1, 4);
    fireEvent.keyDown(textarea, { key: 'Tab' });
    expect(textarea).toHaveValue('  aa\n  bb\ncc');
    expect(textarea.selectionStart).toBe(3);
    expect(textarea.selectionEnd).toBe(8);
  });

  it('dedents with Shift+Tab', () => {
    render(<Harness initial="  key: value" />);
    const textarea = screen.getByRole('textbox') as HTMLTextAreaElement;
    textarea.setSelectionRange(8, 8);
    fireEvent.keyDown(textarea, { key: 'Tab', shiftKey: true });
    expect(textarea).toHaveValue('key: value');
    expect(textarea.selectionStart).toBe(6);
  });

  it('leaves the value alone when Shift+Tab finds nothing to dedent', () => {
    render(<Harness initial="key: value" />);
    const textarea = screen.getByRole('textbox') as HTMLTextAreaElement;
    textarea.setSelectionRange(0, 0);
    const notPrevented = fireEvent.keyDown(textarea, { key: 'Tab', shiftKey: true });
    // Still swallowed (no focus jump), just no edit.
    expect(notPrevented).toBe(false);
    expect(textarea).toHaveValue('key: value');
  });

  it('keeps native Tab behavior in readOnly and with modifiers', () => {
    render(<Harness initial="key: value" readOnly />);
    const textarea = screen.getByRole('textbox') as HTMLTextAreaElement;
    expect(fireEvent.keyDown(textarea, { key: 'Tab' })).toBe(true);
    expect(textarea).toHaveValue('key: value');
    cleanup();

    render(<Harness initial="key: value" />);
    const editable = screen.getByRole('textbox') as HTMLTextAreaElement;
    expect(fireEvent.keyDown(editable, { key: 'Tab', ctrlKey: true })).toBe(true);
    expect(editable).toHaveValue('key: value');
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
