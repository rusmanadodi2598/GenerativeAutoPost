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

// Service encrypts TikTok token material before it reaches the store and
// decrypts it only for the auth flow.
type Service struct {
	store   Store
	keyring *crypto.Keyring
	logger  *slog.Logger
	KeyID   string
}

// NewService wires the token vault. A nil logger falls back to slog.Default so
// a wiring mistake is not read as a storage outage.
func NewService(store Store, keyring *crypto.Keyring, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{store: store, keyring: keyring, logger: logger}
}

// keyID returns the configured key id or the default.
func (s *Service) keyID() string {
	if s.KeyID == "" {
		return defaultKeyID
	}
	return s.KeyID
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
	accessEnc, _, err = s.keyring.Encrypt(ctx, []byte(tokens.AccessToken), s.keyID())
	if err != nil {
		return nil, nil, errTokenEncrypt(err)
	}
	refreshEnc, _, err = s.keyring.Encrypt(ctx, []byte(tokens.RefreshToken), s.keyID())
	if err != nil {
		return nil, nil, errTokenEncrypt(err)
	}
	return accessEnc, refreshEnc, nil
}

// UpsertAccount seals the token pair and stores the account keyed by open id.
// A repeat callback for the same open id updates the existing row and returns
// its id, so the OAuth flow is idempotent.
func (s *Service) UpsertAccount(ctx context.Context, operatorID string, tokens tiktokclient.TokenSet, grantedScopes []string) (string, error) {
	accessEnc, refreshEnc, err := s.encryptPair(ctx, tokens)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	rec := Record{
		OperatorID:    operatorID,
		OpenID:        tokens.OpenID,
		Scopes:        copyStrings(grantedScopes),
		AccessEnc:     accessEnc,
		AccessExpiry:  now.Add(time.Duration(tokens.ExpiresIn) * time.Second),
		RefreshEnc:    refreshEnc,
		RefreshExpiry: now.Add(time.Duration(tokens.RefreshExpiresIn) * time.Second),
		KeyID:         s.keyID(),
		CreatedAt:     now,
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

// StoreTokens seals and persists a freshly rotated token pair. Ownership is
// enforced by the auth flow before this call, matching the store contract.
func (s *Service) StoreTokens(ctx context.Context, accountID string, tokens tiktokclient.TokenSet) error {
	accessEnc, refreshEnc, err := s.encryptPair(ctx, tokens)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	accessExp := now.Add(time.Duration(tokens.ExpiresIn) * time.Second)
	refreshExp := now.Add(time.Duration(tokens.RefreshExpiresIn) * time.Second)
	if err := s.store.UpdateTokens(ctx, accountID, accessEnc, refreshEnc, s.keyID(), accessExp, refreshExp); err != nil {
		return err
	}
	s.logger.DebugContext(ctx, "tiktok account tokens rotated", "account_id", accountID)
	return nil
}

// MarkNeedsReauth flags the account so the UI can ask for a new sign in.
func (s *Service) MarkNeedsReauth(ctx context.Context, accountID string) error {
	return s.store.SetNeedsReauth(ctx, accountID, true)
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
