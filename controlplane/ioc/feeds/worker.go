package feeds

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/ioc/catalog"
	"github.com/section9labs/okesu/controlplane/ioc/feeds/parser"
)

// Worker implements Refresher by chaining Fetcher → Parser → Reconcile
// and writing per-refresh status back to the ioc_feeds row.
type Worker struct {
	store   *db.Store
	fetcher *Fetcher
}

// NewWorker constructs a Worker. The fetcher's stateDir is where git
// worktrees live; the worker doesn't introduce additional state.
func NewWorker(store *db.Store, fetcher *Fetcher) *Worker {
	return &Worker{store: store, fetcher: fetcher}
}

// RefreshOne is the entry point invoked by the Scheduler. On error,
// MarkFeedRefresh is still called with status="error" so the UI shows
// the failure reason. The caller (Scheduler) ignores the returned
// error after logging.
func (w *Worker) RefreshOne(ctx context.Context, fc *db.FeedConfig) error {
	at := time.Now().UTC()
	entries, err := w.gather(fc)
	if err != nil {
		_ = w.store.MarkFeedRefresh(fc.ID, "error", err.Error(), 0, &at)
		return err
	}
	if _, err := Reconcile(w.store, fc.ID, fc.Slug, entries); err != nil {
		_ = w.store.MarkFeedRefresh(fc.ID, "error", err.Error(), 0, &at)
		return err
	}
	_ = w.store.MarkFeedRefresh(fc.ID, "ok", "", len(entries), &at)
	return nil
}

// Gather is the public entry point used by the Validate API endpoint
// (Task 16) — it performs fetch + parse without committing anything
// to the iocs table. Returns the would-be entry count.
func (w *Worker) Gather(fc *db.FeedConfig) ([]catalog.CatalogEntry, error) {
	return w.gather(fc)
}

func (w *Worker) gather(fc *db.FeedConfig) ([]catalog.CatalogEntry, error) {
	switch fc.Kind {
	case "single_file":
		body, err := w.fetcher.FetchSingleFile(fc.URL, "" /* TODO auth header from credential lookup */)
		if err != nil {
			return nil, err
		}
		return parseByEnum(fc.Parser, body)
	case "git":
		root, err := w.fetcher.EnsureGitWorktree(fc.Slug, fc.URL)
		if err != nil {
			return nil, err
		}
		walkRoot := root
		if fc.Subpath != "" {
			walkRoot = filepath.Join(root, fc.Subpath)
		}
		return walkAndParse(walkRoot, fc.Parser)
	}
	return nil, fmt.Errorf("unknown feed kind %q", fc.Kind)
}

func parseByEnum(name string, body []byte) ([]catalog.CatalogEntry, error) {
	switch name {
	case "yara":
		return parser.ParseYARABundle(body)
	case "sigma":
		return parser.ParseSigmaBundle(body)
	case "urlhaus_csv":
		return parser.ParseURLhausCSV(body)
	case "threatfox_csv":
		return parser.ParseThreatFoxCSV(body)
	case "cisa_kev_json":
		return parser.ParseCISAKEVJSON(body)
	}
	return nil, fmt.Errorf("unknown parser %q", name)
}

func walkAndParse(root, parserEnum string) ([]catalog.CatalogEntry, error) {
	var out []catalog.CatalogEntry
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		switch parserEnum {
		case "yara":
			if ext != ".yar" && ext != ".yara" {
				return nil
			}
		case "sigma":
			if ext != ".yml" && ext != ".yaml" {
				return nil
			}
		default:
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		entries, err := parseByEnum(parserEnum, body)
		if err != nil {
			// Skip a broken file, don't fail the whole walk —
			// SigmaHQ has thousands of rules and one malformed
			// document shouldn't lose the rest.
			return nil
		}
		out = append(out, entries...)
		return nil
	})
	return out, err
}
