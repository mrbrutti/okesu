package agent

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"
)

const (
	defaultCertDir       = "/etc/okesu"
	defaultHeartbeatSec  = 30
	defaultPollSec       = 60
)

// MgmtConfig holds the management-plane connection parameters.
// Populated from agent file frontmatter under the mgmt: key.
type MgmtConfig struct {
	URL           string        `yaml:"url"`                     // https://mgmt.example.com
	HeartbeatSec  int           `yaml:"heartbeatSec,omitempty"`  // default 30
	PollSec       int           `yaml:"pollSec,omitempty"`       // default 60
	CertDir       string        `yaml:"certDir,omitempty"`       // default /etc/okesu
}

// registrationPayload is sent to <mgmt>/api/v1/agents/register.
type registrationPayload struct {
	Name     string `json:"name"`
	Host     string `json:"host"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Version  string `json:"version"`
}

// heartbeatPayload is sent to <mgmt>/api/v1/agents/<name>/heartbeat.
type heartbeatPayload struct {
	Host      string `json:"host"`
	Ts        int64  `json:"ts"`
	TickCount int64  `json:"tick_count,omitempty"`
}

// remoteConfig is the config returned by the polling endpoint.
// Additional fields can be added as the management plane evolves.
type remoteConfig struct {
	MaxTurns  int    `json:"max_turns,omitempty"`
	Effort    string `json:"effort,omitempty"`
	Suspended bool   `json:"suspended,omitempty"`
}

// KnownIssue is the wire shape of one entry from
// /api/v1/agents/{name}/known-issues. Mirrors db.KnownIssue.
type KnownIssue struct {
	Fingerprint     string `json:"fingerprint"`
	Status          string `json:"status"` // acknowledged|investigating|resolved|false_positive|wontfix
	TriageNote      string `json:"triage_note,omitempty"`
	UpdatedAt       int64  `json:"updated_at"`
	OccurrenceCount int64  `json:"occurrence_count"`
}

// LookupResult is the wire shape of one row returned by the
// /api/v1/agents/{name}/findings/search endpoint that backs the
// `lookup_findings` LLM tool.
//
// Phase 14 calibration fields:
//   - Severity: effective severity (operator override if set, else original)
//   - OriginalSeverity: what the LLM originally assigned
//   - SuggestedSeverity: server-recommended severity (rule match or
//     ≥3 consistent operator overrides). Empty when no signal exists.
type LookupResult struct {
	Fingerprint       string `json:"fingerprint"`
	Title             string `json:"title,omitempty"`
	Severity          string `json:"severity,omitempty"`
	OriginalSeverity  string `json:"original_severity,omitempty"`
	SuggestedSeverity string `json:"suggested_severity,omitempty"`
	Status            string `json:"status,omitempty"`
	Host              string `json:"host,omitempty"`
	Resource          string `json:"resource,omitempty"`
	TriageNote        string `json:"triage_note,omitempty"`
	LastSeen          int64  `json:"last_seen,omitempty"`
	OccurrenceCount   int64  `json:"occurrence_count"`
}

// MgmtPlane manages the connection to the optional management plane.
type MgmtPlane struct {
	cfg    MgmtConfig
	agent  Config
	host   string
	client *http.Client

	mu     sync.RWMutex
	remote remoteConfig
	// knownIssues is a fingerprint → status map populated by the
	// known-issues poller. Lookups happen on every harvested finding so
	// keep them O(1).
	knownIssues map[string]KnownIssue
}

// NewMgmtPlane constructs a MgmtPlane.
// Returns (nil, nil) when mcfg.URL is empty — callers should treat nil as "no mgmt plane".
func NewMgmtPlane(mcfg MgmtConfig, agentCfg Config) (*MgmtPlane, error) {
	if mcfg.URL == "" {
		return nil, nil
	}

	certDir := mcfg.CertDir
	if certDir == "" {
		certDir = defaultCertDir
	}

	tlsCfg, err := buildMTLSConfig(certDir)
	if err != nil {
		return nil, fmt.Errorf("mgmt mTLS: %w", err)
	}

	hostname, _ := os.Hostname()

	return &MgmtPlane{
		cfg:   mcfg,
		agent: agentCfg,
		host:  hostname,
		client: &http.Client{
			Timeout:   10 * time.Second,
			Transport: &http.Transport{TLSClientConfig: tlsCfg},
		},
	}, nil
}

// Register sends the agent's identity to the management plane.
func (m *MgmtPlane) Register() error {
	payload := registrationPayload{
		Name:     m.agent.Name,
		Host:     m.host,
		Provider: m.agent.Provider,
		Model:    m.agent.Model,
		Version:  Version(),
	}
	return m.post("/api/v1/agents/register", payload)
}

// StartHeartbeat runs a background goroutine that pings the management plane
// every HeartbeatSec seconds until ctx is cancelled.
func (m *MgmtPlane) StartHeartbeat(ctx context.Context, state *DaemonState) {
	interval := time.Duration(m.cfg.HeartbeatSec) * time.Second
	if interval <= 0 {
		interval = defaultHeartbeatSec * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				state.mu.Lock()
				tc := state.TickCount
				state.mu.Unlock()
				payload := heartbeatPayload{
					Host:      m.host,
					Ts:        time.Now().UnixMilli(),
					TickCount: tc,
				}
				if err := m.post("/api/v1/agents/"+m.agent.Name+"/heartbeat", payload); err != nil {
					Emit(Event{
						Type:  EventText,
						Agent: m.agent.Name,
						Host:  m.host,
						Text:  fmt.Sprintf("mgmt heartbeat error: %v", err),
					})
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}

// StartConfigPoller polls the management plane for config updates and applies
// them via the provided reload callback. Runs until ctx is cancelled.
//
// onReload is only invoked when the polled config actually differs from the
// last-applied config. Polls that come back unchanged are silent — without
// this guard, a 60s poll cycle would emit a `config_reloaded` event every
// minute even when no operator touched the config, drowning the Live Events
// feed in noise.
func (m *MgmtPlane) StartConfigPoller(ctx context.Context, onReload func(remoteConfig)) {
	interval := time.Duration(m.cfg.PollSec) * time.Second
	if interval <= 0 {
		interval = defaultPollSec * time.Second
	}
	var lastApplied remoteConfig
	var hasApplied bool
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				rc, err := m.pollConfig()
				if err != nil {
					Emit(Event{
						Type:  EventText,
						Agent: m.agent.Name,
						Host:  m.host,
						Text:  fmt.Sprintf("mgmt poll error: %v", err),
					})
					continue
				}
				m.mu.Lock()
				m.remote = rc
				m.mu.Unlock()
				if !hasApplied || rc != lastApplied {
					lastApplied = rc
					hasApplied = true
					onReload(rc)
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}

// Remote returns the latest config received from the management plane.
func (m *MgmtPlane) Remote() remoteConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.remote
}

// StartKnownIssuesPoller refreshes the local known-issues cache on the same
// cadence as the config poll. Each refresh fetches every fingerprint the
// CP has triaged for THIS agent across all hosts in the last 7 days.
//
// The harvester consults the cache before emitting; daemons without a
// management plane skip this entirely (callers gate on m != nil).
func (m *MgmtPlane) StartKnownIssuesPoller(ctx context.Context) {
	interval := time.Duration(m.cfg.PollSec) * time.Second
	if interval <= 0 {
		interval = defaultPollSec * time.Second
	}
	go func() {
		// Refresh once at startup so the daemon doesn't fly blind for the
		// first poll interval.
		if issues, err := m.fetchKnownIssues(); err == nil {
			m.applyKnownIssues(issues)
		}
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				issues, err := m.fetchKnownIssues()
				if err != nil {
					// Silent — known-issues is an enhancement; failing is
					// not a hard error and we don't want to flood the event
					// log if the CP is briefly unreachable.
					continue
				}
				m.applyKnownIssues(issues)
			}
		}
	}()
}

// KnownIssue returns the cached triage entry for a fingerprint, or
// (zero, false) when nothing matches. Safe for concurrent use.
func (m *MgmtPlane) KnownIssue(fingerprint string) (KnownIssue, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	k, ok := m.knownIssues[fingerprint]
	return k, ok
}

// LookupFindings calls /api/v1/agents/{name}/findings/search?q=...&limit=N.
// Used by the daemon-side lookup_findings tool exposed to the LLM. Each
// call blocks until the CP responds or the context times out.
func (m *MgmtPlane) LookupFindings(ctx context.Context, query string, limit int) ([]LookupResult, error) {
	if limit <= 0 {
		limit = 5
	}
	url := fmt.Sprintf("%s/api/v1/agents/%s/findings/search?q=%s&limit=%d",
		m.cfg.URL, m.agent.Name,
		urlQueryEscape(query), limit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("lookup_findings: HTTP %d", resp.StatusCode)
	}
	var out []LookupResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func (m *MgmtPlane) fetchKnownIssues() ([]KnownIssue, error) {
	url := m.cfg.URL + "/api/v1/agents/" + m.agent.Name + "/known-issues"
	resp, err := m.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("known-issues: HTTP %d", resp.StatusCode)
	}
	var out []KnownIssue
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func (m *MgmtPlane) applyKnownIssues(list []KnownIssue) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.knownIssues == nil || len(list) > 0 {
		m.knownIssues = make(map[string]KnownIssue, len(list))
	}
	for _, k := range list {
		if k.Fingerprint == "" {
			continue
		}
		m.knownIssues[k.Fingerprint] = k
	}
}

// urlQueryEscape is used in place of net/url to avoid importing it just for
// this — query is constrained server-side and our needs are minimal.
func urlQueryEscape(s string) string {
	// Replace spaces and a few obvious URL specials. The CP performs the
	// real LIKE-escape; this is just to keep the URL well-formed.
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == ' ':
			out = append(out, '+')
		case (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'),
			c == '-' || c == '_' || c == '.' || c == '~':
			out = append(out, c)
		default:
			out = append(out, '%', "0123456789ABCDEF"[c>>4], "0123456789ABCDEF"[c&0xf])
		}
	}
	return string(out)
}

func (m *MgmtPlane) pollConfig() (remoteConfig, error) {
	url := m.cfg.URL + "/api/v1/agents/" + m.agent.Name + "/config"
	resp, err := m.client.Get(url)
	if err != nil {
		return remoteConfig{}, err
	}
	defer resp.Body.Close()
	var rc remoteConfig
	if err := json.NewDecoder(resp.Body).Decode(&rc); err != nil {
		return remoteConfig{}, fmt.Errorf("decoding config: %w", err)
	}
	return rc, nil
}

func (m *MgmtPlane) post(path string, payload interface{}) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	resp, err := m.client.Post(m.cfg.URL+path, "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("mgmt %s: HTTP %d", path, resp.StatusCode)
	}
	return nil
}

// buildMTLSConfig constructs a tls.Config using certs from certDir.
// Expected files:
//
//	<certDir>/client.crt  — agent client certificate
//	<certDir>/client.key  — agent private key
//	<certDir>/ca.crt      — CA certificate to verify the server
func buildMTLSConfig(certDir string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(
		certDir+"/client.crt",
		certDir+"/client.key",
	)
	if err != nil {
		return nil, fmt.Errorf("loading agent cert: %w", err)
	}

	caCert, err := os.ReadFile(certDir + "/ca.crt")
	if err != nil {
		return nil, fmt.Errorf("loading CA cert: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("invalid CA cert in %s/ca.crt", certDir)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		MinVersion:   tls.VersionTLS13,
	}, nil
}

// Version returns the okesu binary version string.
// Populated by ldflags at build time; defaults to "dev".
var buildVersion = "dev"

func Version() string { return buildVersion }
