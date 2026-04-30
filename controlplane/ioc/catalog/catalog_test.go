package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/section9labs/okesu/controlplane/db"
)

func TestLoadDir_ParsesSingleFile(t *testing.T) {
	dir := t.TempDir()
	yaml := []byte(`iocs:
  - kind: sha256
    value: DeadBeefDeadBeefDeadBeefDeadBeefDeadBeefDeadBeefDeadBeefDeadBeef
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
	wantNorm := "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	if e.Kind != "sha256" || e.NormalizedValue != wantNorm {
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

// fakeStore is a minimal in-memory IOCStore for catalog tests so we
// don't need to pull in the real db.Store + sqlite test helper from
// the db package. The integration with the real store is exercised
// by the existing db package tests (Group A) and the CP startup smoke
// test in Group Z.
type fakeStore struct {
	rows map[string]*db.IOCRecord
}

func newFakeStore() *fakeStore {
	return &fakeStore{rows: map[string]*db.IOCRecord{}}
}

func (f *fakeStore) key(kind, normalized string) string {
	return kind + "|" + normalized
}

func (f *fakeStore) UpsertIOC(in *db.IOCUpsert) (int64, bool, error) {
	k := f.key(in.Kind, in.NormalizedValue)
	if _, ok := f.rows[k]; ok {
		// Mimic catalog override of observed metadata.
		if in.Source == "catalog" {
			r := f.rows[k]
			r.Source = in.Source
			r.DefinitionPath = in.DefinitionPath
			r.Confidence = in.Confidence
			r.Attribution = in.Attribution
			r.SeverityFloor = in.SeverityFloor
			r.Classification = in.Classification
			r.Notes = in.Notes
		}
		return f.rows[k].ID, false, nil
	}
	id := int64(len(f.rows) + 1)
	source := in.Source
	if source == "" {
		source = "observed"
	}
	f.rows[k] = &db.IOCRecord{
		ID:              id,
		Kind:            in.Kind,
		Value:           in.Value,
		NormalizedValue: in.NormalizedValue,
		Source:          source,
		DefinitionPath:  in.DefinitionPath,
		Confidence:      in.Confidence,
		Attribution:     in.Attribution,
		SeverityFloor:   in.SeverityFloor,
		Classification:  in.Classification,
		Notes:           in.Notes,
	}
	return id, true, nil
}

func (f *fakeStore) lookup(kind, normalized string) (*db.IOCRecord, error) {
	k := f.key(kind, normalized)
	r, ok := f.rows[k]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return r, nil
}

func TestLoadAndUpsert_FedersStoreFromYAML(t *testing.T) {
	dir := t.TempDir()
	yaml := []byte("iocs:\n  - kind: sha256\n    value: DeadBeefDeadBeefDeadBeefDeadBeefDeadBeefDeadBeefDeadBeefDeadBeef\n    severity_floor: HIGH\n")
	if err := os.WriteFile(filepath.Join(dir, "x.yaml"), yaml, 0644); err != nil {
		t.Fatal(err)
	}
	st := newFakeStore()
	n, err := LoadAndUpsert(dir, st)
	if err != nil {
		t.Fatalf("LoadAndUpsert: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 upserted; got %d", n)
	}
	got, err := st.lookup("sha256", "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got.Source != "catalog" || got.SeverityFloor != "HIGH" {
		t.Errorf("expected catalog metadata; got %+v", got)
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
