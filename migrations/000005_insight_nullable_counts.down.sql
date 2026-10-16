-- Rollback backfills NULL counts with zero, then restores NOT NULL. Sign-off required by AGENTS.md 1.7 before merge.
UPDATE video_stats SET view_count = 0 WHERE view_count IS NULL;
UPDATE video_stats SET like_count = 0 WHERE like_count IS NULL;
UPDATE video_stats SET comment_count = 0 WHERE comment_count IS NULL;
UPDATE video_stats SET share_count = 0 WHERE share_count IS NULL;
ALTER TABLE video_stats ALTER COLUMN view_count SET NOT NULL;
ALTER TABLE video_stats ALTER COLUMN like_count SET NOT NULL;
ALTER TABLE video_stats ALTER COLUMN comment_count SET NOT NULL;
ALTER TABLE video_stats ALTER COLUMN share_count SET NOT NULL;