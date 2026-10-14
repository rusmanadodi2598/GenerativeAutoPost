// Package media keeps delete idempotent for a retry after a lost response.
//
// @file      internal/media/delete_idempotent_test.go
// @for       Coverage for the missing, foreign, and owned delete outcomes.
// @uses      context, testing
// @reason    Locks the delete contract so a client retry never sees a 404.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package media

import (
	"context"
	"log/slog"
	"testing"
)

// deleteService builds a service over a store the test controls.
func deleteService(store Store) *Service {
	return NewService(store, ServiceConfig{Driver: string(MediaStorageDisk)}, slog.New(slog.DiscardHandler))
}

func TestDeleteMissingObjectIsNoOp(t *testing.T) {
	store := newFakeStore()
	svc := deleteService(store)

	if err := svc.Delete(context.Background(), "acct-1", "absent"); err != nil {
		t.Fatalf("delete missing = %v, want nil", err)
	}
	if store.deleteCalls != 0 {
		t.Errorf("delete calls = %d, want 0", store.deleteCalls)
	}
}

func TestDeleteMissingObjectOverDiskStoreIsNoOp(t *testing.T) {
	svc := newTestService(t)

	if err := svc.Delete(context.Background(), "acct-1", "absent"); err != nil {
		t.Errorf("delete missing = %v, want nil", err)
	}
}

func TestDeleteForeignObjectIsNotFoundAndKeepsObject(t *testing.T) {
	store := newFakeStore()
	store.objects["obj-1"] = MediaObject{ID: "obj-1", OwnerID: "acct-1"}
	svc := deleteService(store)

	err := svc.Delete(context.Background(), "acct-2", "obj-1")

	if !isNotFound(err) {
		t.Fatalf("delete foreign = %v, want NOT_FOUND", err)
	}
	if store.deleteCalls != 0 {
		t.Errorf("delete calls = %d, want 0", store.deleteCalls)
	}
	if _, ok := store.objects["obj-1"]; !ok {
		t.Error("foreign delete removed the owner's object")
	}
}

func TestDeleteOwnedObjectRemovesIt(t *testing.T) {
	store := newFakeStore()
	store.objects["obj-1"] = MediaObject{ID: "obj-1", OwnerID: "acct-1"}
	svc := deleteService(store)

	if err := svc.Delete(context.Background(), "acct-1", "obj-1"); err != nil {
		t.Fatalf("delete owned = %v, want nil", err)
	}
	if store.deleteCalls != 1 {
		t.Errorf("delete calls = %d, want 1", store.deleteCalls)
	}
	if _, ok := store.objects["obj-1"]; ok {
		t.Error("owned delete left the object in place")
	}
}

func TestDeleteTwiceIsStillNoOp(t *testing.T) {
	store := newFakeStore()
	store.objects["obj-1"] = MediaObject{ID: "obj-1", OwnerID: "acct-1"}
	svc := deleteService(store)
	ctx := context.Background()

	if err := svc.Delete(ctx, "acct-1", "obj-1"); err != nil {
		t.Fatalf("first delete = %v, want nil", err)
	}
	if err := svc.Delete(ctx, "acct-1", "obj-1"); err != nil {
		t.Fatalf("second delete = %v, want nil", err)
	}
	if store.deleteCalls != 1 {
		t.Errorf("delete calls = %d, want 1", store.deleteCalls)
	}
}

func TestDiskRejectsDotID(t *testing.T) {
	store := NewDisk(t.TempDir())

	if err := store.Put(context.Background(), MediaObject{ID: "."}, nil); err == nil {
		t.Fatal("expected a rejection for the dot id")
	}
}
