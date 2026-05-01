package parser

import (
	"os"
	"strings"
	"testing"
)

func TestURLhausCSV_TwoRows(t *testing.T) {
	body, err := os.ReadFile("testdata/urlhaus_sample.csv")
	if err != nil { t.Fatal(err) }
	entries, err := ParseURLhausCSV(body)
	if err != nil { t.Fatalf("parse: %v", err) }
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries got %d", len(entries))
	}
	if entries[0].Kind != "url" || entries[0].Value != "http://malicious.example/x.exe" {
		t.Fatalf("entry 0: %+v", entries[0])
	}
	if !strings.Contains(entries[0].Tags, "malware_download") {
		t.Fatalf("expected threat tag in entry 0: %q", entries[0].Tags)
	}
	if !strings.Contains(entries[0].Tags, "emotet") {
		t.Fatalf("expected emotet tag in entry 0: %q", entries[0].Tags)
	}
	if entries[0].Attribution != "abuse.ch URLhaus" {
		t.Fatalf("attribution: %q", entries[0].Attribution)
	}
}

func TestURLhausCSV_EmptyAndCommentOnly(t *testing.T) {
	entries, err := ParseURLhausCSV([]byte("# only comments\n# more comments\n"))
	if err != nil { t.Fatalf("parse: %v", err) }
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries got %d", len(entries))
	}
}
