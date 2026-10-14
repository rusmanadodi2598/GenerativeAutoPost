// Package media stores uploaded object bytes behind one interface.
//
// @file      internal/media/store.go
// @for       Store interface and the shared object key helpers.
// @uses      context, errors, io
// @reason    Lets disk and object backends swap without touching the service.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-12
package media

import (
	"context"
	"errors"
	"io"
	"strings"
)

// errObjectMissing marks a Get for an id no backend has stored.
var errObjectMissing = errors.New("media: object not found")

// Store persists raw object bytes and signs public URLs for them.
type Store interface {
	Put(ctx context.Context, obj MediaObject, r io.Reader) error
	Get(ctx context.Context, id string) (MediaObject, io.ReadCloser, error)
	Delete(ctx context.Context, id string) error
	SignPublicURL(ctx context.Context, id string) (string, error)
}

// keyForID maps one object id to its backend key. The mapping is the
// identity so an id always resolves to the same key on every backend.
func keyForID(id string) string {
	return id
}

// validID rejects path separators so an id can never escape its root.
func validID(id string) bool {
	return id != "" && !strings.Contains(id, "/") && !strings.Contains(id, "\\") &&
		!strings.Contains(id, "..")
}
