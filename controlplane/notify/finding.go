// Package notify routes finding events to externally-configured channels
// (Slack, email, generic webhook).
//
// Architecture:
//
//   webhook receiver (api/webhook.go)
//     ↓ broadcaster.Publish(line)
//   notify.Worker.Run subscribes
//     ↓ filters to type=="finding"
//   matchRules(finding, rules) → []*Channel
//     ↓ for each match: store.CreateDelivery + enqueue
//   delivery goroutine pool drains the queue
//     ↓ Slack/email/webhook send with retry
//   store.FinishDelivery on terminal status
package notify

import (
	"encoding/json"
	"strings"
)

// Finding is the subset of a finding event that the notify package needs.
// Decouples the worker from the agent.Event and DB Finding types.
type Finding struct {
	ID         int64           `json:"id"`           // optional — set when projecting from DB
	Ts         int64           `json:"ts"`
	Agent      string          `json:"agent"`
	Host       string          `json:"host"`
	Severity   string          `json:"severity"`
	Title      string          `json:"title"`
	Resource   string          `json:"resource"`
	Evidence   string          `json:"evidence"`
	Action     string          `json:"recommended_action"`
	DedupKey   string          `json:"dedup_key"`
	RawJSON    json.RawMessage `json:"-"`
}

// ParseFinding extracts the finding fields from a webhook JSONL line.
// Returns nil if the event is not a finding or the body is unparseable.
func ParseFinding(line []byte) *Finding {
	var raw map[string]any
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil
	}
	t, _ := raw["type"].(string)
	if t != "finding" {
		return nil
	}
	f := &Finding{RawJSON: line}
	getString := func(key string) string {
		v, _ := raw[key].(string)
		return v
	}
	getInt64 := func(key string) int64 {
		switch v := raw[key].(type) {
		case float64:
			return int64(v)
		case int64:
			return v
		}
		return 0
	}
	f.Ts = getInt64("ts")
	f.Agent = getString("agent")
	f.Host = getString("host")
	f.Severity = strings.ToUpper(getString("severity"))
	f.Title = getString("title")
	f.Resource = getString("resource")
	f.Evidence = getString("evidence")
	f.Action = getString("recommended_action")
	f.DedupKey = getString("dedup_key")
	return f
}

// severityRank maps severity strings to numeric levels for threshold
// comparisons. Unknown severities sort to "INFO" (lowest).
func severityRank(s string) int {
	switch strings.ToUpper(s) {
	case "CRITICAL":
		return 5
	case "HIGH":
		return 4
	case "MEDIUM":
		return 3
	case "LOW":
		return 2
	case "INFO":
		return 1
	}
	return 1
}

// SeverityAtLeast reports whether actual >= threshold.
func SeverityAtLeast(actual, threshold string) bool {
	return severityRank(actual) >= severityRank(threshold)
}
