// Package account holds the TikTok account aggregate and its persistence shapes.
//
// @file      internal/account/domain.go
// @for       Account aggregate enforcing token rotation and revoke rules.
// @uses      time, slices, internal/apierror
// @reason    Token mutation rules belong on the aggregate, not on the service.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     domain
// @stability stable
// @since     2026-10-12
package account

import (
	"slices"
	"time"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// Account is the aggregate root for one TikTok account. Fields stay unexported
// so token material only moves through the methods below.
type Account struct {
	id          string
	operatorID  string
	openID      string
	unionID     string
	displayName string
	avatarURL   string
	scopes      []string
	accessEnc   []byte
	refreshEnc  []byte
	accessExp   time.Time
	refreshExp  time.Time
	keyID       string
	needsReauth bool
	revokedAt   *time.Time
	createdAt   time.Time
}

// newAccount rebuilds the aggregate from a persisted record.
func newAccount(rec Record) Account {
	return Account{
		id:          rec.ID,
		operatorID:  rec.OperatorID,
		openID:      rec.OpenID,
		unionID:     rec.UnionID,
		displayName: rec.DisplayName,
		avatarURL:   rec.AvatarURL,
		scopes:      copyStrings(rec.Scopes),
		accessEnc:   copyBytes(rec.AccessEnc),
		accessExp:   rec.AccessExpiry,
		refreshEnc:  copyBytes(rec.RefreshEnc),
		refreshExp:  rec.RefreshExpiry,
		keyID:       rec.KeyID,
		needsReauth: rec.NeedsReauth,
		revokedAt:   rec.RevokedAt,
		createdAt:   rec.CreatedAt,
	}
}

// ID returns the account identifier.
func (a *Account) ID() string { return a.id }

// OperatorID returns the owning operator identifier.
func (a *Account) OperatorID() string { return a.operatorID }

// OpenID returns the TikTok open id.
func (a *Account) OpenID() string { return a.openID }

// DisplayName returns the TikTok display name.
func (a *Account) DisplayName() string { return a.displayName }

// AvatarURL returns the TikTok avatar URL.
func (a *Account) AvatarURL() string { return a.avatarURL }

// Scopes returns a copy of the granted scopes so callers cannot mutate state.
func (a *Account) Scopes() []string { return copyStrings(a.scopes) }

// NeedsReauth reports whether the account must be reconnected.
func (a *Account) NeedsReauth() bool { return a.needsReauth }

// Revoked reports whether the account has been revoked.
func (a *Account) Revoked() bool { return a.revokedAt != nil }

// CreatedAt returns the account creation time.
func (a *Account) CreatedAt() time.Time { return a.createdAt }

// HasScope reports an exact scope grant. An empty argument never matches.
func (a *Account) HasScope(scope string) bool {
	if scope == "" {
		return false
	}
	return slices.Contains(a.scopes, scope)
}

// View projects the account for clients, token material excluded.
func (a *Account) View() View {
	return View{
		ID:          a.id,
		OpenID:      a.openID,
		DisplayName: a.displayName,
		AvatarURL:   a.avatarURL,
		Scopes:      a.Scopes(),
		NeedsReauth: a.needsReauth,
		Revoked:     a.Revoked(),
		CreatedAt:   a.createdAt,
	}
}

// EncryptedAccess returns the encrypted access token and its key id for the
// service to persist. Handlers must never call this.
func (a *Account) EncryptedAccess() ([]byte, string) {
	return copyBytes(a.accessEnc), a.keyID
}

// EncryptedRefresh returns the encrypted refresh token and its key id for the
// service to persist. Handlers must never call this.
func (a *Account) EncryptedRefresh() ([]byte, string) {
	return copyBytes(a.refreshEnc), a.keyID
}

// RotateTokens replaces both encrypted tokens with freshly minted material.
// A revoked account cannot rotate, and material must stay under the key the
// account was first stored with.
func (a *Account) RotateTokens(accessEnc, refreshEnc []byte, keyID string, accessExp, refreshExp time.Time) error {
	if a.Revoked() {
		return errAccountRevoked()
	}
	if len(accessEnc) == 0 || len(refreshEnc) == 0 {
		return apierror.New(apierror.CodeValidationFailed, 0, "Token material must not be empty.")
	}
	if a.keyID != "" && keyID != a.keyID {
		return apierror.New(apierror.CodeValidationFailed, 0, "Token key does not match the stored key.")
	}
	if !accessExp.After(time.Now().UTC()) {
		return apierror.New(apierror.CodeValidationFailed, 0, "Access expiry must be in the future.")
	}
	if refreshExp.Before(accessExp) {
		return apierror.New(apierror.CodeValidationFailed, 0, "Refresh expiry must not precede the access expiry.")
	}
	a.accessEnc = copyBytes(accessEnc)
	a.refreshEnc = copyBytes(refreshEnc)
	a.keyID = keyID
	a.accessExp = accessExp
	a.refreshExp = refreshExp
	a.needsReauth = false
	a.revokedAt = nil
	return nil
}

// Revoke marks the account revoked and drops the token material. It is
// idempotent: revoking an already revoked account is a no-op.
func (a *Account) Revoke(at time.Time) error {
	if a.revokedAt != nil {
		return nil
	}
	stamp := at.UTC()
	a.revokedAt = &stamp
	a.accessEnc = nil
	a.refreshEnc = nil
	a.accessExp = time.Time{}
	a.refreshExp = time.Time{}
	return nil
}

// MarkNeedsReauth flags the account for reconnection. A revoked account cannot
// be flagged.
func (a *Account) MarkNeedsReauth() error {
	if a.Revoked() {
		return errAccountRevoked()
	}
	a.needsReauth = true
	return nil
}

// errAccountRevoked is the client safe answer for any change to a revoked account.
func errAccountRevoked() *apierror.Error {
	return apierror.New(apierror.CodeConflict, 0, "This account is revoked and cannot be changed.")
}

// copyBytes clones a byte slice, treating nil as empty.
func copyBytes(in []byte) []byte {
	if len(in) == 0 {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

// copyStrings clones a string slice, treating nil as empty.
func copyStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}
