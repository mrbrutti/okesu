package parser

import (
	"os"
	"strings"
	"testing"
)

func TestThreatFoxCSV_MapsKinds(t *testing.T) {
	body, err := os.ReadFile("testdata/threatfox_sample.csv")
	if err != nil { t.Fatal(err) }
	entries, err := ParseThreatFoxCSV(body)
	if err != nil { t.Fatalf("parse: %v", err) }
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries got %d", len(entries))
	}
	got := []string{entries[0].Kind, entries[1].Kind, entries[2].Kind}
	want := []string{"sha256", "ipv4", "domain"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("entry %d: kind=%q want=%q", i, got[i], want[i])
		}
	}
	if entries[1].Value != "8.8.8.8" {
		t.Fatalf("ip:port should split off port: %q", entries[1].Value)
	}
	if !strings.Contains(entries[0].Tags, "malware:Emotet") {
		t.Fatalf("expected malware: tag in entry 0: %q", entries[0].Tags)
	}
	if !strings.Contains(entries[0].Tags, "emotet") {
		t.Fatalf("expected emotet (lowercase) tag in entry 0: %q", entries[0].Tags)
	}
	for _, e := range entries {
		if e.Attribution != "abuse.ch ThreatFox" {
			t.Fatalf("attribution: %q", e.Attribution)
		}
	}
}

func TestThreatFoxCSV_IPv6WithPort(t *testing.T) {
	// IPv6 with port appears bracketed: "[2001:db8::1]:443"
	body := []byte(`# header
"2026-04-30","1","[2001:db8::1]:443","ip:port","botnet_cc","mal","","Mal","2026-04-30","80","https://example.com","","0","abuse.ch"
`)
	entries, err := ParseThreatFoxCSV(body)
	if err != nil { t.Fatalf("parse: %v", err) }
	if len(entries) != 1 || entries[0].Kind != "ipv6" || entries[0].Value != "2001:db8::1" {
		t.Fatalf("ipv6 parse: %+v", entries)
	}
}
