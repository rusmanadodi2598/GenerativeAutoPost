-- Rollback drop, sign-off required by AGENTS.md 1.7 before merge.
DROP TABLE IF EXISTS outbox;
DROP TABLE IF EXISTS webhook_events;
DROP TABLE IF EXISTS publish_chunks;