// Package media stores uploaded object bytes behind one interface.
//
// @file      internal/media/s3.go
// @for       Object store adapter that talks to buckets through ObjectClient.
// @uses      context, bytes, crypto/sha256, encoding/json, errors, io, strings, time, internal/apierror
// @reason    Keeps the store contract thin so disk and object backends swap without touching the service.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-12
package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// maxSidecarBytes caps a sidecar read so a bad backend cannot stream an
// unbounded metadata blob into memory. Real sidecars are a few hundred bytes.
const maxSidecarBytes = 1 << 20

// ObjectClient is the narrow PutObject/GetObject/DeleteObject surface the S3
// adapter needs. PutObject takes the body's SHA-256 because the client signs
// the request before the stream is sent and must not buffer it to hash it.
type ObjectClient interface {
	PutObject(ctx context.Context, bucket, key string, body io.Reader, size int64, contentSHA256 string) error
	GetObject(ctx context.Context, bucket, key string) (io.ReadCloser, error)
	DeleteObject(ctx context.Context, bucket, key string) error
}

// S3 adapts an ObjectClient to the Store interface for one bucket.
type S3 struct {
	client     ObjectClient
	bucket     string
	publicBase string
}

// NewS3 pins the adapter to one bucket and one public URL base.
func NewS3(client ObjectClient, bucket, publicBase string) *S3 {
	return &S3{client: client, bucket: bucket, publicBase: strings.TrimSuffix(publicBase, "/")}
}

// notFoundMarker lets an ObjectClient report a miss without media importing
// the client package.
type notFoundMarker interface {
	NotFound() bool
}

// notFoundError marks a client failure that means the key was never stored.
// Only such errors become NOT_FOUND; any other client failure is a storage
// failure so a broken backend is never reported as a clean missing object.
func notFoundError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errObjectMissing) {
		return true
	}
	var marker notFoundMarker
	if errors.As(err, &marker) && marker.NotFound() {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "not found") ||
		strings.Contains(strings.ToLower(err.Error()), "no such")
}

// Put streams the object to bucket/keyForID(id), then writes a sidecar key
// holding the object's metadata. The stream is not buffered here: the caller's
// recorded size and checksum let the client sign and send it as it arrives.
func (s *S3) Put(ctx context.Context, obj MediaObject, r io.Reader) error {
	if !validID(obj.ID) {
		return apierror.New(apierror.CodeValidationFailed, 0, "media id is invalid")
	}
	if obj.ByteSize <= 0 || obj.ChecksumSHA256 == "" {
		return apierror.New(apierror.CodeMediaNotStored, 0, "media object metadata is incomplete")
	}
	if err := s.client.PutObject(ctx, s.bucket, keyForID(obj.ID), r, obj.ByteSize, obj.ChecksumSHA256); err != nil {
		return apierror.New(apierror.CodeMediaNotStored, 0, "media object could not be stored").Wrap(err)
	}
	meta, err := json.Marshal(obj)
	if err != nil {
		return apierror.New(apierror.CodeMediaNotStored, 0, "media object could not be stored").Wrap(err)
	}
	sum := sha256.Sum256(meta)
	if err := s.client.PutObject(ctx, s.bucket, keyForID(obj.ID)+sidecarSuffix, bytes.NewReader(meta), int64(len(meta)), hex.EncodeToString(sum[:])); err != nil {
		return apierror.New(apierror.CodeMediaNotStored, 0, "media object could not be stored").Wrap(err)
	}
	return nil
}

// readSidecar loads the object's recorded metadata. A missing sidecar is not
// fatal and yields an empty object, so an object with no recorded owner is
// denied on read. Any other failure returns an error, so a broken backend is a
// storage fault rather than a clean miss.
func (s *S3) readSidecar(ctx context.Context, id string) (MediaObject, error) {
	body, err := s.client.GetObject(ctx, s.bucket, keyForID(id)+sidecarSuffix)
	if err != nil {
		if notFoundError(err) {
			return MediaObject{}, nil
		}
		return MediaObject{}, err
	}
	defer func() { _ = body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(body, maxSidecarBytes))
	if err != nil {
		return MediaObject{}, err
	}
	var obj MediaObject
	if err := json.Unmarshal(raw, &obj); err != nil {
		return MediaObject{}, err
	}
	return obj, nil
}

// Get returns the key stream plus the metadata sidecar recorded with it.
func (s *S3) Get(ctx context.Context, id string) (MediaObject, io.ReadCloser, error) {
	if !validID(id) {
		return MediaObject{}, nil, apierror.New(apierror.CodeValidationFailed, 0, "media id is invalid")
	}
	body, err := s.client.GetObject(ctx, s.bucket, keyForID(id))
	if err != nil {
		if notFoundError(err) {
			return MediaObject{}, nil, apierror.New(apierror.CodeNotFound, 0, "media object not found").Wrap(err)
		}
		return MediaObject{}, nil, apierror.New(apierror.CodeMediaNotStored, 0, "media object could not be read").Wrap(err)
	}
	obj, err := s.readSidecar(ctx, id)
	if err != nil {
		_ = body.Close()
		return MediaObject{}, nil, apierror.New(apierror.CodeMediaNotStored, 0, "media object could not be read").Wrap(err)
	}
	obj.ID = id
	if obj.Storage == "" {
		obj.Storage = MediaStorageS3
	}
	if obj.CreatedAt.IsZero() {
		obj.CreatedAt = time.Now().UTC()
	}
	return obj, body, nil
}

// Delete removes the key from the bucket.
func (s *S3) Delete(ctx context.Context, id string) error {
	if !validID(id) {
		return apierror.New(apierror.CodeValidationFailed, 0, "media id is invalid")
	}
	if err := s.client.DeleteObject(ctx, s.bucket, keyForID(id)); err != nil && !notFoundError(err) {
		return apierror.New(apierror.CodeMediaNotStored, 0, "media object could not be deleted").Wrap(err)
	}
	if err := s.client.DeleteObject(ctx, s.bucket, keyForID(id)+sidecarSuffix); err != nil && !notFoundError(err) {
		return apierror.New(apierror.CodeMediaNotStored, 0, "media object could not be deleted").Wrap(err)
	}
	return nil
}

// SignPublicURL joins the public base with the object key. An empty base
// means no public URL is served, so it returns empty without an error.
func (s *S3) SignPublicURL(ctx context.Context, id string) (string, error) {
	if !validID(id) {
		return "", apierror.New(apierror.CodeValidationFailed, 0, "media id is invalid")
	}
	if s.publicBase == "" {
		return "", nil
	}
	return s.publicBase + "/" + keyForID(id), nil
}
