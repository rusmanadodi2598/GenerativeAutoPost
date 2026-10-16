// Package compose adapts module collaborators to the webhook seams.
//
// @file      internal/compose/webhook_adapters.go
// @for       Reconciler adapters: upstream status, rate gate, outbox sink.
// @uses      context, internal/tiktokclient, internal/webhook
// @reason    The reconciler declares narrow ports; these adapters are the only place they meet TikTok and Redis.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-15
package compose

import (
	"context"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/account"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/publish"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/tiktokclient"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/webhook"
)

// compile time checks keep the reconciliation seams wired across packages.
var (
	_ attemptSweeper = (*publish.Repository)(nil)
	_ tokenResolver  = (*account.Service)(nil)
	_ limiter        = (*tiktokclient.Limiter)(nil)
	_ statusClient   = (*tiktokclient.Client)(nil)
)

type statusClient interface {
	FetchStatus(ctx context.Context, access, publishID string) (tiktokclient.PublishStatus, error)
}

// statusAdapter maps a TikTok status answer onto the reconciler's verdict.
type statusAdapter struct {
	client statusClient
}

// newStatusAdapter wraps a TikTok client as the reconcile status fetcher.
func newStatusAdapter(client statusClient) statusAdapter {
	return statusAdapter{client: client}
}

// FetchStatus reads one publish status and maps it to the reconciler verdict.
// Downloaded bytes are left out: only upload progress is stored.
func (a statusAdapter) FetchStatus(ctx context.Context, access, publishID string) (webhook.StatusUpdate, error) {
	status, err := a.client.FetchStatus(ctx, access, publishID)
	if err != nil {
		return webhook.StatusUpdate{}, err
	}
	return webhook.StatusUpdate{
		Status:        status.Status,
		FailReason:    status.FailReason,
		PostIDs:       status.PublicalyAvailablePostID,
		UploadedBytes: status.UploadedBytes,
	}, nil
}

// limiter is the shared per-account budget background work must respect.
type limiter interface {
	Allow(ctx context.Context, accountID string, family tiktokclient.Family) (int64, error)
}

// gateAdapter wraps a limiter and an endpoint family as a rate gate,
// dropping the remaining count since callers only need a yes or no.
type gateAdapter struct {
	limiter limiter
	family  tiktokclient.Family
}

// newGateAdapter wraps a limiter and an endpoint family as a rate gate.
func newGateAdapter(l limiter, family tiktokclient.Family) gateAdapter {
	return gateAdapter{limiter: l, family: family}
}

// Allow spends one call in this account's family budget.
func (a gateAdapter) Allow(ctx context.Context, accountID string) error {
	_, err := a.limiter.Allow(ctx, accountID, a.family)
	return err
}
