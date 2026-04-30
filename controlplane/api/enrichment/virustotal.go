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

// VirusTotal adapts api.virustotal.com/v3 (file/ip/domain endpoints).
// Free-tier rate limit is 4 RPM (~0.067 RPS); operators with paid keys
// can lift this in CP config. Caching defaults to 24h (set by the
// service caller, not here).
type VirusTotal struct {
	APIKey  string
	BaseURL string
	Client  *http.Client
}

func (v *VirusTotal) Name() string { return "virustotal" }

func (v *VirusTotal) Supports(kind string) bool {
	switch kind {
	case "sha256", "sha1", "md5", "ipv4", "domain":
		return true
	}
	return false
}

func (v *VirusTotal) Lookup(ctx context.Context, kind, normalizedValue string) (*Result, error) {
	if v.APIKey == "" {
		return nil, errors.New("virustotal: API key not configured")
	}
	base := v.BaseURL
	if base == "" {
		base = "https://www.virustotal.com/api/v3"
	}
	cli := v.Client
	if cli == nil {
		cli = &http.Client{Timeout: 30 * time.Second}
	}

	var path string
	switch kind {
	case "sha256", "sha1", "md5":
		path = "/files/" + normalizedValue
	case "ipv4":
		path = "/ip_addresses/" + normalizedValue
	case "domain":
		path = "/domains/" + normalizedValue
	default:
		return nil, fmt.Errorf("virustotal: unsupported kind %q", kind)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", base+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-apikey", v.APIKey)

	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return &Result{Adapter: v.Name(), Verdict: "clean", RawJSON: `{"reason":"not seen by VT"}`}, nil
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("virustotal: status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Data struct {
			Attributes struct {
				LastAnalysisStats struct {
					Malicious  int `json:"malicious"`
					Suspicious int `json:"suspicious"`
					Harmless   int `json:"harmless"`
					Undetected int `json:"undetected"`
				} `json:"last_analysis_stats"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("virustotal: decode: %w", err)
	}
	stats := raw.Data.Attributes.LastAnalysisStats
	verdict, score := classifyVTStats(stats.Malicious, stats.Suspicious, stats.Harmless, stats.Undetected)
	return &Result{
		Adapter: v.Name(),
		Verdict: verdict,
		Score:   score,
		RawJSON: string(body),
	}, nil
}

// classifyVTStats: >=5 malicious -> "malicious"; 1-4 malicious or any
// suspicious -> "suspicious"; else "clean". Score is the percentage of
// engines flagging malicious or suspicious.
func classifyVTStats(malicious, suspicious, harmless, undetected int) (string, int64) {
	total := malicious + suspicious + harmless + undetected
	if total == 0 {
		return "", 0
	}
	score := int64((malicious + suspicious) * 100 / total)
	switch {
	case malicious >= 5:
		return "malicious", score
	case malicious > 0 || suspicious > 0:
		return "suspicious", score
	default:
		return "clean", score
	}
}
