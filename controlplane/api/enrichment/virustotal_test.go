package enrichment

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVirusTotal_LookupSHA256(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/files/abc") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("x-apikey") != "test-key" {
			t.Errorf("missing or bad api key")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"attributes": map[string]any{
					"last_analysis_stats": map[string]any{
						"malicious":  47,
						"suspicious": 2,
						"undetected": 18,
						"harmless":   3,
					},
				},
			},
		})
	}))
	defer srv.Close()
	v := &VirusTotal{APIKey: "test-key", BaseURL: srv.URL, Client: srv.Client()}
	got, err := v.Lookup(context.Background(), "sha256", "abc")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got.Verdict != "malicious" {
		t.Errorf("Verdict = %q, want malicious", got.Verdict)
	}
	if got.Score == 0 {
		t.Errorf("expected non-zero score")
	}
}

func TestVirusTotal_Supports(t *testing.T) {
	v := &VirusTotal{}
	for _, k := range []string{"sha256", "sha1", "md5", "ipv4", "domain"} {
		if !v.Supports(k) {
			t.Errorf("expected support for %q", k)
		}
	}
	for _, k := range []string{"cve", "mitre", "yara_rule"} {
		if v.Supports(k) {
			t.Errorf("did not expect support for %q", k)
		}
	}
}
