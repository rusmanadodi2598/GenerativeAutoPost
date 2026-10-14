// Package account holds the TikTok account aggregate and its persistence shapes.
//
// @file      internal/account/id.go
// @for       UUIDv7 generation for account row identifiers.
// @uses      github.com/google/uuid
// @reason    001 section 5 gives primary keys to the application as UUIDv7, so the database carries no default.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     domain
// @stability stable
// @since     2026-10-12
package account

import "github.com/google/uuid"

// NewID returns a UUIDv7 for a new account row. The time-ordered layout keeps
// index inserts at the right-hand edge instead of scattering pages.
func NewID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}
