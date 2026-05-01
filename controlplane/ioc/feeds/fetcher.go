package feeds

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Fetcher knows how to retrieve raw bytes for a single_file feed and
// to keep a working tree fresh for a git feed. It does NOT parse —
// callers hand the bytes / file paths to the parser package.
type Fetcher struct {
	stateDir string
	httpc    *http.Client
}

// NewFetcher creates a Fetcher rooted at stateDir. Git working trees
// land under stateDir/<slug>/.
func NewFetcher(stateDir string) *Fetcher {
	return &Fetcher{
		stateDir: stateDir,
		httpc:    &http.Client{Timeout: 60 * time.Second},
	}
}

// FetchSingleFile issues a GET against url with optional Authorization
// header. Non-2xx responses produce an error (and the body is read +
// discarded so the connection can be reused).
func (f *Fetcher) FetchSingleFile(url, authHeader string) ([]byte, error) {
	req, err := http.NewRequestWithContext(context.Background(), "GET", url, nil)
	if err != nil {
		return nil, err
	}
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	resp, err := f.httpc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}

// EnsureGitWorktree clones repoURL into stateDir/<slug>/ if missing,
// or `git pull --ff-only` an existing worktree. Returns the absolute
// path to the worktree root. Caller walks subpath itself. A failed
// pull triggers a recovery reclone; if both fail the error from the
// reclone is returned.
func (f *Fetcher) EnsureGitWorktree(slug, repoURL string) (string, error) {
	dir := filepath.Join(f.stateDir, slug)
	if !exists(dir) {
		cmd := exec.Command("git", "clone", "--depth", "1", repoURL, dir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("git clone: %s: %w", string(out), err)
		}
		return dir, nil
	}
	cmd := exec.Command("git", "-C", dir, "pull", "--ff-only")
	if out, err := cmd.CombinedOutput(); err != nil {
		// Recover by reclone.
		_ = os.RemoveAll(dir)
		cmd2 := exec.Command("git", "clone", "--depth", "1", repoURL, dir)
		if out2, err2 := cmd2.CombinedOutput(); err2 != nil {
			return "", fmt.Errorf("git pull and reclone failed: pull=%s reclone=%s: %w", string(out), string(out2), err2)
		}
	}
	return dir, nil
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
