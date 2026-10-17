// Package operator holds the operator identity row used by Google sign in.
//
// @file      internal/operator/repository.go
// @for       PostgreSQL persistence for the users row of the operator.
// @uses      context, fmt, github.com/google/uuid, github.com/jackc/pgx/v5/pgxpool
// @reason    Every login must ensure the users row before an account row can reference it.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-17
package operator

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository stores operator identity rows in the users table.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository wires the operator store over the shared pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Ensure writes the operator row for the signed in id and moves the email on a
// repeat login. The unique email index still rejects a second id claiming the
// same address, which is intended.
func (r *Repository) Ensure(ctx context.Context, id string, email string) error {
	operatorID, err := uuid.Parse(id)
	if err != nil {
		return fmt.Errorf("operator: parsing operator id: %w", err)
	}
	const q = `INSERT INTO users (id, email) VALUES ($1, $2)
		ON CONFLICT (id) DO UPDATE SET email = EXCLUDED.email`
	if _, err := r.pool.Exec(ctx, q, operatorID, email); err != nil {
		return fmt.Errorf("operator: ensuring operator row: %w", err)
	}
	return nil
}
