package enrichment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// AbuseIPDB adapts the AbuseIPDB v2 /api/v2/check endpoint.
// Maps abuseConfidenceScore: >=75 -> malicious, 25-74 -> suspicious, <25 -> clean.
type AbuseIPDB struct {
	APIKey  string
	BaseURL string
	Client  *http.Client
}

func (a *AbuseIPDB) Name() string              { return "abuseipdb" }
func (a *AbuseIPDB) Supports(kind string) bool { return kind == "ipv4" }

func (a *AbuseIPDB) Lookup(ctx context.Context, kind, normalizedValue string) (*Result, error) {
	if kind != "ipv4" {
		return nil, fmt.Errorf("abuseipdb: unsupported kind %q", kind)
	}
	if a.APIKey == "" {
		return nil, errors.New("abuseipdb: API key not configured")
	}
	base := a.BaseURL
	if base == "" {
		base = "https://api.abuseipdb.com"
	}
	cli := a.Client
	if cli == nil {
		cli = &http.Client{Timeout: 30 * time.Second}
	}
	url := base + "/api/v2/check?ipAddress=" + normalizedValue
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Key", a.APIKey)
	req.Header.Set("Accept", "application/json")

	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("abuseipdb: status %d: %s", resp.StatusCode, string(body))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Data struct {
			AbuseConfidenceScore int `json:"abuseConfidenceScore"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	score := raw.Data.AbuseConfidenceScore
	verdict := "clean"
	switch {
	case score >= 75:
		verdict = "malicious"
	case score >= 25:
		verdict = "suspicious"
	}
	return &Result{
		Adapter: a.Name(),
		Verdict: verdict,
		Score:   int64(score),
		RawJSON: string(body),
	}, nil
}
