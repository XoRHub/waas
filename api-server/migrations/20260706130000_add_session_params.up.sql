-- Connect-time guacd parameter overrides chosen by the user (JSON object,
-- validated against the template's userParams allow-list at connect time).
ALTER TABLE sessions ADD COLUMN params TEXT NOT NULL DEFAULT '{}';
