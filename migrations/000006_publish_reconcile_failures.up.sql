-- Reconcile failure counter so the polling fallback reaches its retry cap (002 section 6).
ALTER TABLE publish_attempts ADD COLUMN reconcile_failures integer NOT NULL DEFAULT 0;
