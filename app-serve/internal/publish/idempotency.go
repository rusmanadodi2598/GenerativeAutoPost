// Package publish serializes Idempotency-Key handling over Redis.
//
// @file      internal/publish/idempotency.go
// @for       Atomic idempotency key claim, replay, and completion.
// @uses      context, crypto/sha256, time, redis/go-redis/v9, internal/apierror, internal/platform/redisx
// @reason    Duplicate inits must replay or clash without a second publish.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-12
package publish

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/platform/redisx"
)

// idempotencyTTL holds a claimed key for one day per 003 section 10.
const idempotencyTTL = 24 * time.Hour

// ErrIdempotencyKeyReused rejects a key claimed with a different body.
var ErrIdempotencyKeyReused = errors.New("idempotency key reused with a different body")

// IdempotencyStore claims one outcome per key and body hash.
type IdempotencyStore struct {
	client *redis.Client
	ttl    time.Duration
}

// NewIdempotencyStore wires the store over a Redis client and TTL. A non
// positive TTL falls back to the 24 hour default.
func NewIdempotencyStore(client *redis.Client, ttl time.Duration) *IdempotencyStore {
	if ttl <= 0 {
		ttl = idempotencyTTL
	}
	return &IdempotencyStore{client: client, ttl: ttl}
}

// BodyHash returns the hex sha256 of one request body for claim comparisons.
func BodyHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func (s *IdempotencyStore) key(k string) string { return redisx.KeyIdempotency + k }

// Begin claims key for bodyHash. It returns the completed response on a same
// key and same body repeat, and ErrIdempotencyKeyReused on a different body. A
// repeat claim for an incomplete first request has no completed response yet:
// the caller gets a typed conflict so it knows to retry instead of waiting.
func (s *IdempotencyStore) Begin(ctx context.Context, key, bodyHash string) ([]byte, error) {
	if key == "" {
		return nil, apierror.New(apierror.CodeValidationFailed, 0, "Idempotency key is required.")
	}
	if bodyHash == "" {
		return nil, apierror.New(apierror.CodeValidationFailed, 0, "Idempotency body hash is required.")
	}
	set, err := s.client.SetNX(ctx, s.key(key)+":hash", bodyHash, s.ttl).Result()
	if err != nil {
		return nil, fmt.Errorf("publish: claiming idempotency key: %w", err)
	}
	if set {
		return nil, nil
	}
	stored, err := s.client.Get(ctx, s.key(key)+":hash").Result()
	if err != nil {
		return nil, fmt.Errorf("publish: reading idempotency hash: %w", err)
	}
	if stored != bodyHash {
		return nil, ErrIdempotencyKeyReused
	}
	resp, err := s.client.Get(ctx, s.key(key)+":resp").Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, apierror.New(apierror.CodeConflict, 0, "Publish is already in progress for this key.")
	}
	if err != nil {
		return nil, fmt.Errorf("publish: reading idempotency response: %w", err)
	}
	return resp, nil
}

// Complete stores the finished response for later same key replays.
func (s *IdempotencyStore) Complete(ctx context.Context, key, bodyHash string, response []byte) error {
	if key == "" {
		return apierror.New(apierror.CodeValidationFailed, 0, "Idempotency key is required.")
	}
	stored, err := s.client.Get(ctx, s.key(key)+":hash").Result()
	if errors.Is(err, redis.Nil) {
		return apierror.New(apierror.CodeNotFound, 0, "Idempotency key is not claimed.")
	}
	if err != nil {
		return fmt.Errorf("publish: reading idempotency hash: %w", err)
	}
	if stored != bodyHash {
		return ErrIdempotencyKeyReused
	}
	if err := s.client.Set(ctx, s.key(key)+":resp", response, s.ttl).Err(); err != nil {
		return fmt.Errorf("publish: storing idempotency response: %w", err)
	}
	return nil
}
