-- Rollback drop, sign-off required by AGENTS.md 1.7 before merge.
ALTER TABLE tiktok_accounts DROP COLUMN needs_reauth;
