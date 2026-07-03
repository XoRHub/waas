CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users (id),
    workspace_id TEXT NOT NULL,
    workspace_name TEXT NOT NULL,
    protocol TEXT NOT NULL,
    client_ip TEXT,
    started_at TIMESTAMPTZ NOT NULL,
    ended_at TIMESTAMPTZ
);

CREATE INDEX idx_sessions_user_id ON sessions (user_id);
CREATE INDEX idx_sessions_workspace_id ON sessions (workspace_id);
