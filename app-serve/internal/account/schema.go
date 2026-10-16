// Package account holds the TikTok account aggregate and its persistence shapes.
//
// @file      internal/account/schema.go
// @for       Account persistence shapes and the store contract.
// @uses      context, time
// @reason    One place names the account record and its storage operations.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     schema
// @stability stable
// @since     2026-10-12
package account

import (
	"context"
	"time"
)

// View is the client facing projection of an account, tokens excluded.
type View struct {
	ID          string    `json:"id"`
	OpenID      string    `json:"open_id"`
	DisplayName string    `json:"display_name"`
	AvatarURL   string    `json:"avatar_url"`
	Scopes      []string  `json:"scopes"`
	NeedsReauth bool      `json:"needs_reauth"`
	Revoked     bool      `json:"revoked"`
	CreatedAt   time.Time `json:"created_at"`
}

// Record is the persistence shape: metadata plus encrypted token material.
type Record struct {
	ID            string
	OperatorID    string
	OpenID        string
	UnionID       string
	DisplayName   string
	AvatarURL     string
	Scopes        []string
	AccessEnc     []byte
	AccessExpiry  time.Time
	RefreshEnc    []byte
	RefreshExpiry time.Time
	KeyID         string
	NeedsReauth   bool
	RevokedAt     *time.Time
	CreatedAt     time.Time
}

// Store persists and loads account records. Implementations enforce that an
// account is only visible to its owning operator, except for GetSystem, which
// the background sweep needs because it has no operator session.
type Store interface {
	UpsertEncrypted(ctx context.Context, rec Record) (Record, error)
	GetOwned(ctx context.Context, operatorID string, accountID string) (Record, error)
	GetSystem(ctx context.Context, accountID string) (Record, error)
	UpdateTokens(ctx context.Context, accountID string, accessEnc, refreshEnc []byte, keyID string, accessExp, refreshExp time.Time) error
	SetNeedsReauth(ctx context.Context, accountID string, needs bool) error
	SetRevoked(ctx context.Context, accountID string, at time.Time) error
}
