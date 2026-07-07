import { describe, expect, it } from 'vitest';
import { protocolRemovalBlock } from './ProtocolTabs';

describe('protocolRemovalBlock', () => {
  it('allows removing a protocol when others remain', () => {
    expect(protocolRemovalBlock({ count: 2 })).toBeNull();
  });

  it('blocks removing the last protocol', () => {
    expect(protocolRemovalBlock({ count: 1 })).toBe('last');
  });

  it('blocks removing a template-locked protocol even among several', () => {
    expect(protocolRemovalBlock({ count: 3, locked: true })).toBe('locked');
  });

  it('locked wins over last (the message must say why)', () => {
    expect(protocolRemovalBlock({ count: 1, locked: true })).toBe('locked');
  });
});
