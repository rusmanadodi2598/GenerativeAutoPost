// Package media stores uploaded object bytes behind one interface.
//
// @file      internal/media/s3.go
// @for       Object store adapter that talks to buckets through ObjectClient.
// @uses      io, strings, internal/apierror
// @reason    The concrete ObjectClient arrives when a real R2 client is wired in Task 12, so this adapter stays thin and testable without an AWS SDK.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-12
package media

import (
	"bytes"
	"context"
	"io"
	"strings"
	"time"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"
)

// ObjectClient is the narrow PutObject/GetObject/DeleteObject surface the S3
// adapter needs. A fake implements it in tests; production wires a real R2
// client in Task 12.
type ObjectClient interface {
	PutObject(ctx context.Context, bucket, key string, body []byte) error
	GetObject(ctx context.Context, bucket, key string) ([]byte, error)
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

// Put buffers the stream and writes it to bucket/keyForID(id).
func (s *S3) Put(ctx context.Context, obj MediaObject, r io.Reader) error {
	if !validID(obj.ID) {
		return apierror.New(apierror.CodeValidationFailed, 0, "media id is invalid")
	}
	body, err := io.ReadAll(r)
	if err != nil {
		return apierror.New(apierror.CodeMediaNotStored, 0, "media object could not be stored").Wrap(err)
	}
	if err := s.client.PutObject(ctx, s.bucket, keyForID(obj.ID), body); err != nil {
		return apierror.New(apierror.CodeMediaNotStored, 0, "media object could not be stored").Wrap(err)
	}
	return nil
}

// Get fetches the key bytes and returns them as a read closer.
func (s *S3) Get(ctx context.Context, id string) (MediaObject, io.ReadCloser, error) {
	if !validID(id) {
		return MediaObject{}, nil, apierror.New(apierror.CodeValidationFailed, 0, "media id is invalid")
	}
	body, err := s.client.GetObject(ctx, s.bucket, keyForID(id))
	if err != nil {
		return MediaObject{}, nil, apierror.New(apierror.CodeNotFound, 0, "media object not found").Wrap(err)
	}
	return MediaObject{
		ID:        id,
		ByteSize:  int64(len(body)),
		Storage:   MediaStorageS3,
		CreatedAt: time.Now().UTC(),
	}, io.NopCloser(bytes.NewReader(body)), nil
}

// Delete removes the key from the bucket.
func (s *S3) Delete(ctx context.Context, id string) error {
	if !validID(id) {
		return apierror.New(apierror.CodeValidationFailed, 0, "media id is invalid")
	}
	if err := s.client.DeleteObject(ctx, s.bucket, keyForID(id)); err != nil {
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
