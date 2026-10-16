// Package compose adapts module collaborators to the webhook seams.
//
// @file      internal/compose/webhook_adapters_test.go
// @for       Adapter tests for the reconciler fetcher, gate, and sink.
// @uses      context, errors, testing, internal/tiktokclient
// @reason    The adapters are the only place the reconciler meets TikTok, Redis, and the outbox consumer.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-15
package compose

import (
	"context"
	"errors"
	"testing"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/tiktokclient"
)

// stubClient replays one TikTok status verdict.
type stubClient struct {
	status tiktokclient.PublishStatus
	err    error
	calls  int
}

func (s *stubClient) FetchStatus(_ context.Context, _, _ string) (tiktokclient.PublishStatus, error) {
	s.calls++
	return s.status, s.err
}

// stubGate replays a limiter refusal.
type stubGate struct {
	err   error
	calls int
}

func (s *stubGate) Allow(_ context.Context, _ string, _ tiktokclient.Family) (int64, error) {
	s.calls++
	return 0, s.err
}

func TestStatusAdapterMapsTheCompletedVerdict(t *testing.T) {
	client := &stubClient{status: tiktokclient.PublishStatus{
		Status:                   "PUBLISH_COMPLETE",
		PublicalyAvailablePostID: []string{"post-1"},
		UploadedBytes:            42,
	}}
	adapter := newStatusAdapter(client)
	update, err := adapter.FetchStatus(context.Background(), "access", "pub-1")
	if err != nil {
		t.Fatalf("FetchStatus returned %v", err)
	}
	if update.Status != "PUBLISH_COMPLETE" || len(update.PostIDs) != 1 || update.UploadedBytes != 42 {
		t.Fatalf("update = %+v", update)
	}
	if client.calls != 1 {
		t.Fatalf("calls = %d, want 1", client.calls)
	}
}

func TestStatusAdapterPropagatesAnUpstreamError(t *testing.T) {
	client := &stubClient{err: errors.New("upstream down")}
	adapter := newStatusAdapter(client)
	if _, err := adapter.FetchStatus(context.Background(), "access", "pub-1"); err == nil {
		t.Fatal("FetchStatus succeeded against a failing client")
	}
}

func TestGateAdapterDropsTheFamilyAndRemainingCount(t *testing.T) {
	gate := &stubGate{}
	adapter := newGateAdapter(gate, tiktokclient.FamilyStatus)
	if err := adapter.Allow(context.Background(), "acc-1"); err != nil {
		t.Fatalf("Allow returned %v", err)
	}
	if gate.calls != 1 {
		t.Fatalf("calls = %d, want 1", gate.calls)
	}
}

func TestGateAdapterPropagatesARefusal(t *testing.T) {
	gate := &stubGate{err: errors.New("over the budget")}
	adapter := newGateAdapter(gate, tiktokclient.FamilyStatus)
	if err := adapter.Allow(context.Background(), "acc-1"); err == nil {
		t.Fatal("Allow succeeded against a refusing limiter")
	}
}
