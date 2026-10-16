// Package compose builds the runnable engine over one validated config.
//
// @file      internal/compose/build.go
// @for       Constructing every module service and handler for production.
// @uses      log/slog, github.com/gin-gonic/gin, github.com/jackc/pgx/v5/pgxpool, github.com/redis/go-redis/v9, internal/config
// @reason    The composition root owns every constructor call, so main stays wiring only.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     router
// @stability stable
// @since     2026-10-15
package compose

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/account"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/auth"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/config"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/insight"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/media"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/platform/crypto"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/platform/httpapi"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/platform/idempotency"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/platform/ratelimit"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/publish"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/tiktokclient"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/webhook"
)

// idempotencyTTL matches the 003 section 10 retention window.
const idempotencyTTL = 24 * time.Hour

// App is the wired process surface: the engine plus the background workers.
type App struct {
	Engine      *gin.Engine
	Reconciler  *webhook.Reconciler
	Worker      *webhook.Worker
	AttemptRows *publish.Repository
}

// Build assembles every module service and mounts its routes. A construction
// fault fails here rather than on the first request.
func Build(cfg config.Config, pool *pgxpool.Pool, client *redis.Client, logger *slog.Logger) (*App, error) {
	if logger == nil {
		logger = slog.Default()
	}
	keyring, err := crypto.NewKeyring(map[string][]byte{cfg.TokenEncKeyID: cfg.TokenEncKey})
	if err != nil {
		return nil, fmt.Errorf("compose: building keyring: %w", err)
	}
	tiktok := tiktokclient.New(tiktokclient.Config{
		ClientKey:    cfg.TikTokClientKey,
		ClientSecret: cfg.TikTokClientSecret,
		RedirectURI:  cfg.TikTokRedirectURI,
	})
	limiter := tiktokclient.NewLimiter(client, logger)
	accounts := account.NewService(account.NewRepository(pool), keyring, cfg.TokenEncKeyID, logger)
	sessions := auth.NewSessionStore(client, cfg.SessionTTL)
	authSvc := auth.NewService(auth.ServiceConfig{
		ClientKey:    cfg.TikTokClientKey,
		RedirectURI:  cfg.TikTokRedirectURI,
		Scopes:       cfg.TikTokScopes,
		AppUIBaseURL: cfg.MediaPublicBaseURL,
		OperatorID:   cfg.OperatorID,
	}, tiktok, tiktok, accounts, auth.NewStateStore(client), sessions,
		auth.NewRedisLocker(client), logger)
	session := operatorSession()

	attempts := publish.NewRepository(pool)
	idempotencyStore := idempotency.New(client, idempotencyTTL)
	insightSvc := insight.NewService(insight.Config{
		Videos: tiktok, Accounts: accounts, Store: insight.NewRepository(pool),
	})
	publishSvc := publish.NewService(publish.Config{
		Poster: tiktok, Accounts: accounts, Creators: creatorOptions{insightSvc},
		Attempts: attempts, Idempotency: idempotencyStore,
		MediaHost: cfg.MediaPublicBaseURL,
	}).WithChunkProxy(publish.ChunkServiceConfig{
		Attempts: attempts, Chunks: attempts, Plans: publish.NewMemoryPlanStore(),
		Uploader: tiktok, TTL: time.Hour,
	})
	store, err := mediaStore(cfg)
	if err != nil {
		return nil, err
	}
	mediaSvc := media.NewService(store, media.ServiceConfig{
		Driver: cfg.MediaDriver, MaxBytes: cfg.MediaMaxBytes,
		MaxDurationMs: cfg.MediaMaxDurationMs, AllowedTypes: cfg.MediaAllowedTypes,
		PublicBaseURL: cfg.MediaPublicBaseURL, Root: cfg.MediaRoot,
	}, logger)

	webhookStore := webhook.NewStore(pool)
	dispatcher := webhook.NewDispatcher(webhookStore, publishSvc, txBinder{repo: attempts},
		webhook.DispatcherConfig{OutboxAggregate: "publish_attempt"}, logger)
	worker := webhook.NewWorker(dispatcher, webhook.WorkerConfig{Size: cfg.WorkerSize}, logger)
	reconciler := webhook.NewReconciler(webhookStore, dispatcher, publishSvc,
		newReconcileReader(attempts, accounts), newStatusAdapter(tiktok),
		newGateAdapter(limiter, tiktokclient.FamilyStatus), attempts,
		webhook.ReconcilerConfig{}, logger)
	reconciler.SetEventSink(newStreamSink(client, logger))

	engine := httpapi.New(logger).Engine()
	Mount(engine, Routes{
		Auth: func(r gin.IRouter) {
			auth.RegisterRoutes(r, authSvc, sessions,
				auth.RouterConfig{CookieName: cfg.SessionCookieName, CookieTTL: cfg.SessionTTL})
		},
		Accounts: func(r gin.IRouter) {
			account.RegisterRoutes(r, account.NewHandler(accounts, session, auth.RequireCSRF()))
		},
		Media: func(r gin.IRouter) {
			media.RegisterRoutes(r, media.NewHandler(mediaSvc, store, idempotencyStore, session))
		},
		Publish: func(r gin.IRouter) {
			publish.RegisterRoutes(r, publish.NewHandler(publishSvc, attempts, publishSvc, session, publish.HandlerConfig{MaxChunkBytes: cfg.ChunkMaxBytes}))
		},
		Insight: func(r gin.IRouter) { insight.RegisterRoutes(r, insight.NewHandler(insightSvc, session)) },
		Webhook: func(r gin.IRouter) {
			window := ratelimit.NewWindow(client, cfg.WebhookThrottleMax, cfg.WebhookThrottleWnd, logger)
			webhook.RegisterRoutes(r, webhook.NewHandler(dispatcher, webhook.HandlerConfig{
				Secret: cfg.TikTokClientSecret,
			}, logger), webhook.Throttle(webhook.NewIPWindow(window), logger))
		},
	})
	return &App{Engine: engine, Reconciler: reconciler, Worker: worker, AttemptRows: attempts}, nil
}

// operatorSession resolves the operator id the console routes guard on.
func operatorSession() func(*gin.Context) (string, bool) {
	return func(c *gin.Context) (string, bool) {
		sess, ok := auth.SessionFrom(c)
		if !ok {
			return "", false
		}
		return sess.OperatorID, true
	}
}

// creatorOptions adapts the insight service to the publish privacy port.
type creatorOptions struct {
	svc *insight.Service
}

// Options returns the account privacy levels, cache-first.
func (c creatorOptions) Options(ctx context.Context, operatorID, accountID string) ([]string, error) {
	return c.svc.CreatorOptions(ctx, operatorID, accountID)
}
