// Package compose selects the media backend from config.
//
// @file      internal/compose/media_store.go
// @for       Media Store construction for the disk and R2 backends.
// @uses      context, time, internal/config, internal/media, internal/platform/r2
// @reason    The driver env decides which backend the upload service writes to.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     router
// @stability stable
// @since     2026-10-16
package compose

import (
	"context"
	"fmt"
	"time"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/config"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/media"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/platform/r2"
)

// mediaStoreCheckTimeout bounds the boot-time R2 credential probe.
const mediaStoreCheckTimeout = 10 * time.Second

// mediaStore builds the configured Store. The S3 backend signs with the R2
// credentials and proves them with a write probe at boot, so a bad key or a bad
// endpoint fails the process instead of the first upload.
func mediaStore(cfg config.Config) (media.Store, error) {
	switch cfg.MediaDriver {
	case config.DriverDisk:
		return media.NewDisk(cfg.MediaRoot), nil
	case config.DriverS3:
		client, err := r2.New(r2.Config{
			Endpoint:  cfg.S3Endpoint,
			Bucket:    cfg.S3Bucket,
			AccessKey: cfg.S3AccessKey,
			SecretKey: cfg.S3SecretKey,
			Region:    cfg.S3Region,
		})
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), mediaStoreCheckTimeout)
		defer cancel()
		if err := client.Check(ctx); err != nil {
			return nil, fmt.Errorf("compose: R2 connectivity check failed: %w", err)
		}
		return media.NewS3(client, cfg.S3Bucket, cfg.MediaPublicBaseURL), nil
	default:
		return nil, fmt.Errorf("compose: unknown media driver %q", cfg.MediaDriver)
	}
}
