// Package s3blob implements ports.BlobStore against any S3-compatible
// object store. Tested adapters in production:
//   - OCI Object Storage (Oracle Cloud) — uses the dedicated S3-compat
//     endpoint, region-scoped: https://<namespace>.compat.objectstorage.<region>.oraclecloud.com
//   - AWS S3 — standard endpoint
//   - Cloudflare R2 — standard S3 endpoint
//   - MinIO (self-hosted)
//
// The minio-go client lets us cover all of these with one driver. The
// only thing that varies is endpoint + credential acquisition.
package s3blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/section9labs/okesu/controlplane/ports"
)

// Config bundles the connection parameters. Region applies for AWS and
// OCI; MinIO ignores it. UseSSL defaults to true — set to false only
// for plain-HTTP test setups.
type Config struct {
	Endpoint  string // host:port — no scheme; we compose with UseSSL
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
	UseSSL    bool
}

// Adapter satisfies ports.BlobStore.
type Adapter struct {
	cli    *minio.Client
	bucket string
}

// New connects to the configured S3-compatible service and verifies the
// bucket exists (creates if missing). UseSSL=false is intended only for
// dev — production should keep it true.
func New(ctx context.Context, c Config) (*Adapter, error) {
	if c.Endpoint == "" || c.Bucket == "" {
		return nil, errors.New("s3blob: endpoint + bucket required")
	}
	if c.AccessKey == "" || c.SecretKey == "" {
		return nil, errors.New("s3blob: access_key + secret_key required")
	}
	// Defensive: see stripScheme rationale in agent/s3transport/client.go.
	// minio-go rejects fully-qualified endpoint URLs ("Endpoint url cannot
	// have fully qualified paths"), and a few legacy transport_configs
	// rows still carry a "https://…" prefix from the OCI provisioner.
	endpoint, secure := stripScheme(c.Endpoint, c.UseSSL)
	cli, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(c.AccessKey, c.SecretKey, ""),
		Secure: secure,
		Region: c.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("s3blob: client: %w", err)
	}

	exists, err := cli.BucketExists(ctx, c.Bucket)
	if err != nil {
		return nil, fmt.Errorf("s3blob: bucket exists: %w", err)
	}
	if !exists {
		if err := cli.MakeBucket(ctx, c.Bucket, minio.MakeBucketOptions{Region: c.Region}); err != nil {
			return nil, fmt.Errorf("s3blob: make bucket: %w", err)
		}
	}

	return &Adapter{cli: cli, bucket: c.Bucket}, nil
}

// stripScheme normalises an S3 endpoint to host[:port] form. Mirrors
// the helper in agent/s3transport/client.go — kept duplicated rather
// than promoted to a shared module because the agent must not import
// the controlplane and vice-versa.
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

// PresignedGetURL returns a time-limited GET URL for an object. Used
// by the managed-deploy flow to hand a fresh OCI VM a curl-able URL
// for its bootstrap bundle without exposing bucket credentials in
// the cloud-init script. The URL self-authenticates via SigV4 query
// params; the only thing the VM needs is plain DNS + outbound HTTPS
// to the bucket endpoint.
func (a *Adapter) PresignedGetURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	u, err := a.cli.PresignedGetObject(ctx, a.bucket, key, expiry, url.Values{})
	if err != nil {
		return "", fmt.Errorf("s3blob: presign %q: %w", key, err)
	}
	return u.String(), nil
}

func (a *Adapter) Put(ctx context.Context, key string, data io.Reader, contentType string) error {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	_, err := a.cli.PutObject(ctx, a.bucket, key, data, -1, minio.PutObjectOptions{
		ContentType: contentType,
	})
	return err
}

func (a *Adapter) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := a.cli.GetObject(ctx, a.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	// minio-go's GetObject doesn't error on missing keys; the next read
	// fails. Stat-then-return surfaces ErrNotFound at the right level.
	if _, err := obj.Stat(); err != nil {
		_ = obj.Close()
		errResp := minio.ToErrorResponse(err)
		if errResp.StatusCode == 404 || errResp.Code == "NoSuchKey" {
			return nil, fmt.Errorf("%w: %s", ports.ErrNotFound, key)
		}
		return nil, err
	}
	return obj, nil
}

func (a *Adapter) Delete(ctx context.Context, key string) error {
	return a.cli.RemoveObject(ctx, a.bucket, key, minio.RemoveObjectOptions{})
}

// DeletePrefix removes every object whose key starts with the given
// prefix. Used by the node/cp destroy paths to sweep the per-node
// or per-CP bucket folder (cp/<id>/nodes/<n>/* and cp/<child>/*).
// Lists in batches and removes via the bulk RemoveObjects channel
// so a thousand-object delete completes in one round-trip.
//
// Best-effort semantics: a partial failure does not roll back; the
// caller logs the error and proceeds. Delete-of-row is the operator's
// final intent and a stale JSON object in the bucket is harmless.
func (a *Adapter) DeletePrefix(ctx context.Context, prefix string) error {
	objCh := make(chan minio.ObjectInfo)
	go func() {
		defer close(objCh)
		for obj := range a.cli.ListObjects(ctx, a.bucket, minio.ListObjectsOptions{
			Prefix:    prefix,
			Recursive: true,
		}) {
			if obj.Err != nil {
				continue
			}
			objCh <- obj
		}
	}()
	errCh := a.cli.RemoveObjects(ctx, a.bucket, objCh, minio.RemoveObjectsOptions{})
	for e := range errCh {
		if e.Err != nil {
			return fmt.Errorf("s3blob: delete prefix %q: %w", prefix, e.Err)
		}
	}
	return nil
}

func (a *Adapter) List(ctx context.Context, prefix string, limit int) ([]ports.BlobInfo, error) {
	if limit <= 0 {
		limit = 1000
	}
	out := []ports.BlobInfo{}
	ch := a.cli.ListObjects(ctx, a.bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	})
	for obj := range ch {
		if obj.Err != nil {
			return nil, obj.Err
		}
		out = append(out, ports.BlobInfo{
			Key:        obj.Key,
			SizeBytes:  obj.Size,
			ModifiedAt: obj.LastModified.UnixMilli(),
			ETag:       strings.Trim(obj.ETag, `"`),
		})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// Compile-time assertion.
var _ ports.BlobStore = (*Adapter)(nil)
