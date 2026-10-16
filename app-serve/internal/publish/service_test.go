// Package publish fakes the collaborators the init service depends on.
//
// @file      internal/publish/service_test.go
// @for       Fakes and fixtures for the init validation tests.
// @uses      context, testing, internal/apierror, internal/tiktokclient
// @reason    Keeps the init tests free of Redis, PostgreSQL, and TikTok network.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package publish

import (
	"context"
	"errors"
	"testing"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/platform/idempotency"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/tiktokclient"
)

const (
	testAccountID = "6f1b7a4e-1c2d-4f3a-9b8c-0d1e2f3a4b5c"
	testMediaID   = "7a2b8c5f-2d3e-4a5b-8c9d-1e2f3a4b5c6d"
	testOperator  = "8b3c9d60-3e4f-4b6c-9d0e-2f3a4b5c6d7e"
	testPublishID = "v_pub_12345"
)

// fakePoster records the calls it receives and replays canned TikTok answers.
type fakePoster struct {
	videoCalls   []tiktokclient.VideoInitRequest
	photoCalls   []tiktokclient.PhotoInitRequest
	draftCalls   []tiktokclient.DraftInitRequest
	uploadURL    string
	err          error
	accessSeen   string
	publishID    string
	optionsQuery int
}

// InitVideoPost returns the canned publish id and upload URL.
func (f *fakePoster) InitVideoPost(_ context.Context, access string, req tiktokclient.VideoInitRequest) (tiktokclient.VideoInitResponse, error) {
	f.accessSeen = access
	f.videoCalls = append(f.videoCalls, req)
	if f.err != nil {
		return tiktokclient.VideoInitResponse{}, f.err
	}
	id := f.publishID
	if id == "" {
		id = testPublishID
	}
	return tiktokclient.VideoInitResponse{PublishID: id, UploadURL: f.uploadURL}, nil
}

// InitPhotoPost returns the canned publish id.
func (f *fakePoster) InitPhotoPost(_ context.Context, access string, req tiktokclient.PhotoInitRequest) (tiktokclient.PhotoInitResponse, error) {
	f.accessSeen = access
	f.photoCalls = append(f.photoCalls, req)
	if f.err != nil {
		return tiktokclient.PhotoInitResponse{}, f.err
	}
	return tiktokclient.PhotoInitResponse{PublishID: testPublishID}, nil
}

// InitDraftUpload returns the canned publish id and upload URL.
func (f *fakePoster) InitDraftUpload(_ context.Context, access string, req tiktokclient.DraftInitRequest) (tiktokclient.DraftInitResponse, error) {
	f.accessSeen = access
	f.draftCalls = append(f.draftCalls, req)
	if f.err != nil {
		return tiktokclient.DraftInitResponse{}, f.err
	}
	return tiktokclient.DraftInitResponse{PublishID: testPublishID, UploadURL: f.uploadURL}, nil
}

// QueryCreatorInfo returns the canned creator profile.
func (f *fakePoster) QueryCreatorInfo(context.Context, string) (tiktokclient.CreatorInfo, error) {
	f.optionsQuery++
	return tiktokclient.CreatorInfo{}, f.err
}

// FetchStatus returns a completed status.
func (f *fakePoster) FetchStatus(context.Context, string, string) (tiktokclient.PublishStatus, error) {
	return tiktokclient.PublishStatus{Status: "PUBLISH_COMPLETE"}, f.err
}

// fakeAccounts resolves an owned account to a fixed access token.
type fakeAccounts struct {
	access string
	err    error
	calls  int
}

// Owned returns the access token for an owned account.
func (f *fakeAccounts) Owned(context.Context, string, string) (string, error) {
	f.calls++
	if f.err != nil {
		return "", f.err
	}
	if f.access == "" {
		return "access-token", nil
	}
	return f.access, nil
}

// fakeCreators serves the account privacy level options.
type fakeCreators struct {
	options []string
	err     error
}

// Options returns the current privacy level options.
func (f *fakeCreators) Options(context.Context, string, string) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.options, nil
}

// fakeAttempts records inserted rows and returns them unchanged.
type fakeAttempts struct {
	inserted []Record
	err      error
}

// Insert stores one attempt row.
func (f *fakeAttempts) Insert(_ context.Context, rec Record) (Record, error) {
	if f.err != nil {
		return Record{}, f.err
	}
	f.inserted = append(f.inserted, rec)
	return rec, nil
}

// fakeIdempotency mimics the Redis claimer in memory.
type fakeIdempotency struct {
	hashes     map[string]string
	responses  map[string][]byte
	claimedErr error
	beginErr   error
}

// newFakeIdempotency returns an empty in memory claimer.
func newFakeIdempotency() *fakeIdempotency {
	return &fakeIdempotency{hashes: map[string]string{}, responses: map[string][]byte{}}
}

// Begin claims key for hash and replays a stored response when the hash matches.
func (f *fakeIdempotency) Begin(_ context.Context, key, hash string) ([]byte, error) {
	if f.beginErr != nil {
		return nil, f.beginErr
	}
	if f.claimedErr != nil {
		return nil, f.claimedErr
	}
	stored, ok := f.hashes[key]
	if !ok {
		f.hashes[key] = hash
		return nil, nil
	}
	if stored != hash {
		return nil, idempotency.ErrKeyReused
	}
	return f.responses[key], nil
}

// Complete stores the response for a later replay.
func (f *fakeIdempotency) Complete(_ context.Context, key, hash string, response []byte) error {
	if stored, ok := f.hashes[key]; !ok || stored != hash {
		return idempotency.ErrKeyReused
	}
	f.responses[key] = response
	return nil
}

// Release drops the claim for a later retry.
func (f *fakeIdempotency) Release(_ context.Context, key, hash string) error {
	if stored, ok := f.hashes[key]; !ok || stored != hash {
		return idempotency.ErrKeyReused
	}
	delete(f.hashes, key)
	delete(f.responses, key)
	return nil
}

// harness wires a Service over the fakes for one test.
type harness struct {
	svc      *Service
	poster   *fakePoster
	accounts *fakeAccounts
	attempts *fakeAttempts
	idem     *fakeIdempotency
}

// newHarness wires the service with permissive defaults.
func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{
		poster:   &fakePoster{uploadURL: "https://upload.example/v4"},
		accounts: &fakeAccounts{},
		attempts: &fakeAttempts{},
		idem:     newFakeIdempotency(),
	}
	h.svc = NewService(Config{
		Poster:      h.poster,
		Accounts:    h.accounts,
		Creators:    &fakeCreators{options: []string{"SELF_ONLY", "FOLLOWERS", "PUBLIC"}},
		Attempts:    h.attempts,
		Idempotency: h.idem,
		MediaHost:   "media.example",
	})
	return h
}

// validVideoRequest returns a request that passes every guard.
func validVideoRequest() InitVideoRequest {
	return InitVideoRequest{
		AccountID: testAccountID,
		MediaID:   testMediaID,
		Source:    sourceFileUpload,
		PostInfo: PostInfoRequest{
			Title:        "hello",
			PrivacyLevel: "FOLLOWERS",
		},
	}
}

// errorCode returns the apierror code carried by err, empty when it is not typed.
func errorCode(err error) string {
	var apiErr *apierror.Error
	if errors.As(err, &apiErr) {
		return string(apiErr.Code)
	}
	return ""
}
