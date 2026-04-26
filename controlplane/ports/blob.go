package ports

import (
	"context"
	"io"
)

// BlobStore is large-object storage — daemon binaries by os/arch, deploy
// artifacts, exported run transcripts, cold-tier event Parquet files,
// backups. NOT for relational state (use the state Store) or for events
// in the hot path (use the event Store).
//
// Adapters: s3 (works for OCI Object Storage, AWS S3, Cloudflare R2,
// MinIO via their S3-compatible APIs), filesystem (local disk; dev
// + small single-instance deployments).
//
// Key naming
//
// Adapters treat keys as opaque strings but operators see them as paths:
//
//	binaries/okesu-linux-arm64
//	exports/runs/2026-04/run-deadbeef.jsonl
//	cold/events/2026-04-26/hour-14.parquet
//
// "/" is the only valid hierarchy separator. Adapters MUST NOT translate
// it (S3 doesn't have real directories; filesystem does — both treat
// the slash as part of the key).
type BlobStore interface {
	// Put stores `data` at key, replacing any existing object. The
	// io.Reader is read fully; large uploads should chunk via the
	// adapter's native multipart support transparently.
	Put(ctx context.Context, key string, data io.Reader, contentType string) error

	// Get returns a reader for the object at key. Caller must Close.
	// Returns ErrNotFound when the key doesn't exist.
	Get(ctx context.Context, key string) (io.ReadCloser, error)

	// Delete removes the object. Idempotent — deleting a missing key
	// returns nil.
	Delete(ctx context.Context, key string) error

	// List returns keys matching the prefix, newest first. limit caps
	// the result set; 0 uses the adapter's default cap (typically 1000).
	List(ctx context.Context, prefix string, limit int) ([]BlobInfo, error)
}

// BlobInfo summarises one object in a List response. Adapters fill what
// they cheaply have; SizeBytes==0 means "unknown" rather than "empty".
type BlobInfo struct {
	Key        string
	SizeBytes  int64
	ModifiedAt int64 // unix ms
	ETag       string
}
