package enrichment

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestShodan_Lookup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("key") != "test-key" {
			t.Errorf("missing key")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ports":        []int{22, 80, 443, 8080},
			"vulns":        []string{"CVE-2024-1234"},
			"tags":         []string{"compromised"},
			"country_code": "RU",
		})
	}))
	defer srv.Close()
	s := &Shodan{APIKey: "test-key", BaseURL: srv.URL, Client: srv.Client()}
	got, err := s.Lookup(context.Background(), "ipv4", "1.2.3.4")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got.Verdict != "malicious" {
		t.Errorf("Verdict = %q, want malicious", got.Verdict)
	}
}

func TestShodan_OnlySupportsIPv4(t *testing.T) {
	s := &Shodan{}
	if !s.Supports("ipv4") {
		t.Errorf("expected ipv4 support")
	}
	for _, k := range []string{"sha256", "domain", "ipv6"} {
		if s.Supports(k) {
			t.Errorf("did not expect %q support", k)
		}
	}
}
