-- Video counts become nullable so a missing count stays missing instead of becoming zero (002 section 7).
ALTER TABLE video_stats ALTER COLUMN view_count DROP NOT NULL;
ALTER TABLE video_stats ALTER COLUMN like_count DROP NOT NULL;
ALTER TABLE video_stats ALTER COLUMN comment_count DROP NOT NULL;
ALTER TABLE video_stats ALTER COLUMN share_count DROP NOT NULL;