import { describe, expect, it } from 'vitest';
import {
  categoryDelegated,
  categorySelector,
  paramDelegated,
  toggleCategory,
  toggleName,
} from './userParams';

// The template editor works on the RAW userParams list (cat: selectors
// intact) — these helpers are its whole delegation logic.
describe('userParams editor helpers', () => {
  it('categorySelector mirrors the Go cat: prefix', () => {
    expect(categorySelector('audio')).toBe('cat:audio');
  });

  it('a category renders as "full" only when its selector is literally present', () => {
    expect(categoryDelegated(['cat:audio', 'color-depth'], 'audio')).toBe(true);
    expect(categoryDelegated(['color-depth'], 'audio')).toBe(false);
    // Every name of the category ticked one by one is functionally
    // equivalent but does NOT render as "full category" — that state is
    // reserved to the literal selector (dynamic future coverage).
    expect(categoryDelegated(['enable-audio', 'audio-servername'], 'audio')).toBe(false);
    expect(categoryDelegated(undefined, 'audio')).toBe(false);
  });

  it('paramDelegated: via its own name or via the whole category', () => {
    expect(paramDelegated(['cat:audio'], 'enable-audio', 'audio')).toBe(true);
    expect(paramDelegated(['enable-audio'], 'enable-audio', 'audio')).toBe(true);
    // Partial selection: unlisted names stay visible-but-locked in the
    // editor (the greyed rendering), never hidden.
    expect(paramDelegated(['enable-audio'], 'audio-servername', 'audio')).toBe(false);
  });

  it('delegating a category absorbs its now-redundant individual names', () => {
    expect(
      toggleCategory(['enable-audio', 'color-depth'], 'audio', ['enable-audio', 'audio-servername'], true),
    ).toEqual(['color-depth', 'cat:audio']);
  });

  it('revoking a category removes only the selector', () => {
    expect(
      toggleCategory(['cat:audio', 'color-depth'], 'audio', ['enable-audio', 'audio-servername'], false),
    ).toEqual(['color-depth']);
  });

  it('toggleName adds and removes exact names without touching selectors', () => {
    expect(toggleName(['cat:audio'], 'color-depth', true)).toEqual(['cat:audio', 'color-depth']);
    expect(toggleName(['cat:audio', 'color-depth'], 'color-depth', false)).toEqual(['cat:audio']);
    expect(toggleName(undefined, 'color-depth', true)).toEqual(['color-depth']);
  });
});
