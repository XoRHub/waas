import { describe, expect, it } from 'vitest';
import { canOverrideField } from './overrides';

// The template ∩ policy gate every override control greys out with —
// it must mirror the webhook: template allows AND policy does not
// restrict away; admins bypass both.
describe('canOverrideField', () => {
  it('requires the template to allow the field', () => {
    expect(canOverrideField('env', { isAdmin: false, templateAllows: [] })).toBe(false);
    expect(canOverrideField('env', { isAdmin: false })).toBe(false);
    expect(canOverrideField('env', { isAdmin: false, templateAllows: ['env'] })).toBe(true);
  });

  it('intersects with the policy allow-list when one is set', () => {
    expect(
      canOverrideField('env', {
        isAdmin: false,
        templateAllows: ['env', 'resources'],
        policyAllows: ['resources'],
      }),
    ).toBe(false);
    expect(
      canOverrideField('resources', {
        isAdmin: false,
        templateAllows: ['env', 'resources'],
        policyAllows: ['resources'],
      }),
    ).toBe(true);
  });

  it('treats an absent policy list as unrestricted', () => {
    expect(
      canOverrideField('nodeSelector', { isAdmin: false, templateAllows: ['nodeSelector'] }),
    ).toBe(true);
  });

  it('lets admins bypass both gates', () => {
    expect(canOverrideField('tolerations', { isAdmin: true })).toBe(true);
    expect(canOverrideField('tolerations', { isAdmin: true, policyAllows: [] })).toBe(true);
  });
});
