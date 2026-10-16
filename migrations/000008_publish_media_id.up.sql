-- Record which media object an attempt posted from. Existing attempts have no
-- recorded media; a hard NOT NULL would reject the migration on any loaded
-- database, so the column stays nullable and reads coalesce to empty.

ALTER TABLE publish_attempts ADD COLUMN media_id uuid;

CREATE INDEX ix_publish_attempts_media_id ON publish_attempts (media_id);