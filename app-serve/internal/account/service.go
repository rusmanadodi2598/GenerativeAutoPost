// Package account holds the TikTok account aggregate and its persistence shapes.
//
// @file      internal/account/service.go
// @for       Account token vault that encrypts at rest and decrypts on demand.
// @uses      context, log/slog, time, internal/apierror, internal/platform/crypto, internal/tiktokclient
// @reason    Keeps TikTok token material out of logs and client responses.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package account

import (
	"context"
	"log/slog"
	"time"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/platform/crypto"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/tiktokclient"
)

// defaultKeyID is used when the composition root sets no explicit key id.
const defaultKeyID = "primary"

// maxTokenSeconds caps a token lifetime at 30 days. A larger value means the
// upstream response is malformed, so the write is refused rather than trusted.
const maxTokenSeconds = 30 * 24 * 3600

// Service encrypts TikTok token material before it reaches the store and
// decrypts it only for the auth flow.
type Service struct {
	store   Store
	keyring *crypto.Keyring
	logger  *slog.Logger
	keyID   string
}

// NewService wires the token vault. keyID is fixed at construction: an empty
// value falls back to "primary". A nil logger falls back to slog.Default so a
// wiring mistake is not read as a storage outage.
func NewService(store Store, keyring *crypto.Keyring, keyID string, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	if keyID == "" {
		keyID = defaultKeyID
	}
	return &Service{store: store, keyring: keyring, keyID: keyID, logger: logger}
}

// tokenExpiries validates the lifetime pair and turns it into absolute
// expiries. A zero or negative value, a refresh lifetime shorter than the
// access lifetime, or a value past the ceiling is a validation failure.
func tokenExpiries(tokens tiktokclient.TokenSet) (accessExp, refreshExp time.Time, err error) {
	if tokens.ExpiresIn <= 0 || tokens.ExpiresIn > maxTokenSeconds {
		return time.Time{}, time.Time{}, apierror.New(apierror.CodeValidationFailed, 0, "Expires in must be between 1 and 2592000 seconds.")
	}
	if tokens.RefreshExpiresIn < tokens.ExpiresIn || tokens.RefreshExpiresIn > maxTokenSeconds {
		return time.Time{}, time.Time{}, apierror.New(apierror.CodeValidationFailed, 0, "Refresh expires in must be between the access expiry and 2592000 seconds.")
	}
	now := time.Now().UTC()
	return now.Add(time.Duration(tokens.ExpiresIn) * time.Second), now.Add(time.Duration(tokens.RefreshExpiresIn) * time.Second), nil
}

// errTokenEncrypt is the client safe answer for a failed seal. The cause stays
// on the wrapped error for development debug output, never on the wire.
func errTokenEncrypt(cause error) *apierror.Error {
	return apierror.New(apierror.CodeInternal, 0, "Could not store the TikTok credentials. Try again.").Wrap(cause)
}

// errTokenDecrypt is the client safe answer for an unreadable ciphertext.
func errTokenDecrypt(cause error) *apierror.Error {
	return apierror.New(apierror.CodeInternal, 0, "Could not read the TikTok credentials. Reconnect the account.").Wrap(cause)
}

// encryptPair seals the access and refresh tokens under the vault key.
func (s *Service) encryptPair(ctx context.Context, tokens tiktokclient.TokenSet) (accessEnc, refreshEnc []byte, err error) {
	accessEnc, _, err = s.keyring.Encrypt(ctx, []byte(tokens.AccessToken), s.keyID)
	if err != nil {
		return nil, nil, errTokenEncrypt(err)
	}
	refreshEnc, _, err = s.keyring.Encrypt(ctx, []byte(tokens.RefreshToken), s.keyID)
	if err != nil {
		return nil, nil, errTokenEncrypt(err)
	}
	return accessEnc, refreshEnc, nil
}

// UpsertAccount seals the token pair and stores the account keyed by open id.
// A repeat callback for the same open id updates the existing row and returns
// its id, so the OAuth flow is idempotent.
func (s *Service) UpsertAccount(ctx context.Context, operatorID string, tokens tiktokclient.TokenSet, grantedScopes []string) (string, error) {
	accessExp, refreshExp, err := tokenExpiries(tokens)
	if err != nil {
		return "", err
	}
	accessEnc, refreshEnc, err := s.encryptPair(ctx, tokens)
	if err != nil {
		return "", err
	}
	rec := Record{
		OperatorID:    operatorID,
		OpenID:        tokens.OpenID,
		Scopes:        copyStrings(grantedScopes),
		AccessEnc:     accessEnc,
		AccessExpiry:  accessExp,
		RefreshEnc:    refreshEnc,
		RefreshExpiry: refreshExp,
		KeyID:         s.keyID,
		CreatedAt:     time.Now().UTC(),
	}
	stored, err := s.store.UpsertEncrypted(ctx, rec)
	if err != nil {
		return "", err
	}
	s.logger.DebugContext(ctx, "tiktok account stored", "account_id", stored.ID, "operator_id", operatorID)
	return stored.ID, nil
}

// RefreshMaterial returns the plaintext token pair for one account. The store
// enforces ownership, so a foreign or unknown account fails before any decrypt
// runs. A decrypt failure is a typed internal error with no token text.
func (s *Service) RefreshMaterial(ctx context.Context, operatorID string, accountID string) (refreshToken string, accessToken string, err error) {
	rec, err := s.store.GetOwned(ctx, operatorID, accountID)
	if err != nil {
		return "", "", err
	}
	access, _, err := s.keyring.Decrypt(ctx, rec.AccessEnc)
	if err != nil {
		s.logger.ErrorContext(ctx, "decrypting tiktok access token", "account_id", accountID, "error", err)
		return "", "", errTokenDecrypt(err)
	}
	refresh, _, err := s.keyring.Decrypt(ctx, rec.RefreshEnc)
	if err != nil {
		s.logger.ErrorContext(ctx, "decrypting tiktok refresh token", "account_id", accountID, "error", err)
		return "", "", errTokenDecrypt(err)
	}
	return string(refresh), string(access), nil
}

// StoreTokens rotates the token pair of one account through the aggregate.
// The read is ownership checked, so a foreign or unknown account never reaches
// the write. A revoked account or an invalid expiry fails before the store is
// touched, and the persisted bytes come from the aggregate.
func (s *Service) StoreTokens(ctx context.Context, operatorID string, accountID string, tokens tiktokclient.TokenSet) error {
	rec, err := s.store.GetOwned(ctx, operatorID, accountID)
	if err != nil {
		return err
	}
	accessExp, refreshExp, err := tokenExpiries(tokens)
	if err != nil {
		return err
	}
	accessEnc, refreshEnc, err := s.encryptPair(ctx, tokens)
	if err != nil {
		return err
	}
	acc := newAccount(rec)
	if err := acc.RotateTokens(accessEnc, refreshEnc, s.keyID, accessExp, refreshExp); err != nil {
		return err
	}
	accessStored, storedKey := acc.EncryptedAccess()
	refreshStored, _ := acc.EncryptedRefresh()
	if err := s.store.UpdateTokens(ctx, accountID, accessStored, refreshStored, storedKey, accessExp, refreshExp); err != nil {
		return err
	}
	s.logger.DebugContext(ctx, "tiktok account tokens rotated", "account_id", accountID)
	return nil
}

// MarkNeedsReauth flags the account so the UI can ask for a new sign in. The
// ownership-checked read runs first, so a foreign operator gets the store's
// not-found answer and no write.
func (s *Service) MarkNeedsReauth(ctx context.Context, operatorID string, accountID string) error {
	rec, err := s.store.GetOwned(ctx, operatorID, accountID)
	if err != nil {
		return err
	}
	acc := newAccount(rec)
	if err := acc.MarkNeedsReauth(); err != nil {
		return err
	}
	return s.store.SetNeedsReauth(ctx, accountID, acc.NeedsReauth())
}

// MarkRevoked checks ownership, then stamps the revoke time. A foreign or
// unknown account fails before the store is mutated.
func (s *Service) MarkRevoked(ctx context.Context, operatorID string, accountID string) error {
	if _, err := s.store.GetOwned(ctx, operatorID, accountID); err != nil {
		return err
	}
	return s.store.SetRevoked(ctx, accountID, time.Now().UTC())
}

// GetOwned loads one account for its operator. The store's not-found error is
// propagated unchanged so the handler can map it without guessing.
func (s *Service) GetOwned(ctx context.Context, operatorID string, accountID string) (Account, error) {
	rec, err := s.store.GetOwned(ctx, operatorID, accountID)
	if err != nil {
		return Account{}, err
	}
	return newAccount(rec), nil
}
