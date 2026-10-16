// Package media locks the Idempotency-Key behavior of the upload route.
//
// @file      internal/media/upload_idempotency_test.go
// @for       Coverage for upload claim, replay, reuse, release, and conflicts.
// @uses      context, errors, net/http, httptest, testing
// @reason    A retried upload must return the first object, never a second one.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-16
package media

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/platform/idempotency"
)

// fakeGuard mimics the Redis store in memory and records release calls.
type fakeGuard struct {
	hashes    map[string]string
	responses map[string][]byte
	released  []string
	beginErr  error
}

func newFakeGuard() *fakeGuard {
	return &fakeGuard{hashes: map[string]string{}, responses: map[string][]byte{}}
}

func (f *fakeGuard) Begin(_ context.Context, key, hash string) ([]byte, error) {
	if f.beginErr != nil {
		return nil, f.beginErr
	}
	stored, ok := f.hashes[key]
	if !ok {
		f.hashes[key] = hash
		return nil, nil
	}
	if stored != hash {
		return nil, idempotency.ErrKeyReused
	}
	resp, ok := f.responses[key]
	if !ok {
		return nil, claimInProgress()
	}
	return resp, nil
}

func (f *fakeGuard) Complete(_ context.Context, key, hash string, response []byte) error {
	if stored, ok := f.hashes[key]; !ok || stored != hash {
		return idempotency.ErrKeyReused
	}
	f.responses[key] = response
	return nil
}

func (f *fakeGuard) Release(_ context.Context, key, hash string) error {
	if stored, ok := f.hashes[key]; !ok || stored != hash {
		return idempotency.ErrKeyReused
	}
	f.released = append(f.released, key)
	delete(f.hashes, key)
	delete(f.responses, key)
	return nil
}

// claimInProgress is the typed conflict the real store returns while a first
// upload under the same key has not finished.
func claimInProgress() error {
	return apierror.New(apierror.CodeConflict, 0, "A request is already in progress for this key.")
}

// uploadWithHeaders runs one upload through a guarded router and returns the
// recorder plus the store so a test can assert on the stored objects.
func uploadWithHeaders(t *testing.T, guard *fakeGuard, payload string, headers map[string]string) (*httptest.ResponseRecorder, *fakeStore) {
	t.Helper()
	store := newFakeStore()
	router := guardedRouter(t, store, guard, "acct-1", 0)
	req := uploadRequest(t, "video/mp4", "clip.mp4", payload, map[string]string{"kind": "video"})
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec, store
}

func TestUploadSameKeySameChecksumReplaysTheStoredObject(t *testing.T) {
	guard := newFakeGuard()
	payload := "replay bytes"
	headers := map[string]string{
		"Idempotency-Key": "key-1",
		"Checksum-SHA256": checksumOf(payload),
	}

	first, store := uploadWithHeaders(t, guard, payload, headers)
	if first.Code != http.StatusCreated {
		t.Fatalf("first status = %d: %s", first.Code, first.Body.String())
	}
	second, _ := uploadWithHeaders(t, guard, payload, headers)
	if second.Code != http.StatusCreated {
		t.Fatalf("second status = %d: %s", second.Code, second.Body.String())
	}
	// Only the data block is compared: meta carries the request id and the
	// server clock, which differ on every response by design.
	if firstID, secondID := uploadedID(t, first), uploadedID(t, second); firstID != secondID {
		t.Errorf("replayed id = %q, want the first id %q", secondID, firstID)
	}
	if len(store.objects) != 1 {
		t.Errorf("stored objects = %d, want 1: the repeat must not store again", len(store.objects))
	}
}

// uploadedID reads the media id out of one upload envelope.
func uploadedID(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding body %q: %v", rec.Body.String(), err)
	}
	return body.Data.ID
}

func TestUploadSameKeyDifferentChecksumIsReuse(t *testing.T) {
	guard := newFakeGuard()
	first := map[string]string{
		"Idempotency-Key": "key-1",
		"Checksum-SHA256": checksumOf("first"),
	}
	if rec, _ := uploadWithHeaders(t, guard, "first", first); rec.Code != http.StatusCreated {
		t.Fatalf("first status = %d: %s", rec.Code, rec.Body.String())
	}

	second := map[string]string{
		"Idempotency-Key": "key-1",
		"Checksum-SHA256": checksumOf("second"),
	}
	rec, _ := uploadWithHeaders(t, guard, "second", second)
	assertStatus(t, rec, http.StatusConflict)
	assertCode(t, rec, "IDEMPOTENCY_KEY_REUSED")
}

func TestUploadKeyWithoutChecksumIsRejected(t *testing.T) {
	guard := newFakeGuard()

	rec, store := uploadWithHeaders(t, guard, "bytes", map[string]string{"Idempotency-Key": "key-1"})

	assertStatus(t, rec, http.StatusUnprocessableEntity)
	assertCode(t, rec, "VALIDATION_FAILED")
	if len(store.objects) != 0 {
		t.Errorf("stored objects = %d, want 0", len(store.objects))
	}
}

func TestUploadMalformedChecksumIsRejected(t *testing.T) {
	guard := newFakeGuard()

	rec, _ := uploadWithHeaders(t, guard, "bytes", map[string]string{"Checksum-SHA256": "not-a-digest"})

	assertStatus(t, rec, http.StatusUnprocessableEntity)
	assertCode(t, rec, "VALIDATION_FAILED")
}

func TestUploadFailedUploadReleasesTheClaim(t *testing.T) {
	guard := newFakeGuard()
	payload := "bytes"
	headers := map[string]string{
		"Idempotency-Key": "key-1",
		// A checksum that does not match the payload fails the upload after
		// the claim, so the key must be given back for a corrected retry.
		"Checksum-SHA256": checksumOf("different"),
	}

	rec, store := uploadWithHeaders(t, guard, payload, headers)
	assertStatus(t, rec, http.StatusUnprocessableEntity)
	if len(store.objects) != 0 {
		t.Errorf("stored objects = %d, want 0", len(store.objects))
	}
	if len(guard.released) != 1 || guard.released[0] != "key-1" {
		t.Errorf("released = %v, want [key-1]", guard.released)
	}
}

func TestUploadSameKeyWhileFirstIsInProgressIsConflict(t *testing.T) {
	guard := newFakeGuard()
	payload := "in flight bytes"
	headers := map[string]string{
		"Idempotency-Key": "key-1",
		"Checksum-SHA256": checksumOf(payload),
	}
	// Claim the key without completing it, the state a running first upload is
	// in from the second request's point of view.
	if _, err := guard.Begin(context.Background(), "key-1", checksumOf(payload)); err != nil {
		t.Fatalf("seeding a claim: %v", err)
	}

	rec, store := uploadWithHeaders(t, guard, payload, headers)
	assertStatus(t, rec, http.StatusConflict)
	assertCode(t, rec, "CONFLICT")
	if len(store.objects) != 0 {
		t.Errorf("stored objects = %d, want 0", len(store.objects))
	}
}

func TestUploadUppercaseChecksumStillClaimsAndStores(t *testing.T) {
	guard := newFakeGuard()
	payload := "mixed case"
	headers := map[string]string{
		"Idempotency-Key": "key-1",
		"Checksum-SHA256": "  " + upperChecksum(payload) + "  ",
	}

	rec, store := uploadWithHeaders(t, guard, payload, headers)
	assertStatus(t, rec, http.StatusCreated)
	if len(store.objects) != 1 {
		t.Errorf("stored objects = %d, want 1", len(store.objects))
	}
}

// upperChecksum returns the uppercase hex digest for a payload.
func upperChecksum(payload string) string {
	upper := checksumOf(payload)
	out := make([]byte, len(upper))
	for i := 0; i < len(upper); i++ {
		c := upper[i]
		if c >= 'a' && c <= 'f' {
			c -= 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}
