/**
 * canOverrideField mirrors the webhook's override gate for DISPLAY:
 * a field is overridable when the template allows it AND the policy
 * does not restrict it away (admins bypass both, like the webhook).
 * Shared by the creation dialog and the runtime settings tab so the
 * two surfaces can never grey out differently; enforcement stays
 * server-side (operator/pkg/policy.CheckOverrides).
 */
export function canOverrideField(
  field: string,
  opts: {
    isAdmin: boolean;
    /** Template-level allow-list (WorkspaceTemplate.allowedOverrides). */
    templateAllows?: string[];
    /** Policy-level allow-list (QuotaStatus.allowedOverrides); undefined
     *  = the policy does not restrict overrides. */
    policyAllows?: string[];
  },
): boolean {
  if (opts.isAdmin) return true;
  const policyOK = !opts.policyAllows || opts.policyAllows.includes(field);
  return (opts.templateAllows?.includes(field) ?? false) && policyOK;
}
