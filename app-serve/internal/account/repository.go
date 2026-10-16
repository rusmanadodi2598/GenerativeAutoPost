// Package account holds the TikTok account aggregate and its persistence shapes.
//
// @file      internal/account/repository.go
// @for       PostgreSQL persistence for TikTok account rows.
// @uses      context, time, github.com/jackc/pgx/v5, github.com/google/uuid, internal/apierror
// @reason    Keeps ownership checks and token writes in one SQL layer.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-12
package account

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// accountColumns is the single column list every read and the RETURNING clause
// share, so the scan order in scanAccount cannot drift from the projection.
const accountColumns = `id, user_id, open_id, COALESCE(union_id, ''), COALESCE(display_name, ''), ` +
	`COALESCE(avatar_url, ''), scopes, access_token_enc, access_token_expires_at, ` +
	`refresh_token_enc, refresh_expires_at, token_key_id, needs_reauth, revoked_at, created_at`

// Repository stores accounts in the tiktok_accounts table.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository wires the account store over the shared pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// errAccountNotFound is the deny by default answer for an unknown or foreign
// account: the two cases are indistinguishable on purpose.
func errAccountNotFound() *apierror.Error {
	return apierror.New(apierror.CodeNotFound, 0, "Account not found.")
}

// scanAccount reads one row in accountColumns order.
func scanAccount(row pgx.Row) (Record, error) {
	var (
		rec  Record
		id   uuid.UUID
		user uuid.UUID
	)
	if err := row.Scan(&id, &user, &rec.OpenID, &rec.UnionID, &rec.DisplayName, &rec.AvatarURL,
		&rec.Scopes, &rec.AccessEnc, &rec.AccessExpiry, &rec.RefreshEnc, &rec.RefreshExpiry,
		&rec.KeyID, &rec.NeedsReauth, &rec.RevokedAt, &rec.CreatedAt); err != nil {
		return Record{}, err
	}
	rec.ID = id.String()
	rec.OperatorID = user.String()
	return rec, nil
}

// UpsertEncrypted writes the token material keyed by open id. On conflict only
// the token, key id, expiry, scope, and profile columns move: created_at,
// revoked_at, needs_reauth, and the owning user_id are left alone, so a repeat
// OAuth callback never silently un-revokes or re-homes an account.
func (r *Repository) UpsertEncrypted(ctx context.Context, rec Record) (Record, error) {
	user, err := uuid.Parse(rec.OperatorID)
	if err != nil {
		return Record{}, fmt.Errorf("account: parsing operator id: %w", err)
	}
	id, err := uuid.Parse(rec.ID)
	if err != nil {
		return Record{}, fmt.Errorf("account: parsing account id: %w", err)
	}
	const q = `INSERT INTO tiktok_accounts
		(id, user_id, open_id, union_id, display_name, avatar_url, scopes,
		 access_token_enc, access_token_expires_at, refresh_token_enc, refresh_expires_at,
		 token_key_id, created_at)
		VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, ''), $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (open_id) DO UPDATE SET
			union_id = EXCLUDED.union_id,
			display_name = EXCLUDED.display_name,
			avatar_url = EXCLUDED.avatar_url,
			scopes = EXCLUDED.scopes,
			access_token_enc = EXCLUDED.access_token_enc,
			access_token_expires_at = EXCLUDED.access_token_expires_at,
			refresh_token_enc = EXCLUDED.refresh_token_enc,
			refresh_expires_at = EXCLUDED.refresh_expires_at,
			token_key_id = EXCLUDED.token_key_id
		RETURNING ` + accountColumns
	scopes := rec.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	created := rec.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	row := r.pool.QueryRow(ctx, q, id, user, rec.OpenID, rec.UnionID, rec.DisplayName, rec.AvatarURL,
		scopes, rec.AccessEnc, rec.AccessExpiry, rec.RefreshEnc, rec.RefreshExpiry, rec.KeyID, created)
	stored, err := scanAccount(row)
	if err != nil {
		return Record{}, fmt.Errorf("account: upserting account: %w", err)
	}
	return stored, nil
}

// GetOwned loads one account for its operator. A missing row and a row owned by
// someone else both come back as not found, so ownership is never probed.
func (r *Repository) GetOwned(ctx context.Context, operatorID string, accountID string) (Record, error) {
	user, err := uuid.Parse(operatorID)
	if err != nil {
		return Record{}, errAccountNotFound().Wrap(fmt.Errorf("account: parsing operator id: %w", err))
	}
	id, err := uuid.Parse(accountID)
	if err != nil {
		return Record{}, errAccountNotFound().Wrap(fmt.Errorf("account: parsing account id: %w", err))
	}
	const q = `SELECT ` + accountColumns + ` FROM tiktok_accounts WHERE id = $1 AND user_id = $2`
	rec, err := scanAccount(r.pool.QueryRow(ctx, q, id, user))
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, errAccountNotFound()
	}
	if err != nil {
		return Record{}, fmt.Errorf("account: loading owned account: %w", err)
	}
	return rec, nil
}

// GetSystem loads one account by id with no operator filter. It exists for the
// background reconcile sweep, which has no operator session; handlers keep
// using GetOwned.
func (r *Repository) GetSystem(ctx context.Context, accountID string) (Record, error) {
	id, err := uuid.Parse(accountID)
	if err != nil {
		return Record{}, errAccountNotFound().Wrap(fmt.Errorf("account: parsing account id: %w", err))
	}
	const q = `SELECT ` + accountColumns + ` FROM tiktok_accounts WHERE id = $1`
	rec, err := scanAccount(r.pool.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, errAccountNotFound()
	}
	if err != nil {
		return Record{}, fmt.Errorf("account: loading account for system read: %w", err)
	}
	return rec, nil
}

// UpdateTokens rotates the stored ciphertext, key id, and expiries in one write.
func (r *Repository) UpdateTokens(ctx context.Context, accountID string, accessEnc, refreshEnc []byte, keyID string, accessExp, refreshExp time.Time) error {
	id, err := uuid.Parse(accountID)
	if err != nil {
		return errAccountNotFound().Wrap(fmt.Errorf("account: parsing account id: %w", err))
	}
	const q = `UPDATE tiktok_accounts
		SET access_token_enc = $2, refresh_token_enc = $3, token_key_id = $4,
		    access_token_expires_at = $5, refresh_expires_at = $6
		WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q, id, accessEnc, refreshEnc, keyID, accessExp, refreshExp)
	if err != nil {
		return fmt.Errorf("account: rotating tokens: %w", err)
	}
	return expectRow(tag)
}

// SetNeedsReauth sets the re-login flag the UI reads.
func (r *Repository) SetNeedsReauth(ctx context.Context, accountID string, needs bool) error {
	id, err := uuid.Parse(accountID)
	if err != nil {
		return errAccountNotFound().Wrap(fmt.Errorf("account: parsing account id: %w", err))
	}
	tag, err := r.pool.Exec(ctx, `UPDATE tiktok_accounts SET needs_reauth = $2 WHERE id = $1`, id, needs)
	if err != nil {
		return fmt.Errorf("account: setting needs_reauth: %w", err)
	}
	return expectRow(tag)
}

// SetRevoked stamps the revoke time once. A second call keeps the original
// timestamp, so the audit record shows when access actually ended.
func (r *Repository) SetRevoked(ctx context.Context, accountID string, at time.Time) error {
	id, err := uuid.Parse(accountID)
	if err != nil {
		return errAccountNotFound().Wrap(fmt.Errorf("account: parsing account id: %w", err))
	}
	const q = `UPDATE tiktok_accounts SET revoked_at = COALESCE(revoked_at, $2) WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q, id, at)
	if err != nil {
		return fmt.Errorf("account: revoking account: %w", err)
	}
	return expectRow(tag)
}

// expectRow turns a zero row count into the shared not found error, so an
// unknown id reads the same whether it came from a SELECT or an UPDATE.
func expectRow(tag pgconn.CommandTag) error {
	if tag.RowsAffected() == 0 {
		return errAccountNotFound()
	}
	return nil
}
