// Package postgres runs numbered SQL migrations and journals each one.
//
// @file      internal/platform/postgres/migrate.go
// @for       Transactional up/down migration runner with a journal table.
// @uses      github.com/jackc/pgx/v5/pgxpool, migrations/
// @reason    Schema changes must be ordered, idempotent, and reversible.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-12
package postgres

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const journalTable = "schema_migrations"

// migrationLockKey is one fixed advisory key shared by every runner. Two
// replicas booting at once would otherwise both read "not applied" and both
// apply the same file.
const migrationLockKey int64 = 0x6D6967726174

// migrationLockTimeout bounds the wait for the advisory lock. A stuck holder
// would otherwise stall a booting replica until its context expires, and a
// blocked pg_advisory_xact_lock ignores context cancellation in the server.
const migrationLockTimeout = 10 * time.Second

// Up applies every pending migration in dir, oldest filename first. Running it
// against an already-applied directory is a no-op.
func Up(ctx context.Context, pool *pgxpool.Pool, dir string) error {
	return up(ctx, pool, dir, migrationLockTimeout)
}

// up applies pending migrations with an explicit lock wait bound, so a test
// can exercise the timeout without holding a connection for the full default.
func up(ctx context.Context, pool *pgxpool.Pool, dir string, lockTimeout time.Duration) error {
	files, err := migrationFiles(dir, "up")
	if err != nil {
		return err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockMigrations(ctx, tx, lockTimeout); err != nil {
		return err
	}
	if err := ensureJournal(ctx, tx); err != nil {
		return err
	}
	applied, err := journalVersions(ctx, tx)
	if err != nil {
		return err
	}
	done := make(map[string]bool, len(applied))
	for _, version := range applied {
		done[version] = true
	}
	for _, version := range sortedVersions(files) {
		if done[version] {
			continue
		}
		if err := applyFile(ctx, tx, filepath.Join(dir, files[version]), version, false); err != nil {
			return fmt.Errorf("postgres: apply %s: %w", version, err)
		}
	}
	return tx.Commit(ctx)
}

// Down rolls back the most recently applied migrations, newest first. A
// non-positive step count does nothing.
func Down(ctx context.Context, pool *pgxpool.Pool, dir string, steps int) error {
	return down(ctx, pool, dir, steps, migrationLockTimeout)
}

// down rolls back migrations under the same bounded lock wait as up.
func down(ctx context.Context, pool *pgxpool.Pool, dir string, steps int, lockTimeout time.Duration) error {
	if steps <= 0 {
		return nil
	}
	files, err := migrationFiles(dir, "down")
	if err != nil {
		return err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockMigrations(ctx, tx, lockTimeout); err != nil {
		return err
	}
	if err := ensureJournal(ctx, tx); err != nil {
		return err
	}
	applied, err := journalVersions(ctx, tx)
	if err != nil {
		return err
	}
	for i := len(applied) - 1; i >= 0 && steps > 0; i-- {
		version := applied[i]
		name, ok := files[version]
		if !ok {
			return fmt.Errorf("postgres: no down migration for %s", version)
		}
		if err := applyFile(ctx, tx, filepath.Join(dir, name), version, true); err != nil {
			return fmt.Errorf("postgres: revert %s: %w", version, err)
		}
		steps--
	}
	return tx.Commit(ctx)
}

// lockMigrations serializes whole runs behind one transaction-scoped advisory
// key, so the journal read and the DDL apply cannot interleave. The lock wait
// is bounded by lock_timeout, which the server applies to advisory locks too.
func lockMigrations(ctx context.Context, tx pgx.Tx, timeout time.Duration) error {
	if _, err := tx.Exec(ctx, "SELECT set_config('lock_timeout', $1, true)", timeout.String()); err != nil {
		return fmt.Errorf("postgres: set migration lock timeout: %w", err)
	}
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrationLockKey); err != nil {
		return fmt.Errorf("postgres: acquire migration lock: %w", err)
	}
	// Restore the default so the DDL that follows is not bounded by the lock
	// wait bound.
	if _, err := tx.Exec(ctx, "SELECT set_config('lock_timeout', '0', true)"); err != nil {
		return fmt.Errorf("postgres: reset lock timeout: %w", err)
	}
	return nil
}

// ensureJournal creates the journal table once per database.
func ensureJournal(ctx context.Context, tx pgx.Tx) error {
	sql := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		version    text PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`, journalTable)
	if _, err := tx.Exec(ctx, sql); err != nil {
		return fmt.Errorf("postgres: create journal: %w", err)
	}
	return nil
}

// journalVersions returns every journaled version, ordered oldest first.
func journalVersions(ctx context.Context, tx pgx.Tx) ([]string, error) {
	query := "SELECT version FROM " + journalTable + " ORDER BY version ASC"
	rows, err := tx.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("postgres: read journal: %w", err)
	}
	defer rows.Close()

	var versions []string
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("postgres: scan journal: %w", err)
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterate journal: %w", err)
	}
	return versions, nil
}

// applyFile runs one migration file and its journal change inside the caller's
// transaction.
func applyFile(ctx context.Context, tx pgx.Tx, path, version string, down bool) error {
	statements, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}
	if _, err := tx.Exec(ctx, string(statements)); err != nil {
		return fmt.Errorf("run SQL: %w", err)
	}
	if down {
		_, err = tx.Exec(ctx, "DELETE FROM "+journalTable+" WHERE version = $1", version)
	} else {
		_, err = tx.Exec(ctx, "INSERT INTO "+journalTable+" (version) VALUES ($1)", version)
	}
	if err != nil {
		return fmt.Errorf("write journal: %w", err)
	}
	return nil
}
