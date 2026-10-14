// Package media defines upload object shapes and the resumable chunk planner.
//
// @file      internal/media/domain_test.go
// @for       Table coverage for the chunk planner edge rules.
// @uses      testing, internal/apierror
// @reason    Locks planner behavior before stores and handlers use it.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     domain
// @stability stable
// @since     2026-10-12
package media

import (
	"errors"
	"testing"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

func TestPlanChunks(t *testing.T) {
	mb := int64(1 << 20)

	t.Run("exact multiple splits evenly", func(t *testing.T) {
		plans, err := PlanChunks(10*mb, 5*mb)
		if err != nil {
			t.Fatalf("PlanChunks returned error: %v", err)
		}
		if len(plans) != 2 {
			t.Fatalf("expected 2 chunks, got %d", len(plans))
		}
		if plans[0].Size() != 5*mb || plans[1].Size() != 5*mb {
			t.Fatalf("unexpected sizes %d and %d", plans[0].Size(), plans[1].Size())
		}
	})

	t.Run("small remainder merges into previous chunk", func(t *testing.T) {
		total := 10*mb + 123
		plans, err := PlanChunks(total, 5*mb)
		if err != nil {
			t.Fatalf("PlanChunks returned error: %v", err)
		}
		if len(plans) != 2 {
			t.Fatalf("expected 2 chunks, got %d", len(plans))
		}
		if plans[0].Size() != 5*mb {
			t.Fatalf("expected first size %d, got %d", 5*mb, plans[0].Size())
		}
		if plans[1].Size() != 5*mb+123 {
			t.Fatalf("expected merged size %d, got %d", 5*mb+123, plans[1].Size())
		}
	})

	t.Run("single chunk covers total below chunk size", func(t *testing.T) {
		plans, err := PlanChunks(1024, 5*mb)
		if err != nil {
			t.Fatalf("PlanChunks returned error: %v", err)
		}
		if len(plans) != 1 {
			t.Fatalf("expected 1 chunk, got %d", len(plans))
		}
		if plans[0].First != 0 || plans[0].Last != 1024 {
			t.Fatalf("unexpected range %d-%d", plans[0].First, plans[0].Last)
		}
	})

	t.Run("over 1000 chunks rejected", func(t *testing.T) {
		total := int64(1001) * 5 * mb
		_, err := PlanChunks(total, 5*mb)
		if err == nil {
			t.Fatal("expected error for 1001 chunk input")
		}
		var apiErr *apierror.Error
		if !errors.As(err, &apiErr) {
			t.Fatalf("expected typed apierror, got %T", err)
		}
		if apiErr.Code != apierror.CodeValidationFailed {
			t.Fatalf("expected VALIDATION_FAILED, got %s", apiErr.Code)
		}
	})

	t.Run("merge beyond 64 MB rejected", func(t *testing.T) {
		total := MaxChunkSize + mb
		_, err := PlanChunks(total, MaxChunkSize)
		if err == nil {
			t.Fatal("expected error when merged chunk exceeds 64 MB")
		}
		var apiErr *apierror.Error
		if !errors.As(err, &apiErr) {
			t.Fatalf("expected typed apierror, got %T", err)
		}
	})

	t.Run("zero bytes rejected", func(t *testing.T) {
		_, err := PlanChunks(0, 5*mb)
		if err == nil {
			t.Fatal("expected error for zero bytes")
		}
		var apiErr *apierror.Error
		if !errors.As(err, &apiErr) {
			t.Fatalf("expected typed apierror, got %T", err)
		}
		if apiErr.Code != apierror.CodeValidationFailed {
			t.Fatalf("expected VALIDATION_FAILED, got %s", apiErr.Code)
		}
	})
}
