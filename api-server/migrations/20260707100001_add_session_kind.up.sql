-- Sessions may now target either a provisioned workspace or a remote
-- workspace; workspace_id points into the corresponding store.
ALTER TABLE sessions ADD COLUMN kind TEXT NOT NULL DEFAULT 'workspace';
