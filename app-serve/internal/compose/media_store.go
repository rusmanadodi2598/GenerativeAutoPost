// Package compose selects the media backend from config.
//
// @file      internal/compose/media_store.go
// @for       Media Store construction for the disk and object backends.
// @uses      fmt, internal/config, internal/media
// @reason    The driver env decides which backend the upload service writes to.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     router
// @stability stable
// @since     2026-10-16
package compose

import (
	"fmt"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/config"
	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/media"
)

// mediaStore builds the configured Store. The S3 backend needs an object
// client that ships with the R2 credentials, so asking for it before that
// client exists fails loudly instead of silently writing to disk.
func mediaStore(cfg config.Config) (media.Store, error) {
	switch cfg.MediaDriver {
	case config.DriverDisk:
		return media.NewDisk(cfg.MediaRoot), nil
	case config.DriverS3:
		return nil, fmt.Errorf("compose: MEDIA_DRIVER=s3 has no object client wired yet")
	default:
		return nil, fmt.Errorf("compose: unknown media driver %q", cfg.MediaDriver)
	}
}
