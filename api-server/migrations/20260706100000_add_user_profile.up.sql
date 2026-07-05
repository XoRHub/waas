-- Self-service profile: display name shown in the portal and a small JSON
-- blob of UI preferences (workspace open target, language). Both owned by
-- the user; OIDC will later take over identity fields, not preferences.
ALTER TABLE users ADD COLUMN display_name TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN preferences TEXT NOT NULL DEFAULT '{}';
