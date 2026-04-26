package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// WebhookConfig is the JSON shape persisted in notification_channels.config
// for type=webhook. Generic outbound HTTPS POST destination.
type WebhookConfig struct {
	URL          string            `json:"url"`
	Secret       string            `json:"secret,omitempty"`        // optional HMAC-SHA256 signing
	ExtraHeaders map[string]string `json:"extra_headers,omitempty"` // sent verbatim
}

// WebhookSender posts the finding payload to the configured URL.
//
// When a secret is set, signs the payload with HMAC-SHA256 and includes
// the signature in `X-Okesu-Signature` (mirrors the daemon webhook
// receiver's contract — the same verification helper works on both ends).
type WebhookSender struct {
	HTTP *http.Client
}

func (s *WebhookSender) Send(ctx context.Context, raw []byte, f *Finding) error {
	var cfg WebhookConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("invalid webhook config: %w", err)
	}
	if cfg.URL == "" {
		return fmt.Errorf("webhook: url is required")
	}

	payload, err := json.Marshal(f)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.URL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Okesu-Source", "control-plane")
	req.Header.Set("X-Okesu-Severity", f.Severity)
	req.Header.Set("X-Okesu-Timestamp", fmt.Sprintf("%d", time.Now().UnixMilli()))
	for k, v := range cfg.ExtraHeaders {
		req.Header.Set(k, v)
	}
	if cfg.Secret != "" {
		mac := hmac.New(sha256.New, []byte(cfg.Secret))
		mac.Write(payload)
		req.Header.Set("X-Okesu-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}

	client := s.HTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("webhook %s: HTTP %d", cfg.URL, resp.StatusCode)
	}
	return nil
}
