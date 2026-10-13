// Package auth issues and checks operator sessions and OAuth state.
//
// @file      internal/auth/schema.go
// @for       Wire shapes for the current operator session.
// @uses      nothing beyond the Go standard library
// @reason    The console needs the session view without token material.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-12
package auth

import "time"

// SessionResponse is the session view sent to the console. It deliberately
// carries no token material: the ID stays server side.
type SessionResponse struct {
	OperatorID string    `json:"operator_id"`
	Scopes     []string  `json:"scopes"`
	ExpiresAt  time.Time `json:"expires_at"`
}
