// Package filesystem implements the BlobStore port against a local
// directory. Used in dev (one less external service) and as a fallback
// for small single-instance deployments. Production uses the s3 adapter
// against OCI Object Storage / AWS S3 / etc.
package filesystem

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/section9labs/okesu/controlplane/ports"
)

// BlobStore stores objects as files under Root. Object keys map to
// relative paths; "/" in the key becomes a directory separator.
//
// The implementation is intentionally dumb — no atomic-rename
// optimization, no fsync, no checksumming. For dev workloads that's
// fine; production wants the s3 adapter regardless.
type BlobStore struct {
	Root string
}

// New creates a filesystem BlobStore rooted at dir, creating it if
// missing.
func New(dir string) (*BlobStore, error) {
	if dir == "" {
		return nil, errors.New("filesystem blob: dir required")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	return &BlobStore{Root: dir}, nil
}

// safePath joins key onto Root, refusing keys that escape via "..".
func (b *BlobStore) safePath(key string) (string, error) {
	clean := filepath.Clean("/" + key)        // normalises and prevents traversal
	rel := strings.TrimPrefix(clean, "/")     // back to relative
	if rel == "" || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("invalid blob key %q", key)
	}
	return filepath.Join(b.Root, rel), nil
}

func (b *BlobStore) Put(ctx context.Context, key string, data io.Reader, contentType string) error {
	_ = contentType // adapters that don't track content-type skip it
	path, err := b.safePath(key)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, data)
	return err
}

func (b *BlobStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	path, err := b.safePath(key)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ports.ErrNotFound, key)
		}
		return nil, err
	}
	return f, nil
}

func (b *BlobStore) Delete(ctx context.Context, key string) error {
	path, err := b.safePath(key)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (b *BlobStore) List(ctx context.Context, prefix string, limit int) ([]ports.BlobInfo, error) {
	if limit <= 0 {
		limit = 1000
	}
	root, err := b.safePath(prefix + "/_")
	if err != nil {
		return nil, err
	}
	root = filepath.Dir(root) // trim the synthetic suffix
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	out := []ports.BlobInfo{}
	err = filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return filepath.SkipDir
			}
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		// Convert filesystem path back to a logical key.
		rel, rerr := filepath.Rel(b.Root, path)
		if rerr != nil {
			return nil // skip, shouldn't happen
		}
		key := filepath.ToSlash(rel)
		if !strings.HasPrefix(key, prefix) {
			return nil
		}
		out = append(out, ports.BlobInfo{
			Key:        key,
			SizeBytes:  info.Size(),
			ModifiedAt: info.ModTime().UnixMilli(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Newest first.
	sort.Slice(out, func(i, j int) bool { return out[i].ModifiedAt > out[j].ModifiedAt })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Compile-time check that we satisfy the port.
var _ ports.BlobStore = (*BlobStore)(nil)

// _now is here only to keep `time` imported when we extend the adapter
// later — easier than chasing an "imported and not used" compile error
// during interactive dev.
var _ = time.Now
