-- The KasmVNC server bundled in a kasmweb/* image (version + its own
-- kasmvnc_defaults.yaml), as published by the catalog: the admin template
-- form derives its KasmVNC option list and defaults from it. A JSON blob
-- like recommended (same SQLite type-affinity passthrough), never read by
-- enforcement or the operator. NULL = unknown (manifest without the
-- field), no hint.
ALTER TABLE catalog_entries ADD COLUMN kasmvnc JSONB;
