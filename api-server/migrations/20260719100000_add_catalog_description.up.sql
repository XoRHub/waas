-- Full per-image description from the catalog manifest: a prefill hint
-- for the admin template form's description field, never read by
-- enforcement or the operator. NULL = unknown (manifest predates the
-- field), no hint.
ALTER TABLE catalog_entries ADD COLUMN description TEXT;
