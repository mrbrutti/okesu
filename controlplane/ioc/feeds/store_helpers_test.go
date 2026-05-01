package feeds

import (
	"path/filepath"
	"testing"

	"github.com/section9labs/okesu/controlplane/db"
)

// newTestStore opens a fresh on-disk SQLite store under t.TempDir().
// Mirrors the api package's helper for cross-package consistency.
func newTestStore(t *testing.T) *db.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cp.db")
	s, err := db.Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
