import { describe, expect, it } from 'vitest';
import { addGroup, toggleGroup } from './GroupsPicker';

// The selection logic shared by user creation AND edition (the edit
// dialog had regressed to a comma-separated text field).
describe('GroupsPicker helpers', () => {
  it('toggles a group in and out', () => {
    expect(toggleGroup([], 'dev')).toEqual(['dev']);
    expect(toggleGroup(['dev', 'ops'], 'dev')).toEqual(['ops']);
  });
  it('adds trimmed custom groups, deduplicated', () => {
    expect(addGroup(['dev'], '  ops ')).toEqual(['dev', 'ops']);
    expect(addGroup(['dev'], 'dev')).toEqual(['dev']);
    expect(addGroup(['dev'], '   ')).toEqual(['dev']);
  });
  it('pre-filled edition keeps untouched groups intact', () => {
    // Edition state = the user's full current list; a toggle only
    // affects the clicked group (no silent overwrite of the rest).
    const current = ['dev', 'ops', 'sec'];
    expect(toggleGroup(current, 'ops')).toEqual(['dev', 'sec']);
  });
});
