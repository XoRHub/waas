-- Remote Workspaces: user-registered OUT-OF-CLUSTER machines reachable
-- through guacd (ssh/vnc/rdp). Deliberately a separate entity from
-- provisioned workspaces: no template, no operator, no compute lifecycle.
-- Credentials NEVER land in this table — they live in one Kubernetes
-- Secret per row (secret_name), resolved server-side at connect time.
CREATE TABLE remote_workspaces (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES users (id),
    name TEXT NOT NULL,
    hostname TEXT NOT NULL,
    port INTEGER NOT NULL,
    protocol TEXT NOT NULL,
    -- guacd connection parameters (JSON object), validated against the
    -- platform parameter registry (non-platform tiers only).
    params TEXT NOT NULL DEFAULT '{}',
    -- Name of the Kubernetes Secret holding the credentials.
    secret_name TEXT NOT NULL,
    -- Which credential keys are present in the Secret (JSON array of
    -- "username"/"password"/"private-key"/"passphrase") so the UI can say
    -- "credentials stored" without ever reading the Secret.
    credential_keys TEXT NOT NULL DEFAULT '[]',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (owner_id, name)
);

CREATE INDEX idx_remote_workspaces_owner ON remote_workspaces (owner_id);
