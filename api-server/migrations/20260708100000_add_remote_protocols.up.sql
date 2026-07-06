-- Multi-protocol remote workspaces: JSON list of endpoints
-- [{name, port, default, params}]. Empty on legacy rows — the repository
-- synthesizes the single legacy protocol/port/params entry on read, so no
-- data backfill is needed. The legacy columns keep mirroring the default
-- entry for compatibility.
ALTER TABLE remote_workspaces ADD COLUMN protocols TEXT NOT NULL DEFAULT '';
