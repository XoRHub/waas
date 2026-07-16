CREATE TABLE catalog_entries (
    workspace_image_name TEXT NOT NULL,
    image                TEXT NOT NULL,
    os                   TEXT,
    app                  TEXT,
    version              TEXT,
    icon                 TEXT,
    display_name         TEXT,
    synced_at            TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (workspace_image_name, image)
);

CREATE INDEX idx_catalog_entries_workspace_image_name ON catalog_entries (workspace_image_name);
