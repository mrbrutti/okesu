package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestJobsEnv_BothKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.env")
	if err := writeJobsEnvAtomicTo(path, "sk-ant-abc", "sk-oai-xyz"); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := "ANTHROPIC_API_KEY=sk-ant-abc\nOPENAI_API_KEY=sk-oai-xyz\n"
	if string(got) != want {
		t.Errorf("body: got %q, want %q", string(got), want)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if st.Mode().Perm() != 0600 {
		t.Errorf("mode: got %o, want 0600", st.Mode().Perm())
	}
}

func TestJobsEnv_OnlyAnthropic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.env")
	if err := writeJobsEnvAtomicTo(path, "sk-ant-abc", ""); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := "ANTHROPIC_API_KEY=sk-ant-abc\n"
	if string(got) != want {
		t.Errorf("body: got %q, want %q", string(got), want)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if st.Mode().Perm() != 0600 {
		t.Errorf("mode: got %o, want 0600", st.Mode().Perm())
	}
}

func TestJobsEnv_BothEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.env")
	if err := writeJobsEnvAtomicTo(path, "", ""); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("body: got %q, want empty", string(got))
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if st.Mode().Perm() != 0600 {
		t.Errorf("mode: got %o, want 0600", st.Mode().Perm())
	}
}

func TestJobsEnv_AtomicReplace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.env")
	// Pre-populate with junk content the helper must overwrite.
	if err := os.WriteFile(path, []byte("ANTHROPIC_API_KEY=old\nOPENAI_API_KEY=stale\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := writeJobsEnvAtomicTo(path, "sk-ant-new", "sk-oai-new"); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := "ANTHROPIC_API_KEY=sk-ant-new\nOPENAI_API_KEY=sk-oai-new\n"
	if string(got) != want {
		t.Errorf("body: got %q, want %q", string(got), want)
	}
	// rename(2) preserves the destination's metadata bits inconsistently
	// across platforms, but our helper chmod's the temp before rename so
	// the final file should always end up 0600.
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if st.Mode().Perm() != 0600 {
		t.Errorf("mode: got %o, want 0600", st.Mode().Perm())
	}
}

func TestJobsEnv_PackageVarUnchanged(t *testing.T) {
	// Sanity: production callsite still points at the canonical path so
	// the daemon writes to /etc/okesu/jobs.env in real deployments.
	if jobsEnvPath != "/etc/okesu/jobs.env" {
		t.Errorf("jobsEnvPath: got %q, want %q", jobsEnvPath, "/etc/okesu/jobs.env")
	}
}
