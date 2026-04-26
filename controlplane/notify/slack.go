package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// SlackConfig is the JSON shape persisted in notification_channels.config
// for type=slack.
type SlackConfig struct {
	WebhookURL      string `json:"webhook_url"`
	ChannelOverride string `json:"channel_override,omitempty"` // e.g. "#alerts"
}

// SlackSender posts a finding to a Slack incoming webhook.
type SlackSender struct {
	HTTP *http.Client
}

// Send delivers f to the channel. Honors ctx for timeout.
func (s *SlackSender) Send(ctx context.Context, raw []byte, f *Finding) error {
	var cfg SlackConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("invalid slack config: %w", err)
	}
	if cfg.WebhookURL == "" {
		return fmt.Errorf("slack webhook_url is required")
	}

	body := slackPayload(f, cfg.ChannelOverride)
	b, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.WebhookURL, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

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
		return fmt.Errorf("slack: HTTP %d", resp.StatusCode)
	}
	return nil
}

// slackPayload builds the Block Kit message for a finding.
// Uses the simple `text` shape (renders fine in any incoming webhook) plus
// `blocks` for the rich layout when the workspace supports them.
func slackPayload(f *Finding, channelOverride string) map[string]any {
	emoji := severityEmoji(f.Severity)
	headerText := fmt.Sprintf("%s %s — %s", emoji, f.Severity, f.Title)
	plain := headerText
	if f.Agent != "" {
		plain += " · agent: " + f.Agent
	}
	if f.Host != "" {
		plain += " · host: " + f.Host
	}

	blocks := []map[string]any{
		{
			"type": "header",
			"text": map[string]any{"type": "plain_text", "text": headerText, "emoji": true},
		},
	}

	var fields []map[string]any
	addField := func(label, value string) {
		if value == "" {
			return
		}
		fields = append(fields, map[string]any{
			"type": "mrkdwn",
			"text": fmt.Sprintf("*%s*\n%s", label, value),
		})
	}
	addField("Agent", f.Agent)
	addField("Host", f.Host)
	if f.Resource != "" {
		addField("Resource", "`"+f.Resource+"`")
	}
	if len(fields) > 0 {
		blocks = append(blocks, map[string]any{"type": "section", "fields": fields})
	}

	if f.Evidence != "" {
		blocks = append(blocks, map[string]any{
			"type": "section",
			"text": map[string]any{"type": "mrkdwn", "text": "*Evidence*\n```" + truncate(f.Evidence, 1500) + "```"},
		})
	}
	if f.Action != "" {
		blocks = append(blocks, map[string]any{
			"type": "section",
			"text": map[string]any{"type": "mrkdwn", "text": "*Recommended action*\n" + truncate(f.Action, 1500)},
		})
	}

	out := map[string]any{
		"text":   plain,
		"blocks": blocks,
	}
	if channelOverride != "" {
		out["channel"] = channelOverride
	}
	return out
}

func severityEmoji(s string) string {
	switch s {
	case "CRITICAL":
		return ":rotating_light:"
	case "HIGH":
		return ":warning:"
	case "MEDIUM":
		return ":large_orange_circle:"
	case "LOW":
		return ":large_yellow_circle:"
	default:
		return ":information_source:"
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
