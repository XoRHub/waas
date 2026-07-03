-- Audit trail. Append-only by contract: the application never issues
-- UPDATE or DELETE against this table.
CREATE TABLE audit_logs (
    id TEXT PRIMARY KEY,
    occurred_at TIMESTAMPTZ NOT NULL,
    actor_id TEXT,
    actor_username TEXT,
    action TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id TEXT,
    detail TEXT,
    client_ip TEXT
);

CREATE INDEX idx_audit_logs_occurred_at ON audit_logs (occurred_at);
CREATE INDEX idx_audit_logs_actor_id ON audit_logs (actor_id);
