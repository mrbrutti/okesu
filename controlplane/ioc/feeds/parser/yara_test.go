package parser

import (
	"os"
	"strings"
	"testing"
)

func TestYARAParser_SplitsTwoRules(t *testing.T) {
	body, err := os.ReadFile("testdata/yara_two_rules.yar")
	if err != nil {
		t.Fatal(err)
	}

	entries, err := ParseYARABundle(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries got %d", len(entries))
	}

	if entries[0].Name != "One" || entries[1].Name != "Two" {
		t.Fatalf("names: %q %q", entries[0].Name, entries[1].Name)
	}
	if entries[0].SeverityFloor != "HIGH" || entries[1].SeverityFloor != "MEDIUM" {
		t.Fatalf("severities: %q %q", entries[0].SeverityFloor, entries[1].SeverityFloor)
	}
	for _, e := range entries {
		if e.Kind != "yara_rule" {
			t.Fatalf("kind: %q", e.Kind)
		}
		if !strings.HasPrefix(e.Value, "rule ") {
			t.Fatalf("value should be a rule body: %q", e.Value[:20])
		}
		if e.NormalizedValue == "" {
			t.Fatalf("normalized value missing for %q", e.Name)
		}
	}
}
