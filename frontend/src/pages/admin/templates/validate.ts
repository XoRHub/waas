import i18n from '@/i18n';
import type { YamlIssue } from '@/components/YamlEditor';

// Semantic validation of the workload YAML: must be a mapping, and kind
// (when present) must be one the CR accepts.
export function validateWorkload(value: unknown): YamlIssue[] {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    return [{ line: 0, message: 'workload must be a YAML mapping' }];
  }
  const kind = (value as Record<string, unknown>).kind;
  if (kind !== undefined && !['Deployment', 'StatefulSet', 'Pod'].includes(String(kind))) {
    return [
      { line: 0, message: `kind: must be Deployment, StatefulSet or Pod (got "${String(kind)}")` },
    ];
  }
  return [];
}

// Shape-only check for kasmvncConfig: a mapping (or empty). The
// clipboard-DLP key rejection stays webhook-only — its error message is
// already explicit, no client-side duplicate.
export function validateKasmVNCConfig(value: unknown): YamlIssue[] {
  if (value === undefined || value === null) return [];
  if (typeof value !== 'object' || Array.isArray(value)) {
    return [{ line: 0, message: i18n.t('admin.templatesPage.kasmvncConfigMustBeMapping') }];
  }
  return [];
}
