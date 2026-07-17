-- Per-image architectures from the catalog manifest (JSON list, e.g.
-- ["amd64"]): a prefill hint for the admin template form's nodeSelector
-- (kubernetes.io/arch), never read by enforcement or the operator's
-- entry-level archAffinity. NULL = unknown (manifest predates the
-- field), no hint.
ALTER TABLE catalog_entries ADD COLUMN architectures TEXT;
