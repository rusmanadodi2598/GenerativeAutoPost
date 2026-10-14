// Package media stores uploaded object bytes behind one interface.
//
// @file      internal/media/disk.go
// @for       Local disk store that keeps one file per object id.
// @uses      os, path/filepath, internal/apierror
// @reason    Gives developers and small deploys a hermetic object store.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-12
package media

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// Disk keeps object bytes under one root directory, one file per id.
type Disk struct {
	root string
}

// NewDisk points a store at root. Files appear only on Put.
func NewDisk(root string) *Disk {
	return &Disk{root: root}
}

// Put writes the full stream to root/id, creating the root on demand.
func (d *Disk) Put(ctx context.Context, obj MediaObject, r io.Reader) error {
	if !validID(obj.ID) {
		return apierror.New(apierror.CodeValidationFailed, 0, "media id is invalid")
	}
	if err := os.MkdirAll(d.root, 0o755); err != nil {
		return apierror.New(apierror.CodeMediaNotStored, 0, "media store root is unavailable").Wrap(err)
	}
	dst, err := os.OpenFile(filepath.Join(d.root, keyForID(obj.ID)), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return apierror.New(apierror.CodeMediaNotStored, 0, "media object could not be stored").Wrap(err)
	}
	defer func() { _ = dst.Close() }()
	if _, err := io.Copy(dst, r); err != nil {
		return apierror.New(apierror.CodeMediaNotStored, 0, "media object could not be stored").Wrap(err)
	}
	return nil
}

// Get opens the stored file and returns its descriptor as the object.
func (d *Disk) Get(ctx context.Context, id string) (MediaObject, io.ReadCloser, error) {
	if !validID(id) {
		return MediaObject{}, nil, apierror.New(apierror.CodeValidationFailed, 0, "media id is invalid")
	}
	f, err := os.Open(filepath.Join(d.root, keyForID(id)))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return MediaObject{}, nil, apierror.New(apierror.CodeNotFound, 0, "media object not found").Wrap(errObjectMissing)
		}
		return MediaObject{}, nil, apierror.New(apierror.CodeMediaNotStored, 0, "media object could not be read").Wrap(err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return MediaObject{}, nil, apierror.New(apierror.CodeMediaNotStored, 0, "media object could not be read").Wrap(err)
	}
	return MediaObject{ID: id, ByteSize: info.Size(), Storage: MediaStorageDisk, CreatedAt: info.ModTime().UTC()}, f, nil
}

// Delete removes the stored file. A missing file is a no-op.
func (d *Disk) Delete(ctx context.Context, id string) error {
	if !validID(id) {
		return apierror.New(apierror.CodeValidationFailed, 0, "media id is invalid")
	}
	if err := os.Remove(filepath.Join(d.root, keyForID(id))); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return apierror.New(apierror.CodeMediaNotStored, 0, "media object could not be deleted").Wrap(err)
	}
	return nil
}

// SignPublicURL returns empty: the disk store is private by default.
func (d *Disk) SignPublicURL(ctx context.Context, id string) (string, error) {
	if !validID(id) {
		return "", apierror.New(apierror.CodeValidationFailed, 0, "media id is invalid")
	}
	return "", nil
}
