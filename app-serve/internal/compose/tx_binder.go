// Package compose adapts the publish repository to the dispatch binder.
//
// @file      internal/compose/tx_binder.go
// @for       StatusBinder adapter routing the commit transaction to publish.
// @uses      context, internal/publish, internal/webhook
// @reason    The dispatch commit lands in one transaction, so the binder must speak both seams.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-16
package compose

import (
	"context"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/publish"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/webhook"
)

// txBinder adapts a publish repository to the webhook transaction binder.
type txBinder struct {
	repo *publish.Repository
}

// InTx binds the repository to the open transaction and returns the writer the
// dispatcher drives, so the state change commits with the outbox row.
func (b txBinder) InTx(_ context.Context, tx webhook.Tx) webhook.StatusUpdater {
	return b.repo.InTx(txAdapter{inner: tx})
}

// txAdapter translates the webhook transaction handle to the publish shape.
type txAdapter struct {
	inner webhook.Tx
}

// Exec runs one statement through the dispatch transaction.
func (a txAdapter) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return a.inner.Exec(ctx, sql, args...)
}
