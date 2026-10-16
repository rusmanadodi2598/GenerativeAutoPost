// Package compose builds the runnable HTTP engine over one config.
//
// @file      internal/compose/media_store_test.go
// @for       Boot-time R2 credential check tests for the media store.
// @uses      net/http, net/http/httptest, testing, internal/config
// @reason    A bad R2 credential must fail the process, not the first upload.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     router
// @stability stable
// @since     2026-10-16
package compose

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kentangtech/GenerativeAutoPost/app-serve/internal/config"
)

// s3Config points the S3 driver at one test endpoint.
func s3Config(endpoint string) config.Config {
	return config.Config{
		MediaDriver: config.DriverS3, S3Endpoint: endpoint, S3Bucket: "media",
		S3AccessKey: "key", S3SecretKey: "secret", S3Region: "auto",
	}
}

// TestMediaStoreFailsOnDeniedR2Credentials proves a rejected HeadBucket stops
// the build instead of surfacing on the first upload.
func TestMediaStoreFailsOnDeniedR2Credentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	if _, err := mediaStore(s3Config(server.URL)); err == nil {
		t.Fatal("mediaStore accepted an endpoint that denied the credentials")
	}
}

// TestMediaStoreBuildsOnReachableBucket proves a 2xx HeadBucket yields a store.
func TestMediaStoreBuildsOnReachableBucket(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	store, err := mediaStore(s3Config(server.URL))
	if err != nil {
		t.Fatalf("mediaStore: %v", err)
	}
	if store == nil {
		t.Fatal("mediaStore returned a nil store")
	}
}
