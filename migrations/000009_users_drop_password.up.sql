-- The Google-only operator signs in via OIDC, so users no longer store a password hash.

ALTER TABLE users DROP COLUMN password_hash;
