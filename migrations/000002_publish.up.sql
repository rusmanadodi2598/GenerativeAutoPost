-- Publish transfer bookkeeping, webhook idempotency, and the outbox (001 section 5).

CREATE TABLE publish_chunks (
    id                 uuid PRIMARY KEY,
    publish_attempt_id uuid NOT NULL REFERENCES publish_attempts (id) ON DELETE CASCADE,
    chunk_index        integer NOT NULL,
    byte_start         bigint NOT NULL,
    byte_end           bigint NOT NULL,
    content_range      text NOT NULL,
    uploaded_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (publish_attempt_id, chunk_index)
);

CREATE TABLE webhook_events (
    id           uuid PRIMARY KEY,
    dedupe_key   text NOT NULL,
    event_type   text NOT NULL,
    publish_id   text,
    post_id      text,
    payload      jsonb NOT NULL,
    received_at  timestamptz NOT NULL DEFAULT now(),
    processed_at timestamptz
);

CREATE UNIQUE INDEX ux_webhook_events_dedupe_key ON webhook_events (dedupe_key);
CREATE INDEX ix_webhook_events_unprocessed ON webhook_events (received_at)
    WHERE processed_at IS NULL;

CREATE TABLE outbox (
    id             uuid PRIMARY KEY,
    aggregate_type text NOT NULL,
    aggregate_id   uuid NOT NULL,
    event_type     text NOT NULL,
    payload        jsonb NOT NULL,
    published_at   timestamptz
);

CREATE INDEX ix_outbox_unpublished ON outbox (id) WHERE published_at IS NULL;