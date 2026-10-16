-- Cache the creator profile fields beside the privacy options, so one snapshot
-- answers the whole creator-info contract.

ALTER TABLE creator_info_snapshots
    ADD COLUMN creator_username  text NOT NULL DEFAULT '',
    ADD COLUMN creator_nickname  text NOT NULL DEFAULT '',
    ADD COLUMN creator_avatar_url text NOT NULL DEFAULT '';