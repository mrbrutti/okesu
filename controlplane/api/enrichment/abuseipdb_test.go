package enrichment

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAbuseIPDB_Lookup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Key") != "test-key" {
			t.Errorf("missing api key")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"abuseConfidenceScore": 87,
				"countryCode":          "RU",
				"totalReports":         42,
			},
		})
	}))
	defer srv.Close()
	a := &AbuseIPDB{APIKey: "test-key", BaseURL: srv.URL, Client: srv.Client()}
	got, err := a.Lookup(context.Background(), "ipv4", "1.2.3.4")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got.Score != 87 {
		t.Errorf("Score = %d, want 87", got.Score)
	}
	if got.Verdict != "malicious" {
		t.Errorf("Verdict = %q, want malicious", got.Verdict)
	}
}

func TestAbuseIPDB_OnlySupportsIPv4(t *testing.T) {
	a := &AbuseIPDB{}
	if !a.Supports("ipv4") {
		t.Errorf("expected ipv4 support")
	}
	for _, k := range []string{"sha256", "domain", "url", "ipv6"} {
		if a.Supports(k) {
			t.Errorf("did not expect %q support", k)
		}
	}
}
