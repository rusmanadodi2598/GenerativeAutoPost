// Command app-serve boots the API process and serves HTTP until SIGTERM.
//
// @file      cmd/app-serve/main.go
// @for       Process entrypoint wiring config, pools, and the server.
// @uses      internal/compose, internal/config, platform packages, net/http
// @reason    Composition root with no business logic of its own.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     config
// @stability stable
// @since     2026-10-12
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/compose"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/config"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/platform/logging"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/platform/postgres"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/platform/redisx"
)

func main() {
	if err := run(); err != nil {
		slog.Error("app-serve failed", "error", err)
		os.Exit(1)
	}
}

// run loads config, migrates when asked, wires dependencies, and blocks until
// SIGTERM.
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := logging.New(cfg)
	slog.SetDefault(logger)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := postgres.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()

	if cfg.RunMigrations {
		if err := postgres.Up(ctx, pool, cfg.MigrationsDir); err != nil {
			return err
		}
		logger.Info("app-serve migrations applied")
	}

	client, err := redisx.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() {
		_ = client.Close()
	}()

	app, err := compose.Build(cfg, pool, client, logger)
	if err != nil {
		return err
	}

	work, stopWorkers := context.WithCancel(context.Background())
	defer stopWorkers()
	var workers sync.WaitGroup
	workers.Add(3)
	go supervised(&workers, logger, "webhook worker", func() {
		defer workers.Done()
		app.Worker.Run(work)
	})
	go supervised(&workers, logger, "reconcile sweep", func() {
		defer workers.Done()
		app.Reconciler.Run(work)
	})
	go supervised(&workers, logger, "outbox relay", func() {
		defer workers.Done()
		app.Reconciler.RunOutbox(work)
	})

	srv := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           app.Engine,
		ReadHeaderTimeout: 5 * time.Second,
	}

	stopped := make(chan os.Signal, 1)
	signal.Notify(stopped, syscall.SIGTERM, syscall.SIGINT)

	serveErr := make(chan error, 1)
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				logger.Error("serve panic recovered", "panic", rec)
				serveErr <- errors.New("http serve panic")
			}
		}()
		logger.Info("app-serve listening", "addr", cfg.Addr(), "env", cfg.AppEnv)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		} else {
			serveErr <- nil
		}
	}()

	select {
	case sig := <-stopped:
		logger.Info("shutdown signal", "signal", sig.String())
	case err := <-serveErr:
		return err
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	stopWorkers()
	workers.Wait()
	return <-serveErr
}

// supervised runs one background worker and recovers a panic into a log, since
// an unrecovered goroutine kills the whole process per AGENTS.md 1.6.
func supervised(workers *sync.WaitGroup, logger *slog.Logger, name string, run func()) {
	defer func() {
		if rec := recover(); rec != nil {
			logger.Error("background worker panic recovered", "worker", name, "panic", rec)
		}
	}()
	run()
}
