package catalog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDir_ParsesSingleFile(t *testing.T) {
	dir := t.TempDir()
	yaml := []byte(`iocs:
  - kind: sha256
    value: ABC
    confidence: high
    attribution: apt-foo
    severity_floor: HIGH
    classification: malware-c2
    notes: example
`)
	if err := os.WriteFile(filepath.Join(dir, "test.yaml"), yaml, 0644); err != nil {
		t.Fatal(err)
	}
	entries, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry; got %d", len(entries))
	}
	e := entries[0]
	if e.Kind != "sha256" || e.Value != "ABC" || e.NormalizedValue != "abc" {
		t.Errorf("entry mismatch: %+v", e)
	}
	if e.SeverityFloor != "HIGH" || e.Attribution != "apt-foo" {
		t.Errorf("metadata mismatch: %+v", e)
	}
	if e.DefinitionPath == "" || filepath.Base(e.DefinitionPath) != "test.yaml" {
		t.Errorf("expected DefinitionPath set to source file; got %q", e.DefinitionPath)
	}
	if e.Source != "catalog" {
		t.Errorf("expected Source=catalog; got %q", e.Source)
	}
}

func TestLoadDir_RejectsUnknownKind(t *testing.T) {
	dir := t.TempDir()
	yaml := []byte("iocs:\n  - kind: spaceship\n    value: foo\n")
	if err := os.WriteFile(filepath.Join(dir, "bad.yaml"), yaml, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDir(dir); err == nil {
		t.Errorf("expected error for unknown kind")
	}
}

func TestLoadDir_MissingDirReturnsEmpty(t *testing.T) {
	entries, err := LoadDir(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("LoadDir on missing dir should not error; got %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected empty entries; got %d", len(entries))
	}
}
