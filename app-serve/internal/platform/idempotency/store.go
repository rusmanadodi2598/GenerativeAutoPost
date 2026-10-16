// Package idempotency serializes Idempotency-Key handling over Redis.
//
// @file      internal/platform/idempotency/store.go
// @for       Atomic idempotency key claim, replay, completion, and release.
// @uses      context, crypto/sha256, time, redis/go-redis/v9, internal/apierror, internal/platform/redisx
// @reason    Publish and media both need one claim per key, so the store lives below both.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-12
package idempotency

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

// DefaultTTL holds a claimed key for one day per 003 section 10.
const DefaultTTL = 24 * time.Hour

// ErrKeyReused rejects a key claimed with a different body hash.
var ErrKeyReused = errors.New("idempotency key reused with a different body")

// releaseScript drops a claim only while it still carries the body hash the
// caller claimed with, so a slow failure cannot erase the outcome a later
// attempt already stored under the same key.
var releaseScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("DEL", KEYS[1], KEYS[2])
end
return 0
`)

// Store claims one outcome per key and body hash.
type Store struct {
	client *redis.Client
	ttl    time.Duration
}

// New wires the store over a Redis client and TTL. A non positive TTL falls
// back to the 24 hour default.
func New(client *redis.Client, ttl time.Duration) *Store {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Store{client: client, ttl: ttl}
}

// BodyHash returns the hex sha256 of one request body for claim comparisons.
func BodyHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func (s *Store) key(k string) string { return redisx.KeyIdempotency + k }

// Begin claims key for bodyHash. It returns the completed response on a same
// key and same body repeat, and ErrKeyReused on a different body. A repeat
// claim for an incomplete first request has no completed response yet: the
// caller gets a typed conflict so it knows to retry instead of waiting.
func (s *Store) Begin(ctx context.Context, key, bodyHash string) ([]byte, error) {
	if key == "" {
		return nil, apierror.New(apierror.CodeValidationFailed, 0, "Idempotency key is required.")
	}
	if bodyHash == "" {
		return nil, apierror.New(apierror.CodeValidationFailed, 0, "Idempotency body hash is required.")
	}
	set, err := s.client.SetNX(ctx, s.key(key)+":hash", bodyHash, s.ttl).Result()
	if err != nil {
		return nil, fmt.Errorf("idempotency: claiming key: %w", err)
	}
	if set {
		return nil, nil
	}
	stored, err := s.client.Get(ctx, s.key(key)+":hash").Result()
	if err != nil {
		return nil, fmt.Errorf("idempotency: reading hash: %w", err)
	}
	if stored != bodyHash {
		return nil, ErrKeyReused
	}
	resp, err := s.client.Get(ctx, s.key(key)+":resp").Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, apierror.New(apierror.CodeConflict, 0, "A request is already in progress for this key.")
	}
	if err != nil {
		return nil, fmt.Errorf("idempotency: reading response: %w", err)
	}
	return resp, nil
}

// Complete stores the finished response for later same key replays.
func (s *Store) Complete(ctx context.Context, key, bodyHash string, response []byte) error {
	if key == "" {
		return apierror.New(apierror.CodeValidationFailed, 0, "Idempotency key is required.")
	}
	stored, err := s.client.Get(ctx, s.key(key)+":hash").Result()
	if errors.Is(err, redis.Nil) {
		return apierror.New(apierror.CodeNotFound, 0, "Idempotency key is not claimed.")
	}
	if err != nil {
		return fmt.Errorf("idempotency: reading hash: %w", err)
	}
	if stored != bodyHash {
		return ErrKeyReused
	}
	if err := s.client.Set(ctx, s.key(key)+":resp", response, s.ttl).Err(); err != nil {
		return fmt.Errorf("idempotency: storing response: %w", err)
	}
	return nil
}

// Release gives a failed attempt's key back so the caller may correct the
// request and retry under it, instead of being answered 409 by a claim that
// will never complete.
func (s *Store) Release(ctx context.Context, key, bodyHash string) error {
	if key == "" || bodyHash == "" {
		return apierror.New(apierror.CodeValidationFailed, 0,
			"Idempotency key and body hash are required.")
	}
	if err := releaseScript.Run(ctx, s.client,
		[]string{s.key(key) + ":hash", s.key(key) + ":resp"}, bodyHash).Err(); err != nil {
		return fmt.Errorf("idempotency: releasing claim: %w", err)
	}
	return nil
}
