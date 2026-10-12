// Package postgres opens a bounded pgx pool from typed config.
//
// @file      internal/platform/postgres/postgres.go
// @for       PostgreSQL pool with explicit limits from config.
// @uses      github.com/jackc/pgx/v5/pgxpool, internal/config
// @reason    One place owns pool sizing per AGENTS.md 1.7.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-12
package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/config"
)

// Open parses the DSN and applies the explicit pool limits from config.
func Open(ctx context.Context, cfg config.Config) (*pgxpool.Pool, error) {
	parsed, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse DSN: %w", err)
	}
	if cfg.DBMaxOpenConns > 0 {
		parsed.MaxConns = cfg.DBMaxOpenConns
	}
	if cfg.DBMaxIdleConns >= 0 {
		parsed.MinConns = cfg.DBMaxIdleConns
	}
	if cfg.DBConnMaxLifetime > 0 {
		parsed.MaxConnLifetime = cfg.DBConnMaxLifetime
	} else {
		parsed.MaxConnLifetime = 5 * time.Minute
	}
	pool, err := pgxpool.NewWithConfig(ctx, parsed)
	if err != nil {
		return nil, fmt.Errorf("postgres: open pool: %w", err)
	}
	return pool, nil
}
