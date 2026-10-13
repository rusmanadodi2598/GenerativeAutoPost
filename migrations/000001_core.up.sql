-- Core identity and account tables (001 section 5).
-- Primary keys are uuid with no server default; the application supplies UUIDv7.

CREATE TABLE users (
    id            uuid PRIMARY KEY,
    email         text NOT NULL,
    password_hash text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX ux_users_email ON users (email);

CREATE TABLE sessions (
    id         uuid PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    csrf_token text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX ix_sessions_user_id ON sessions (user_id);

CREATE TABLE tiktok_accounts (
    id                      uuid PRIMARY KEY,
    user_id                 uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    open_id                 text NOT NULL,
    union_id                text,
    display_name            text,
    avatar_url              text,
    scopes                  text[] NOT NULL DEFAULT '{}',
    access_token_enc        bytea NOT NULL,
    access_token_expires_at timestamptz NOT NULL,
    refresh_token_enc       bytea NOT NULL,
    refresh_expires_at      timestamptz NOT NULL,
    token_key_id            text NOT NULL,
    revoked_at              timestamptz,
    created_at              timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX ux_tiktok_accounts_open_id ON tiktok_accounts (open_id);
CREATE INDEX ix_tiktok_accounts_user_id ON tiktok_accounts (user_id);

CREATE TABLE publish_attempts (
    id             uuid PRIMARY KEY,
    account_id     uuid NOT NULL REFERENCES tiktok_accounts (id) ON DELETE CASCADE,
    publish_id     text NOT NULL,
    post_mode      text NOT NULL CHECK (post_mode IN ('DIRECT_POST', 'MEDIA_UPLOAD')),
    media_type     text NOT NULL CHECK (media_type IN ('VIDEO', 'PHOTO')),
    source         text NOT NULL CHECK (source IN ('FILE_UPLOAD', 'PULL_FROM_URL')),
    status         text NOT NULL CHECK (status IN (
                       'CREATED', 'PROCESSING_UPLOAD', 'PROCESSING_DOWNLOAD',
                       'SEND_TO_USER_INBOX', 'PUBLISH_COMPLETE', 'FAILED')),
    privacy_level  text,
    title          text,
    description    text,
    fail_reason    text,
    post_id        text,
    uploaded_bytes bigint NOT NULL DEFAULT 0,
    created_at     timestamptz NOT NULL DEFAULT now(),
    completed_at   timestamptz
);

CREATE UNIQUE INDEX ux_publish_attempts_publish_id ON publish_attempts (publish_id);
CREATE INDEX ix_publish_attempts_account_status ON publish_attempts (account_id, status);