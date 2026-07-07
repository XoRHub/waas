import { describe, expect, it } from 'vitest';
import { ClipboardSync } from './clipboard';

describe('ClipboardSync', () => {
  it('sends fresh local text', () => {
    const sync = new ClipboardSync();
    expect(sync.shouldSend('hello')).toBe(true);
  });

  it('never sends empty text', () => {
    const sync = new ClipboardSync();
    expect(sync.shouldSend('')).toBe(false);
  });

  it('deduplicates an unchanged local clipboard (focus polling)', () => {
    const sync = new ClipboardSync();
    sync.sent('hello');
    expect(sync.shouldSend('hello')).toBe(false);
    expect(sync.shouldSend('world')).toBe(true);
  });

  it('does not echo back what the desktop just sent', () => {
    // remote copy → navigator.clipboard.writeText → window focus →
    // readText returns the same text: sending it back would loop.
    const sync = new ClipboardSync();
    sync.receive('from-desktop');
    expect(sync.shouldSend('from-desktop')).toBe(false);
  });

  it('exposes the last received text for the manual fallback', () => {
    const sync = new ClipboardSync();
    sync.receive('a');
    sync.receive('b');
    expect(sync.lastReceived).toBe('b');
  });

  it('allows sending edited text after a receive', () => {
    const sync = new ClipboardSync();
    sync.receive('from-desktop');
    expect(sync.shouldSend('from-desktop edited')).toBe(true);
  });
});
