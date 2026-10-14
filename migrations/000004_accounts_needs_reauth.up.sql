-- Re-auth flag for the TikTok account aggregate (Task 7). Rows start clear.
ALTER TABLE tiktok_accounts ADD COLUMN needs_reauth boolean NOT NULL DEFAULT false;
