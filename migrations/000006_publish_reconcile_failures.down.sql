-- Rollback for the reconcile failure counter.
ALTER TABLE publish_attempts DROP COLUMN reconcile_failures;
