// Package account holds the TikTok account aggregate and its persistence shapes.
//
// @file      internal/account/credentials_repository.go
// @for       PostgreSQL reads and writes for the account listing and credential clearing.
// @uses      context, errors, fmt, time, github.com/google/uuid, github.com/jackc/pgx/v5
// @reason    The listing and the revoke write carry the operator scoping rules, so they live apart from the token rotation queries.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-16
package account

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ListOwned loads every account record of one operator in created order, the
// read the console account list renders.
func (r *Repository) ListOwned(ctx context.Context, operatorID string) ([]Record, error) {
	user, err := uuid.Parse(operatorID)
	if err != nil {
		return nil, errAccountNotFound().Wrap(fmt.Errorf("account: parsing operator id: %w", err))
	}
	const q = `SELECT ` + accountColumns + ` FROM tiktok_accounts WHERE user_id = $1 ORDER BY created_at ASC, id ASC`
	rows, err := r.pool.Query(ctx, q, user)
	if err != nil {
		return nil, fmt.Errorf("account: listing owned accounts: %w", err)
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		rec, err := scanAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("account: scanning owned account: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("account: listing owned accounts: %w", err)
	}
	if out == nil {
		out = []Record{}
	}
	return out, nil
}

// ClearCredentials drops the token material and stamps the revoke time in one
// write. The read stays outside: the aggregate refuses a change to a revoked
// account, so the caller checks ownership and state before calling.
func (r *Repository) ClearCredentials(ctx context.Context, accountID string, at time.Time) error {
	id, err := uuid.Parse(accountID)
	if err != nil {
		return errAccountNotFound().Wrap(fmt.Errorf("account: parsing account id: %w", err))
	}
	const q = `UPDATE tiktok_accounts
		SET access_token_enc = ''::bytea, refresh_token_enc = ''::bytea,
		    access_token_expires_at = now(), refresh_expires_at = now(),
		    revoked_at = COALESCE(revoked_at, $2)
		WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q, id, at)
	if err != nil {
		return fmt.Errorf("account: clearing account credentials: %w", err)
	}
	return expectRow(tag)
}
