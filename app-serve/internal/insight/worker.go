// Package insight sweeps connected accounts into the stats history.
//
// @file      internal/insight/worker.go
// @for       Periodic sweep that refreshes tokens, pages each account, and appends stats.
// @uses      context, fmt, log/slog, strconv, time
// @reason    The read path went read-only, so this worker is the steady writer of video_stats.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     worker
// @stability stable
// @since     2026-10-16
package insight

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"
)

// TokenRefresher renews the account token before a sweep reads it.
type TokenRefresher interface {
	Refresh(ctx context.Context, operatorID, accountID string) error
}

// AccountLister lists the operator's connected accounts.
type AccountLister interface {
	List(ctx context.Context, operatorID string) ([]string, error)
}

// RateGate caps list calls per account so a sweep never spikes usage.
type RateGate interface {
	Allow(ctx context.Context, accountID string) error
}

// StatsWorkerConfig carries the worker dependencies and bounds.
type StatsWorkerConfig struct {
	OperatorID string
	Videos     VideoClient
	Accounts   AccountResolver
	Refresher  TokenRefresher
	Lister     AccountLister
	Gate       RateGate
	Store      SnapshotStore
	Interval   time.Duration
	Retention  time.Duration
	Batch      int
	Now        func() time.Time
	Logger     *slog.Logger
}

// StatsWorker sweeps every connected account on an interval.
type StatsWorker struct {
	cfg StatsWorkerConfig
}

// defaults bound one sweep when a field is zero.
const (
	defaultStatsWorkerInterval  = 3 * time.Hour
	defaultStatsWorkerRetention = 168 * time.Hour
)

// NewStatsWorker wires the sweep, defaulting the interval, retention, batch,
// clock, and logger.
func NewStatsWorker(cfg StatsWorkerConfig) *StatsWorker {
	if cfg.Interval <= 0 {
		cfg.Interval = defaultStatsWorkerInterval
	}
	if cfg.Retention <= 0 {
		cfg.Retention = defaultStatsWorkerRetention
	}
	if cfg.Batch <= 0 || cfg.Batch > maxQueryIDs {
		cfg.Batch = maxQueryIDs
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &StatsWorker{cfg: cfg}
}

// Run sweeps on the configured interval until ctx is done.
func (w *StatsWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.SweepOnce(ctx); err != nil {
				w.cfg.Logger.WarnContext(ctx, "stats sweep failed", "error", err.Error())
			}
		}
	}
}

// SweepOnce walks every connected account and prunes old points. A per-account
// failure is logged and skipped so one bad token cannot stall the rest.
func (w *StatsWorker) SweepOnce(ctx context.Context) error {
	accounts, err := w.cfg.Lister.List(ctx, w.cfg.OperatorID)
	if err != nil {
		return fmt.Errorf("insight: listing accounts for the stats sweep: %w", err)
	}
	for _, accountID := range accounts {
		if err := ctx.Err(); err != nil {
			return err
		}
		w.sweepAccount(ctx, accountID)
	}
	cutoff := w.cfg.Now().Add(-w.cfg.Retention)
	if _, err := w.cfg.Store.DeleteStatsBefore(ctx, cutoff); err != nil {
		return fmt.Errorf("insight: pruning video stats: %w", err)
	}
	return nil
}

// sweepAccount refreshes one token, lists its newest videos, and appends a
// point per video. A spent list budget ends this account early and resumes on
// the next tick.
func (w *StatsWorker) sweepAccount(ctx context.Context, accountID string) {
	log := w.cfg.Logger.With("account_id", accountID)
	if err := w.cfg.Refresher.Refresh(ctx, w.cfg.OperatorID, accountID); err != nil {
		log.WarnContext(ctx, "refreshing the account token, skipping account", "error", err.Error())
		return
	}
	access, err := w.cfg.Accounts.Owned(ctx, w.cfg.OperatorID, accountID)
	if err != nil {
		log.WarnContext(ctx, "resolving the access token, skipping account", "error", err.Error())
		return
	}
	ids := w.listVideoIDs(ctx, accountID, access, log)
	if len(ids) > 0 {
		w.appendStats(ctx, access, ids, log)
	}
}

// listVideoIDs pages the newest videos until the catalog ends or the shared
// list budget is spent. A spent budget returns what it already listed.
func (w *StatsWorker) listVideoIDs(ctx context.Context, accountID, access string, log *slog.Logger) []string {
	var ids []string
	cursor := ""
	for {
		if err := w.cfg.Gate.Allow(ctx, accountID); err != nil {
			log.WarnContext(ctx, "list budget spent, resuming on the next tick", "error", err.Error())
			return ids
		}
		page, err := w.cfg.Videos.ListVideos(ctx, access, cursor, 0)
		if err != nil {
			log.WarnContext(ctx, "listing videos", "error", err.Error())
			return ids
		}
		for _, video := range page.Videos {
			ids = append(ids, video.ID)
		}
		if !page.HasMore || page.Cursor == 0 {
			return ids
		}
		cursor = strconv.FormatInt(page.Cursor, 10)
	}
}

// appendStats queries the collected ids in batches and appends one point per
// video. A failed batch is logged and skipped, so one bad id cannot drop the rest.
func (w *StatsWorker) appendStats(ctx context.Context, access string, ids []string, log *slog.Logger) {
	for start := 0; start < len(ids); start += w.cfg.Batch {
		end := min(start+w.cfg.Batch, len(ids))
		videos, err := w.cfg.Videos.QueryVideos(ctx, access, ids[start:end], videoQueryFields)
		if err != nil {
			log.WarnContext(ctx, "querying video stats", "error", err.Error())
			continue
		}
		for _, video := range videos {
			if _, err := w.cfg.Store.InsertStats(ctx, snapshotFromVideo(video, w.cfg.Now())); err != nil {
				log.WarnContext(ctx, "appending video stats", "video_id", video.ID, "error", err.Error())
			}
		}
	}
}
