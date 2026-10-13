// Package auth issues and checks operator sessions and OAuth state.
//
// @file      internal/auth/session.go
// @for       Redis backed operator sessions with a sliding expiry.
// @uses      context, encoding/json, errors, time, redis/go-redis/v9, internal/apierror, internal/platform/redisx
// @reason    Signed in operators need a server side session and CSRF token.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/platform/redisx"
)

// Session is the server side record behind a browser cookie.
type Session struct {
	ID         string    `json:"id"`
	OperatorID string    `json:"operator_id"`
	CSRFToken  string    `json:"csrf_token"`
	Scopes     []string  `json:"scopes"`
	CreatedAt  time.Time `json:"created_at"`
}

// errSessionUnauthorized is the client safe answer for a missing session.
var errSessionUnauthorized = apierror.New(apierror.CodeUnauthorized, 0, "Your session has expired. Sign in again.")

// errSessionUnavailable is the client safe answer for a Redis outage.
var errSessionUnavailable = apierror.New(apierror.CodeServiceUnavailable, 0, "Session storage is unavailable. Try again shortly.")

// SessionStore persists sessions in Redis under a fixed TTL.
type SessionStore struct {
	client *redis.Client
	ttl    time.Duration
}

// NewSessionStore wires a session store over Redis with an idle TTL.
func NewSessionStore(client *redis.Client, ttl time.Duration) *SessionStore {
	return &SessionStore{client: client, ttl: ttl}
}

// Create writes the session under its id with the configured TTL.
func (s *SessionStore) Create(ctx context.Context, sess Session) error {
	raw, err := json.Marshal(sess)
	if err != nil {
		return errSessionUnavailable.Wrap(err)
	}
	if err := s.client.Set(ctx, redisx.KeySession+sess.ID, raw, s.ttl).Err(); err != nil {
		return errSessionUnavailable.Wrap(err)
	}
	return nil
}

// Get loads and decodes the session, mapping a miss to unauthorized.
func (s *SessionStore) Get(ctx context.Context, id string) (*Session, error) {
	raw, err := s.client.Get(ctx, redisx.KeySession+id).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, errSessionUnauthorized
	}
	if err != nil {
		return nil, errSessionUnavailable.Wrap(err)
	}
	var sess Session
	if err := json.Unmarshal(raw, &sess); err != nil {
		return nil, errSessionUnavailable.Wrap(err)
	}
	return &sess, nil
}

// Delete removes the session. A missing key is not an error.
func (s *SessionStore) Delete(ctx context.Context, id string) error {
	if err := s.client.Del(ctx, redisx.KeySession+id).Err(); err != nil {
		return errSessionUnavailable.Wrap(err)
	}
	return nil
}

// Touch resets the TTL on an existing session. A missing key is unauthorized.
func (s *SessionStore) Touch(ctx context.Context, id string) error {
	ok, err := s.client.Expire(ctx, redisx.KeySession+id, s.ttl).Result()
	if err != nil {
		return errSessionUnavailable.Wrap(err)
	}
	if !ok {
		return errSessionUnauthorized
	}
	return nil
}

// NewSessionID returns a fresh opaque session id.
func NewSessionID() (string, error) {
	return randomToken()
}

// NewCSRFToken returns a fresh double submit token.
func NewCSRFToken() (string, error) {
	return randomToken()
}
