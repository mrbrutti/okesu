// Bucket is the subset of s3transport.Client that s3rpc needs.
// Defined here so tests can substitute an in-memory fake without
// dragging in minio + a live bucket connection.
//
// The concrete *s3transport.Client satisfies this interface
// structurally — no production wiring change is needed.

package s3rpc

import (
	"context"

	"github.com/section9labs/okesu/agent/s3transport"
)

// Bucket is the read/write contract over a key-prefix in some object
// store. The four methods are the smallest set s3rpc.Client and
// s3rpc.Server use; expanding it should be a deliberate change so
// the test fake stays in lock-step.
type Bucket interface {
	Put(ctx context.Context, key string, body []byte, contentType string) error
	GetBytes(ctx context.Context, key string) ([]byte, error)
	List(ctx context.Context, prefix string, limit int) ([]s3transport.ObjectInfo, error)
	Delete(ctx context.Context, key string) error
}
