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
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// sidecarSuffix names the metadata file kept beside each object's bytes.
const sidecarSuffix = ".json"

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
	if _, err := d.copy(ctx, dst, r); err != nil {
		return apierror.New(apierror.CodeMediaNotStored, 0, "media object could not be stored").Wrap(err)
	}
	if err := d.writeSidecar(obj); err != nil {
		return err
	}
	return nil
}

// copy streams r into dst, stopping as soon as the request context is done so
// a cancelled upload does not keep writing bytes to disk.
func (d *Disk) copy(ctx context.Context, dst io.Writer, r io.Reader) (int64, error) {
	buf := make([]byte, 32*1024)
	var written int64
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		n, readErr := r.Read(buf)
		if n > 0 {
			m, err := dst.Write(buf[:n])
			written += int64(m)
			if err != nil {
				return written, err
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return written, nil
			}
			return written, readErr
		}
	}
}

// writeSidecar persists the object's metadata next to its bytes.
// The sidecar exists because the disk store has no metadata table: without it
// a later Get cannot recover the owner or content type from the file alone.
func (d *Disk) writeSidecar(obj MediaObject) error {
	body, err := json.Marshal(obj)
	if err != nil {
		return apierror.New(apierror.CodeMediaNotStored, 0, "media object could not be stored").Wrap(err)
	}
	path := filepath.Join(d.root, keyForID(obj.ID)+sidecarSuffix)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return apierror.New(apierror.CodeMediaNotStored, 0, "media object could not be stored").Wrap(err)
	}
	return nil
}

// readSidecar loads the stored metadata. A missing sidecar is not fatal: the
// object is then treated as unowned so access is denied rather than granted.
func (d *Disk) readSidecar(id string) MediaObject {
	var obj MediaObject
	body, err := os.ReadFile(filepath.Join(d.root, keyForID(id)+sidecarSuffix))
	if err != nil {
		return MediaObject{}
	}
	_ = json.Unmarshal(body, &obj)
	return obj
}

// Get opens the stored file and merges the recorded metadata, so the returned
// object carries the owner the sidecar recorded.
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
	obj := d.readSidecar(id)
	obj.ID = id
	obj.ByteSize = info.Size()
	if obj.Storage == "" {
		obj.Storage = MediaStorageDisk
	}
	if obj.CreatedAt.IsZero() {
		obj.CreatedAt = info.ModTime().UTC()
	}
	return obj, f, nil
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
	sidecar := filepath.Join(d.root, keyForID(id)+sidecarSuffix)
	if err := os.Remove(sidecar); err != nil && !errors.Is(err, os.ErrNotExist) {
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
