// In-memory Bucket implementation used by the s3rpc unit + roundtrip
// tests. Lives in a _test.go file so it doesn't ship in the production
// binary.

package s3rpc

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/section9labs/okesu/agent/s3transport"
)

// fakeBucket is a process-local key/value store that satisfies the
// Bucket interface. Two s3rpc parties using the same *fakeBucket
// instance see the same writes — that's the integration setup the
// roundtrip tests rely on.
type fakeBucket struct {
	mu      sync.Mutex
	objects map[string]fakeObject

	// Hooks for fault-injection tests. Default zero values are
	// no-ops — wire them up in the test that needs them.
	beforeGet  func(key string) error
	beforeList func(prefix string) error
}

type fakeObject struct {
	body     []byte
	modified time.Time
}

func newFakeBucket() *fakeBucket {
	return &fakeBucket{objects: map[string]fakeObject{}}
}

func (b *fakeBucket) Put(_ context.Context, key string, body []byte, _ string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	cp := append([]byte(nil), body...)
	b.objects[key] = fakeObject{body: cp, modified: time.Now()}
	return nil
}

func (b *fakeBucket) GetBytes(_ context.Context, key string) ([]byte, error) {
	if b.beforeGet != nil {
		if err := b.beforeGet(key); err != nil {
			return nil, err
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	o, ok := b.objects[key]
	if !ok {
		return nil, errors.New("not found")
	}
	out := append([]byte(nil), o.body...)
	return out, nil
}

func (b *fakeBucket) Delete(_ context.Context, key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.objects, key)
	return nil
}

func (b *fakeBucket) List(_ context.Context, prefix string, limit int) ([]s3transport.ObjectInfo, error) {
	if b.beforeList != nil {
		if err := b.beforeList(prefix); err != nil {
			return nil, err
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]s3transport.ObjectInfo, 0, 16)
	for k, o := range b.objects {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		out = append(out, s3transport.ObjectInfo{
			Key:      k,
			Size:     int64(len(o.body)),
			Modified: o.modified.UnixMilli(),
		})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

// snapshotKeys is a test helper for assertions about which keys exist
// after a sequence of operations. Returns a sorted slice would be
// nicer but tests do their own sorting where it matters.
func (b *fakeBucket) snapshotKeys() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.objects))
	for k := range b.objects {
		out = append(out, k)
	}
	return out
}

// hasKey is a convenience for one-off existence checks in tests.
func (b *fakeBucket) hasKey(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.objects[key]
	return ok
}
