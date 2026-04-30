package enrichment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Shodan adapts the Shodan REST API /shodan/host/{ip} endpoint.
// Verdict heuristic: any "compromised"/"malware"/"tor" tag -> malicious;
// presence of any vulns -> suspicious; else clean.
type Shodan struct {
	APIKey  string
	BaseURL string
	Client  *http.Client
}

func (s *Shodan) Name() string              { return "shodan" }
func (s *Shodan) Supports(kind string) bool { return kind == "ipv4" }

func (s *Shodan) Lookup(ctx context.Context, kind, normalizedValue string) (*Result, error) {
	if kind != "ipv4" {
		return nil, fmt.Errorf("shodan: unsupported kind %q", kind)
	}
	if s.APIKey == "" {
		return nil, errors.New("shodan: API key not configured")
	}
	base := s.BaseURL
	if base == "" {
		base = "https://api.shodan.io"
	}
	cli := s.Client
	if cli == nil {
		cli = &http.Client{Timeout: 30 * time.Second}
	}
	url := base + "/shodan/host/" + normalizedValue + "?key=" + s.APIKey
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return &Result{Adapter: s.Name(), Verdict: "clean", RawJSON: `{"reason":"not in shodan"}`}, nil
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("shodan: status %d: %s", resp.StatusCode, string(body))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Tags  []string `json:"tags"`
		Vulns []string `json:"vulns"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	verdict := "clean"
	score := int64(0)
	for _, t := range raw.Tags {
		switch strings.ToLower(t) {
		case "compromised", "malware", "tor":
			verdict = "malicious"
			score = 90
		}
	}
	if verdict == "clean" && len(raw.Vulns) > 0 {
		verdict = "suspicious"
		score = 50
	}
	return &Result{
		Adapter: s.Name(),
		Verdict: verdict,
		Score:   score,
		RawJSON: string(body),
	}, nil
}
