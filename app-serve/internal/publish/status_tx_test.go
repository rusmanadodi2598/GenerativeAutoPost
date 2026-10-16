// Package publish binds the status writer to an open transaction.
//
// @file      internal/publish/status_tx_test.go
// @for       InTx tests proving a webhook transition commits inside the caller's transaction.
// @uses      context, testing, github.com/jackc/pgx/v5
// @reason    The outbox row and the state change must commit together, or a crash leaves one without the other.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-15
package publish

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// recordingTx captures the statements an apply step runs through it.
type recordingTx struct {
	statements []string
	args       []any
	err        error
}

func (t *recordingTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if t.err != nil {
		return pgconn.CommandTag{}, t.err
	}
	t.statements = append(t.statements, sql)
	t.args = append(t.args, args...)
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

// TestInTxRunsTheTransitionThroughTheTransaction proves the bound writer sends
// its UPDATE through the caller's handle rather than the pool.
func TestInTxRunsTheTransitionThroughTheTransaction(t *testing.T) {
	tx := &recordingTx{}
	up := newTxStatusWriter(tx)
	if err := up.MarkPublishComplete(context.Background(), testPublishID, "post-9"); err != nil {
		t.Fatalf("MarkPublishComplete returned %v", err)
	}
	if len(tx.statements) != 1 {
		t.Fatalf("statements = %d, want 1", len(tx.statements))
	}
	joined := strings.Join(append(tx.statements, " "), " ")
	for _, want := range []string{"publish_attempts", "PUBLISH_COMPLETE", "completed_at"} {
		if !strings.Contains(joined, want) {
			t.Errorf("statement is missing %q: %s", want, joined)
		}
	}
	if tx.args[0] != testPublishID || tx.args[1] != "post-9" {
		t.Errorf("args = %v", tx.args)
	}
}

// TestInTxMarkFailedCarriesTheReason proves the failed transition stores the
// upstream reason rather than an empty string.
func TestInTxMarkFailedCarriesTheReason(t *testing.T) {
	tx := &recordingTx{}
	up := newTxStatusWriter(tx)
	if err := up.MarkPublishFailed(context.Background(), testPublishID, "AUDIO_TOO_LONG"); err != nil {
		t.Fatalf("MarkPublishFailed returned %v", err)
	}
	joined := strings.Join(tx.statements, " ")
	if !strings.Contains(joined, "FAILED") || !strings.Contains(joined, "fail_reason") {
		t.Errorf("statement = %s", joined)
	}
	if tx.args[1] != "AUDIO_TOO_LONG" {
		t.Errorf("reason arg = %v", tx.args[1])
	}
}

// TestInTxPropagatesAnExecFailure proves a rejected write surfaces, so the
// dispatcher rolls the transaction back instead of stamping the event.
func TestInTxPropagatesAnExecFailure(t *testing.T) {
	tx := &recordingTx{err: errors.New("write refused")}
	up := newTxStatusWriter(tx)
	if err := up.MarkInboxDelivered(context.Background(), testPublishID); err == nil {
		t.Fatal("MarkInboxDelivered succeeded against a failing transaction")
	}
}

// TestInTxMarksUnpublishedWithoutChangingStatus proves the no-longer-public
// event only stores the post id and leaves the settled status alone.
func TestInTxMarksUnpublishedWithoutChangingStatus(t *testing.T) {
	tx := &recordingTx{}
	up := newTxStatusWriter(tx)
	if err := up.MarkUnpublished(context.Background(), testPublishID, "post-3"); err != nil {
		t.Fatalf("MarkUnpublished returned %v", err)
	}
	joined := strings.Join(tx.statements, " ")
	if strings.Contains(joined, "'PUBLISH_COMPLETE'") || strings.Contains(joined, "status =") {
		t.Errorf("statement must not set status: %s", joined)
	}
}
