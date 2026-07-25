-- Lower bound on the issue time (JWT "iat") of acceptable access and
-- stream tokens: the auth middleware rejects any token issued before
-- this instant. Stamped to "now" on logout, deactivation, role change
-- and password change — what makes revocation immediate instead of
-- waiting out the token TTL. NULL (the default, and every pre-migration
-- account) bounds nothing, so the migration itself invalidates no
-- outstanding token.
ALTER TABLE users ADD COLUMN tokens_valid_after TIMESTAMPTZ;
