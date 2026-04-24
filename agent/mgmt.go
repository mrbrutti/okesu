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

// MgmtPlane manages the connection to the optional management plane.
type MgmtPlane struct {
	cfg    MgmtConfig
	agent  Config
	host   string
	client *http.Client

	mu       sync.RWMutex
	remote   remoteConfig
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
func (m *MgmtPlane) StartConfigPoller(ctx context.Context, onReload func(remoteConfig)) {
	interval := time.Duration(m.cfg.PollSec) * time.Second
	if interval <= 0 {
		interval = defaultPollSec * time.Second
	}
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
				onReload(rc)
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
//	<certDir>/agent.crt  — agent client certificate
//	<certDir>/agent.key  — agent private key
//	<certDir>/ca.crt     — CA certificate to verify the server
func buildMTLSConfig(certDir string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(
		certDir+"/agent.crt",
		certDir+"/agent.key",
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
