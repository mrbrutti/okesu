//go:build integration

package feeds

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestFetcher_Git_CloneAndPull(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}

	root := t.TempDir()
	upstream := filepath.Join(root, "upstream.git")
	work := filepath.Join(root, "scratch")

	mustRun(t, "git", "init", "--bare", upstream)
	mustRun(t, "git", "init", work)
	if err := os.MkdirAll(filepath.Join(work, "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "rules", "r.yar"), []byte("rule R { condition: false }"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", work, "add", ".")
	mustRun(t, "git", "-C", work, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-m", "init")
	mustRun(t, "git", "-C", work, "remote", "add", "origin", upstream)
	// Push to whatever branch the local "git init" created (could be
	// master or main depending on the operator's git defaults).
	branchOut, err := exec.Command("git", "-C", work, "branch", "--show-current").CombinedOutput()
	if err != nil {
		t.Fatalf("get branch: %v: %s", err, branchOut)
	}
	branch := string(branchOut)
	branch = trimNewline(branch)
	mustRun(t, "git", "-C", work, "push", "origin", "HEAD:"+branch)

	f := NewFetcher(filepath.Join(root, "state"))
	dir, err := f.EnsureGitWorktree("test-feed", upstream)
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	if !exists(filepath.Join(dir, "rules", "r.yar")) {
		t.Fatalf("expected cloned file %s/rules/r.yar to exist", dir)
	}

	// Second call exercises the pull path against an unchanged remote.
	if _, err := f.EnsureGitWorktree("test-feed", upstream); err != nil {
		t.Fatalf("pull: %v", err)
	}
	if !exists(filepath.Join(dir, "rules", "r.yar")) {
		t.Fatalf("expected file to still exist after pull")
	}
}

func mustRun(t *testing.T, name string, args ...string) {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, string(out))
	}
}

func trimNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
