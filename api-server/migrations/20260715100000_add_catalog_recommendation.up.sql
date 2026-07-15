-- Deployment-prefill hints per catalog entry: a display badge
-- (profile) and a JSON blob (recommended) the admin template form can
-- copy into a WorkspaceTemplate's Workload on explicit request. Never
-- read by enforcement, never merged into a built pod. JSONB is a type
-- name SQLite has no native support for, but its type-affinity rules
-- (no INT/CHAR/CLOB/TEXT/BLOB/REAL/FLOA/DOUB substring) fall back to
-- NUMERIC affinity, which leaves any non-numeric-looking TEXT (every
-- value here starts with "{") stored as-is — so this column round-trips
-- unchanged on SQLite too, while getting real validation/binary storage
-- on Postgres.
ALTER TABLE catalog_entries ADD COLUMN profile TEXT;
ALTER TABLE catalog_entries ADD COLUMN recommended JSONB;
