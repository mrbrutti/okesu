// Thin S3-compatible client used by the dead-drop transport.
//
// We deliberately don't import the CP-side `controlplane/adapters/s3blob`
// here — the agent should not depend on the controlplane module. Both
// sides use the same minio-go library underneath; this wrapper just
// covers the operations the transport needs (get / put / delete / list,
// plus a conditional put for atomic claim).

package s3transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// ClientConfig bundles the connection parameters. UseSSL=false is
// only for local test loops (MinIO over plain HTTP); production
// always wants TLS.
type ClientConfig struct {
	Endpoint  string // host[:port], no scheme
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
	UseSSL    bool
}

// Client wraps minio-go with the small surface this package needs.
type Client struct {
	cli    *minio.Client
	bucket string
}

// NewClient connects and verifies the bucket is reachable.
func NewClient(ctx context.Context, c ClientConfig) (*Client, error) {
	if c.Endpoint == "" || c.Bucket == "" {
		return nil, errors.New("s3transport: endpoint + bucket required")
	}
	if c.AccessKey == "" || c.SecretKey == "" {
		return nil, errors.New("s3transport: access_key + secret_key required")
	}
	// Defensive: legacy transport_configs rows occasionally carry a
	// fully-qualified URL (e.g. "https://host.example") because the
	// OCI provisioner used to store the public endpoint verbatim. The
	// minio-go validator rejects those — strip the scheme here and
	// honour it on UseSSL so old rows keep working without a manual fix.
	endpoint, secure := stripScheme(c.Endpoint, c.UseSSL)
	cli, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(c.AccessKey, c.SecretKey, ""),
		Secure: secure,
		Region: c.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("s3transport: minio: %w", err)
	}
	exists, err := cli.BucketExists(ctx, c.Bucket)
	if err != nil {
		return nil, fmt.Errorf("s3transport: bucket exists: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("s3transport: bucket %q does not exist", c.Bucket)
	}
	return &Client{cli: cli, bucket: c.Bucket}, nil
}

// stripScheme normalises an S3 endpoint to host[:port] form. If the
// caller passed "https://host" we honour the scheme as the secure
// signal; an explicit fallback flag covers the bare-host case so the
// original UseSSL passed in by the caller still wins. Trailing path
// is dropped so the minio-go validator (which rejects "fully
// qualified paths") accepts the result.
func stripScheme(endpoint string, fallbackSecure bool) (string, bool) {
	endpoint = strings.TrimSpace(endpoint)
	switch {
	case strings.HasPrefix(endpoint, "https://"):
		endpoint = strings.TrimPrefix(endpoint, "https://")
		fallbackSecure = true
	case strings.HasPrefix(endpoint, "http://"):
		endpoint = strings.TrimPrefix(endpoint, "http://")
		fallbackSecure = false
	}
	if i := strings.IndexByte(endpoint, '/'); i >= 0 {
		endpoint = endpoint[:i]
	}
	return endpoint, fallbackSecure
}

// ErrNotFound surfaces 404 from the underlying S3 service.
var ErrNotFound = errors.New("s3transport: object not found")

// ErrPreconditionFailed surfaces 412 from a conditional put. The
// runner's claim flow uses this to detect a race.
var ErrPreconditionFailed = errors.New("s3transport: precondition failed")

// Get reads a full object. Caller closes.
func (c *Client) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := c.cli.GetObject(ctx, c.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	if _, err := obj.Stat(); err != nil {
		_ = obj.Close()
		if minio.ToErrorResponse(err).StatusCode == 404 {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return obj, nil
}

// GetBytes is a convenience for small JSON / text objects.
func (c *Client) GetBytes(ctx context.Context, key string) ([]byte, error) {
	r, err := c.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// Put writes data to key, overwriting unconditionally.
func (c *Client) Put(ctx context.Context, key string, body []byte, contentType string) error {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	_, err := c.cli.PutObject(ctx, c.bucket, key, bytesReader(body), int64(len(body)), minio.PutObjectOptions{
		ContentType: contentType,
	})
	return err
}

// PutIfAbsent writes data to key only if the key doesn't exist
// (S3 If-None-Match: *). Returns ErrPreconditionFailed when the
// object already exists.
func (c *Client) PutIfAbsent(ctx context.Context, key string, body []byte, contentType string) error {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	opts := minio.PutObjectOptions{ContentType: contentType}
	// minio-go SetMatchETagExcept attaches `If-None-Match: *` so the
	// PUT fails (412) if any object already exists at this key.
	opts.SetMatchETagExcept("*")
	_, err := c.cli.PutObject(ctx, c.bucket, key, bytesReader(body), int64(len(body)), opts)
	if err != nil {
		if minio.ToErrorResponse(err).StatusCode == 412 {
			return ErrPreconditionFailed
		}
		return err
	}
	return nil
}

// Delete removes an object. Idempotent; missing keys do not error.
func (c *Client) Delete(ctx context.Context, key string) error {
	return c.cli.RemoveObject(ctx, c.bucket, key, minio.RemoveObjectOptions{})
}

// ObjectInfo summarises one object in a List response.
type ObjectInfo struct {
	Key      string
	Size     int64
	ETag     string
	Modified int64 // unix ms
}

// List returns all keys under prefix, up to limit. Recursive.
func (c *Client) List(ctx context.Context, prefix string, limit int) ([]ObjectInfo, error) {
	if limit <= 0 {
		limit = 1000
	}
	out := make([]ObjectInfo, 0, 16)
	ch := c.cli.ListObjects(ctx, c.bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	})
	for o := range ch {
		if o.Err != nil {
			return nil, o.Err
		}
		out = append(out, ObjectInfo{
			Key:      o.Key,
			Size:     o.Size,
			ETag:     strings.Trim(o.ETag, `"`),
			Modified: o.LastModified.UnixMilli(),
		})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// helpers

type bytesReaderT struct {
	b []byte
	i int
}

func bytesReader(b []byte) *bytesReaderT { return &bytesReaderT{b: b} }

func (r *bytesReaderT) Read(p []byte) (int, error) {
	if r.i >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.i:])
	r.i += n
	return n, nil
}
