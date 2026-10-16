// Package publish covers the webhook driven status transitions.
//
// @file      internal/publish/status_test.go
// @for       StatusUpdater tests for MarkPublishComplete, MarkPublishFailed, and MarkInboxDelivered.
// @uses      context, errors, testing, internal/apierror
// @reason    Webhook delivery is the primary source of publish state, so these writes must honour the aggregate rules.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-15
package publish

import (
	"context"
	"errors"
	"testing"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// statusStore is an in memory AttemptStore keyed by publish id, so the status
// writes can be asserted without PostgreSQL.
type statusStore struct {
	byPublishID map[string]*Record
	getErr      error
	updateErr   error
	updates     []Record
}

func newStatusStore() *statusStore {
	return &statusStore{byPublishID: map[string]*Record{}}
}

func (s *statusStore) Get(_ context.Context, id string) (Record, error) {
	for _, rec := range s.byPublishID {
		if rec.ID == id {
			return *rec, nil
		}
	}
	return Record{}, apierror.New(apierror.CodeNotFound, 0, "Publish attempt not found.")
}

func (s *statusStore) GetByPublishID(_ context.Context, publishID string) (Record, error) {
	if s.getErr != nil {
		return Record{}, s.getErr
	}
	rec, ok := s.byPublishID[publishID]
	if !ok {
		return Record{}, apierror.New(apierror.CodeNotFound, 0, "Publish attempt not found.")
	}
	return *rec, nil
}

func (s *statusStore) Update(_ context.Context, rec Record) error {
	if s.updateErr != nil {
		return s.updateErr
	}
	s.updates = append(s.updates, rec)
	stored, ok := s.byPublishID[rec.PublishID]
	if ok {
		*stored = rec
	}
	return nil
}

func (s *statusStore) seed(rec Record) {
	s.byPublishID[rec.PublishID] = &rec
}

// newStatusService builds a Service whose attempt reads and writes go to the
// in memory store.
func newStatusService(store *statusStore) *Service {
	svc := NewService(Config{
		Poster:      &fakePoster{},
		Accounts:    &fakeAccounts{},
		Creators:    &fakeCreators{},
		Idempotency: newFakeIdempotency(),
		MediaHost:   "media.example",
	})
	svc.chunks.attempts = store
	return svc
}

// openAttempt is a stored attempt waiting for a webhook verdict.
func openAttempt(publishID string, status Status) Record {
	return Record{
		ID: "attempt-1", AccountID: testAccountID, PublishID: publishID,
		PostMode: modeDirectPost, MediaType: "VIDEO", Source: sourceFileUpload,
		Status: status,
	}
}

func TestMarkPublishCompleteSettlesTheAttempt(t *testing.T) {
	store := newStatusStore()
	store.seed(openAttempt(testPublishID, StatusProcessingUpload))
	svc := newStatusService(store)
	if err := svc.MarkPublishComplete(context.Background(), testPublishID, "post-77"); err != nil {
		t.Fatalf("MarkPublishComplete returned %v", err)
	}
	got := store.byPublishID[testPublishID]
	if got.Status != StatusPublishComplete || got.PostID != "post-77" {
		t.Fatalf("stored attempt = %+v", *got)
	}
}

func TestMarkPublishFailedStoresReason(t *testing.T) {
	store := newStatusStore()
	store.seed(openAttempt(testPublishID, StatusProcessingUpload))
	svc := newStatusService(store)
	if err := svc.MarkPublishFailed(context.Background(), testPublishID, "AUDIO_TOO_LONG"); err != nil {
		t.Fatalf("MarkPublishFailed returned %v", err)
	}
	got := store.byPublishID[testPublishID]
	if got.Status != StatusFailed || got.FailReason != "AUDIO_TOO_LONG" {
		t.Fatalf("stored attempt = %+v", *got)
	}
}

func TestMarkInboxDeliveredMovesTheUpload(t *testing.T) {
	store := newStatusStore()
	store.seed(openAttempt(testPublishID, StatusProcessingUpload))
	svc := newStatusService(store)
	if err := svc.MarkInboxDelivered(context.Background(), testPublishID); err != nil {
		t.Fatalf("MarkInboxDelivered returned %v", err)
	}
	if store.byPublishID[testPublishID].Status != StatusSendToUserInbox {
		t.Fatalf("stored attempt = %+v", *store.byPublishID[testPublishID])
	}
}

func TestMarkPublishCompleteRejectsAnEmptyPostID(t *testing.T) {
	store := newStatusStore()
	store.seed(openAttempt(testPublishID, StatusProcessingUpload))
	svc := newStatusService(store)
	err := svc.MarkPublishComplete(context.Background(), testPublishID, "")
	if got := errorCode(err); got != string(apierror.CodeValidationFailed) {
		t.Fatalf("code = %q, want VALIDATION_FAILED (err %v)", got, err)
	}
	if len(store.updates) != 0 {
		t.Fatalf("update calls = %d, want 0", len(store.updates))
	}
}

func TestMarkPublishCompleteRejectsATerminalAttempt(t *testing.T) {
	store := newStatusStore()
	store.seed(openAttempt(testPublishID, StatusPublishComplete))
	svc := newStatusService(store)
	err := svc.MarkPublishComplete(context.Background(), testPublishID, "post-77")
	if got := errorCode(err); got != string(apierror.CodeConflict) {
		t.Fatalf("code = %q, want CONFLICT (err %v)", got, err)
	}
}

func TestMarkPublishCompleteAnswersNotFoundForAnUnknownPublishID(t *testing.T) {
	svc := newStatusService(newStatusStore())
	err := svc.MarkPublishComplete(context.Background(), "v_pub_missing", "post-1")
	if got := errorCode(err); got != string(apierror.CodeNotFound) {
		t.Fatalf("code = %q, want NOT_FOUND (err %v)", got, err)
	}
}

func TestMarkPublishFailedPropagatesAStoreFailure(t *testing.T) {
	store := newStatusStore()
	store.seed(openAttempt(testPublishID, StatusProcessingUpload))
	store.updateErr = errors.New("write refused")
	svc := newStatusService(store)
	if err := svc.MarkPublishFailed(context.Background(), testPublishID, "REASON"); err == nil {
		t.Fatal("MarkPublishFailed succeeded against a failing store")
	}
}

func TestMarkUnpublishedKeepsTheSettledAttempt(t *testing.T) {
	store := newStatusStore()
	store.seed(openAttempt(testPublishID, StatusPublishComplete))
	svc := newStatusService(store)
	if err := svc.MarkUnpublished(context.Background(), testPublishID, "post-77"); err != nil {
		t.Fatalf("MarkUnpublished returned %v", err)
	}
	if store.byPublishID[testPublishID].Status != StatusPublishComplete {
		t.Fatalf("stored attempt = %+v", *store.byPublishID[testPublishID])
	}
}
