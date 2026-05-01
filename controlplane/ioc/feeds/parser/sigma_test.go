package parser

import (
	"os"
	"strings"
	"testing"
)

func TestSigmaParser_TwoDocs(t *testing.T) {
	body, err := os.ReadFile("testdata/sigma_two_docs.yml")
	if err != nil {
		t.Fatal(err)
	}

	entries, err := ParseSigmaBundle(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries got %d", len(entries))
	}
	if entries[0].Name == "" || entries[1].Name == "" {
		t.Fatalf("expected titles to populate Name: %+v", entries)
	}
	if entries[0].SeverityFloor != "HIGH" || entries[1].SeverityFloor != "MEDIUM" {
		t.Fatalf("severities: %q %q", entries[0].SeverityFloor, entries[1].SeverityFloor)
	}
	for _, e := range entries {
		if e.Kind != "sigma_rule" {
			t.Fatalf("kind: %q", e.Kind)
		}
		if e.NormalizedValue == "" {
			t.Fatalf("normalized value empty for %q", e.Name)
		}
		if !strings.Contains(e.Value, "title:") {
			t.Fatalf("value should contain rule body: %q", e.Value[:30])
		}
	}
}

func TestSigmaParser_SingleDocNoSeparator(t *testing.T) {
	// One rule with no `---` separator should still produce one entry.
	body := []byte("title: Solo Rule\nlevel: low\ndetection:\n  selection:\n    image: foo\n  condition: selection\n")
	entries, err := ParseSigmaBundle(body)
	if err != nil { t.Fatalf("parse: %v", err) }
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry got %d", len(entries))
	}
	if entries[0].Name != "Solo Rule" {
		t.Fatalf("title: %q", entries[0].Name)
	}
}

func TestSigmaParser_EmptyInput(t *testing.T) {
	entries, err := ParseSigmaBundle(nil)
	if err != nil { t.Fatalf("parse: %v", err) }
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries got %d", len(entries))
	}
}
