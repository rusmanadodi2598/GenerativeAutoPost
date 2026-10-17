-- Restore the password_hash column for rollback; the Google-only operator leaves it empty.

ALTER TABLE users ADD COLUMN password_hash text NOT NULL DEFAULT '';
