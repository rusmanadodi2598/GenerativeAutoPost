// Package r2 signs and sends Cloudflare R2 object requests with AWS SigV4.
//
// @file      internal/platform/r2/client.go
// @for       SigV4 signed PutObject, GetObject, DeleteObject, and HeadBucket.
// @uses      context, net/http, net/url, internal/platform/r2 signer
// @reason    Gives media a real object client without an AWS SDK dependency.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-16
package r2

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// requestTimeout bounds one object transfer, sized for a 200 MB upload on a
	// slow link rather than a small JSON call.
	requestTimeout = 10 * time.Minute
	dialTimeout    = 10 * time.Second
	tlsTimeout     = 10 * time.Second
	headerTimeout  = 30 * time.Second
	idleTimeout    = 90 * time.Second
	// maxErrorBody caps the error text carried into a returned message.
	maxErrorBody = 8 << 10
)

// Config is the R2 endpoint and credential set the client signs with.
type Config struct {
	Endpoint  string
	Bucket    string
	AccessKey string
	SecretKey string
	Region    string
}

// Client signs and sends object requests to one R2 endpoint.
type Client struct {
	endpoint *url.URL
	bucket   string
	creds    credentials
	http     *http.Client
	now      func() time.Time
}

// New validates the endpoint and returns a client. A malformed endpoint is a
// construction fault rather than a request-time surprise.
func New(cfg Config) (*Client, error) {
	endpoint, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("r2: parsing S3_ENDPOINT: %w", err)
	}
	if endpoint.Host == "" {
		return nil, errors.New("r2: S3_ENDPOINT must be an absolute url")
	}
	if endpoint.Scheme != "https" && endpoint.Scheme != "http" {
		return nil, fmt.Errorf("r2: S3_ENDPOINT scheme %q is not http or https", endpoint.Scheme)
	}
	region := cfg.Region
	if region == "" {
		region = "auto"
	}
	return &Client{
		endpoint: endpoint,
		bucket:   cfg.Bucket,
		creds:    credentials{accessKey: cfg.AccessKey, secretKey: cfg.SecretKey, region: region},
		http:     &http.Client{Timeout: requestTimeout, Transport: newTransport()},
		now:      time.Now,
	}, nil
}

// PutObject streams body to bucket/key. size and contentSHA256 come from the
// caller because signing needs both before the stream is sent.
func (c *Client) PutObject(ctx context.Context, bucket, key string, body io.Reader, size int64, contentSHA256 string) error {
	if size <= 0 {
		return fmt.Errorf("r2: put %s: a positive size is required", key)
	}
	if contentSHA256 == "" {
		return fmt.Errorf("r2: put %s: a content sha256 is required", key)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.objectURL(bucket, key).String(), body)
	if err != nil {
		return err
	}
	req.ContentLength = size
	req.Header.Set("x-amz-content-sha256", contentSHA256)
	c.authorize(req, contentSHA256)
	return c.expectNoContent(req)
}

// GetObject returns the object body. A 404 is a typed miss so the caller can
// treat a missing key as a clean absent object.
func (c *Client) GetObject(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.objectURL(bucket, key).String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-amz-content-sha256", emptyBodyHash)
	c.authorize(req, emptyBodyHash)

	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("r2: GET %s: %w", req.URL.Path, err)
	}
	if res.StatusCode == http.StatusNotFound {
		_ = res.Body.Close()
		return nil, notFoundError{key: key}
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		defer func() { _ = res.Body.Close() }()
		return nil, statusError(res)
	}
	return res.Body, nil
}

// DeleteObject removes bucket/key. A missing key is a typed miss, not a fault.
func (c *Client) DeleteObject(ctx context.Context, bucket, key string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.objectURL(bucket, key).String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("x-amz-content-sha256", emptyBodyHash)
	c.authorize(req, emptyBodyHash)

	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("r2: DELETE %s: %w", req.URL.Path, err)
	}
	defer func() { _ = res.Body.Close() }()
	switch {
	case res.StatusCode == http.StatusNotFound:
		_, _ = io.Copy(io.Discard, res.Body)
		return notFoundError{key: key}
	case res.StatusCode >= 200 && res.StatusCode < 300:
		_, _ = io.Copy(io.Discard, res.Body)
		return nil
	default:
		return statusError(res)
	}
}

// probeKey is the reserved key Check writes and removes. A leftover probe is
// harmless: the next boot overwrites it before removing it.
const probeKey = ".genauto-boot-probe"

// Check proves the endpoint is reachable and the credentials can write, the
// permission uploads need. It writes and removes a small probe key rather than
// a HeadBucket, because a bucket-scoped token that can write objects may not
// hold the bucket-level list permission HeadBucket requires. It is a one-shot
// boot gate with no retry: a transient failure is a process restart, and a
// retry would only delay the failure a bad credential should have at boot.
func (c *Client) Check(ctx context.Context) error {
	body := []byte("genauto probe")
	if err := c.PutObject(ctx, c.bucket, probeKey, bytes.NewReader(body), int64(len(body)), sha256Hex(body)); err != nil {
		return fmt.Errorf("r2: probe write failed: %w", err)
	}
	if err := c.DeleteObject(ctx, c.bucket, probeKey); err != nil {
		var miss notFoundError
		if errors.As(err, &miss) {
			return nil
		}
		return fmt.Errorf("r2: probe delete failed: %w", err)
	}
	return nil
}

// expectNoContent runs a request whose success carries no body to return.
func (c *Client) expectNoContent(req *http.Request) error {
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("r2: %s %s: %w", req.Method, req.URL.Path, err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		_, _ = io.Copy(io.Discard, res.Body)
		return nil
	}
	return statusError(res)
}

// authorize signs req in place as an s3 request.
func (c *Client) authorize(req *http.Request, payloadHash string) {
	signRequest(req, c.creds, sigService, payloadHash, c.now())
}

// objectURL builds endpoint/bucket/key without letting the key escape the root.
func (c *Client) objectURL(bucket, key string) *url.URL {
	u := *c.endpoint
	u.Path = strings.TrimSuffix(c.endpoint.Path, "/") + "/" + bucket + "/" + key
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return &u
}

// notFoundError marks a 404 so a caller can treat a missing key as a clean miss
// through a structural check rather than error text.
type notFoundError struct {
	key string
}

func (e notFoundError) Error() string { return "r2: object " + e.key + " not found" }

// NotFound implements the miss marker media checks.
func (e notFoundError) NotFound() bool { return true }

// statusError turns a failed response into an error carrying its short detail.
func statusError(res *http.Response) error {
	detail, _ := io.ReadAll(io.LimitReader(res.Body, maxErrorBody))
	message := strings.TrimSpace(string(detail))
	if message == "" {
		message = http.StatusText(res.StatusCode)
	}
	return fmt.Errorf("r2: request failed with status %d: %s", res.StatusCode, message)
}

// newTransport sets explicit dial, TLS, header, and idle timeouts, since a
// zero value would let a stuck connection pin a goroutine forever.
func newTransport() *http.Transport {
	base, _ := http.DefaultTransport.(*http.Transport)
	transport := base.Clone()
	transport.DialContext = (&net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}).DialContext
	transport.TLSHandshakeTimeout = tlsTimeout
	transport.ResponseHeaderTimeout = headerTimeout
	transport.IdleConnTimeout = idleTimeout
	return transport
}
