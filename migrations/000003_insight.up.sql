-- Insight time-series and cached creator info (001 section 5).

CREATE TABLE video_stats (
    id            uuid PRIMARY KEY,
    video_id      text NOT NULL,
    view_count    bigint NOT NULL DEFAULT 0,
    like_count    bigint NOT NULL DEFAULT 0,
    comment_count bigint NOT NULL DEFAULT 0,
    share_count   bigint NOT NULL DEFAULT 0,
    fetched_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX ix_video_stats_video_fetched ON video_stats (video_id, fetched_at DESC);

CREATE TABLE creator_info_snapshots (
    id                           uuid PRIMARY KEY,
    account_id                   uuid NOT NULL REFERENCES tiktok_accounts (id) ON DELETE CASCADE,
    privacy_level_options        text[] NOT NULL DEFAULT '{}',
    comment_disabled             boolean NOT NULL DEFAULT false,
    duet_disabled                boolean NOT NULL DEFAULT false,
    stitch_disabled              boolean NOT NULL DEFAULT false,
    max_video_post_duration_sec  integer NOT NULL DEFAULT 0,
    fetched_at                   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX ix_creator_info_snapshots_account_fetched
    ON creator_info_snapshots (account_id, fetched_at DESC);