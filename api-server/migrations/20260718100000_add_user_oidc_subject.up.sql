-- The IdP's stable subject identifier (OIDC "sub" claim). SSO logins
-- resolve the account by this value first; the username claim is only a
-- display/provisioning hint. Empty = local-only account (or an SSO
-- account provisioned before this column existed, linked at next login).
ALTER TABLE users ADD COLUMN oidc_subject TEXT NOT NULL DEFAULT '';

-- One platform account per IdP identity. Partial so the empty default
-- (every local account) never collides.
CREATE UNIQUE INDEX users_oidc_subject_unique ON users (oidc_subject)
WHERE oidc_subject != '';
