-- Authentik group names, comma-separated. Local mirror of the OIDC
-- "groups" claim: kept in sync at each OIDC login once SSO lands, and
-- editable by admins meanwhile so group-based policies work with local
-- auth too. Named user_groups because GROUPS is reserved in PostgreSQL.
ALTER TABLE users ADD COLUMN user_groups TEXT NOT NULL DEFAULT '';
