package parser

import (
	"os"
	"strings"
	"testing"
)

func TestCISAKEVJSON_TwoCVEs(t *testing.T) {
	body, err := os.ReadFile("testdata/cisa_kev_sample.json")
	if err != nil { t.Fatal(err) }
	entries, err := ParseCISAKEVJSON(body)
	if err != nil { t.Fatalf("parse: %v", err) }
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries got %d", len(entries))
	}
	if entries[0].Kind != "cve" || entries[0].Value != "CVE-2024-11111" {
		t.Fatalf("entry 0: %+v", entries[0])
	}
	if !strings.Contains(entries[0].Tags, "ransomware") {
		t.Fatalf("expected ransomware tag for entry 0: %q", entries[0].Tags)
	}
	if strings.Contains(entries[1].Tags, "ransomware") {
		t.Fatalf("entry 1 should NOT have ransomware tag: %q", entries[1].Tags)
	}
	for _, e := range entries {
		if e.SeverityFloor != "HIGH" {
			t.Fatalf("severity: %q", e.SeverityFloor)
		}
		if e.Attribution != "CISA" {
			t.Fatalf("attribution: %q", e.Attribution)
		}
		if !strings.Contains(e.Tags, "vendor:Vendor") {
			t.Fatalf("vendor tag missing: %q", e.Tags)
		}
		if !strings.Contains(e.Tags, "product:Prod") {
			t.Fatalf("product tag missing: %q", e.Tags)
		}
		if !strings.Contains(e.Tags, "cisa-kev") {
			t.Fatalf("cisa-kev tag missing: %q", e.Tags)
		}
	}
}

func TestCISAKEVJSON_MalformedJSON(t *testing.T) {
	_, err := ParseCISAKEVJSON([]byte("{not-json"))
	if err == nil {
		t.Fatal("expected error on malformed JSON")
	}
}

func TestCISAKEVJSON_EmptyVulnerabilities(t *testing.T) {
	entries, err := ParseCISAKEVJSON([]byte(`{"vulnerabilities": []}`))
	if err != nil { t.Fatalf("parse: %v", err) }
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries got %d", len(entries))
	}
}
