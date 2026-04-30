package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// PrependLessons returns the system prompt with the agent's recent
// lessons rendered as a markdown bullet list under a "## Lessons from
// prior runs" header, then a blank line, then the original prompt.
// Pure function — caller decides whether to call it.
//
// Whitespace-only entries are dropped — they'd render as empty bullets
// and add no signal to the model.
func PrependLessons(systemPrompt string, lessons []string) string {
	cleaned := make([]string, 0, len(lessons))
	for _, l := range lessons {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		cleaned = append(cleaned, l)
	}
	if len(cleaned) == 0 {
		return systemPrompt
	}
	var b strings.Builder
	b.WriteString("## Lessons from prior runs\n\n")
	for _, l := range cleaned {
		b.WriteString("- ")
		b.WriteString(l)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(systemPrompt)
	return b.String()
}

// FetchLessons hits the CP's lessons endpoint and returns the lesson
// texts (newest first). Best-effort — returns nil on any error so the
// caller can fall back to the raw prompt.
//
// The mgmt-plane endpoint lives at /api/v1/agents/{name}/lessons
// (mTLS-gated, alongside known-issues and findings/search). Callers
// that already have an mTLS-configured *http.Client (e.g. MgmtPlane)
// should pass it; otherwise a default client with a 5s timeout is used.
//
// JSON shape on the wire is db.AgentLesson; we only need the Text
// field, so a minimal decode struct is used.
func FetchLessons(ctx context.Context, cpURL, agentName string, client *http.Client) []string {
	if cpURL == "" || agentName == "" {
		return nil
	}
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	url := strings.TrimRight(cpURL, "/") + "/api/v1/agents/" + agentName + "/lessons"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil
	}
	var rows []struct {
		Text string `json:"Text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.Text != "" {
			out = append(out, r.Text)
		}
	}
	return out
}

// ApplyLessonsToPrompt fetches lessons (if cpURL is configured) and
// returns a new SystemPrompt with them prepended. Best-effort: any
// failure (network, no CP, no lessons) returns the input prompt
// unchanged. agentName comes from cfg.Name, cpURL from the daemon's
// mgmt config. The optional client is the daemon's mTLS-configured
// http.Client; nil falls back to a default client (which won't be able
// to authenticate against the mgmt plane, so callers running against a
// real CP should always pass their MgmtPlane client).
func ApplyLessonsToPrompt(ctx context.Context, cpURL, agentName, systemPrompt string, client *http.Client) string {
	lessons := FetchLessons(ctx, cpURL, agentName, client)
	return PrependLessons(systemPrompt, lessons)
}
