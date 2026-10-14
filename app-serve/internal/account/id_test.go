// Package account holds the TikTok account aggregate and its persistence shapes.
//
// @file      internal/account/id_test.go
// @for       UUIDv7 id helper coverage.
// @uses      encoding, github.com/google/uuid
// @reason    Guarantees the generated identifier is a v7 uuid before rows depend on it.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     domain
// @stability stable
// @since     2026-10-12
package account

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestNewIDIsUUIDv7(t *testing.T) {
	t.Parallel()
	first, err := NewID()
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	if first == "" {
		t.Fatal("NewID returned an empty id")
	}
	parsed, err := uuid.Parse(first)
	if err != nil {
		t.Fatalf("NewID %q is not a uuid: %v", first, err)
	}
	if parsed.Version() != 7 {
		t.Errorf("NewID %q has version %d, want 7", first, parsed.Version())
	}
	raw, err := hex.DecodeString(strings.ReplaceAll(first, "-", ""))
	if err != nil {
		t.Fatalf("NewID %q is not hex: %v", first, err)
	}
	if (raw[6]>>4)&0xf != 7 {
		t.Errorf("NewID %q carries nibble %d, want 7", first, (raw[6]>>4)&0xf)
	}
	second, err := NewID()
	if err != nil {
		t.Fatalf("second NewID: %v", err)
	}
	if first == second {
		t.Error("two NewID calls returned the same id")
	}
}
