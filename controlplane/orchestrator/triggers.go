// Trigger probes — finding-driven and cron auto-runs for
// orchestrations.
//
// Both probes are read-only relative to the engine: they decide
// whether to spawn a run, then call back into the coordinator's
// public Trigger() function which seeds the run row + step records
// and kicks the engine. The coordinator owns the actual dispatch.
//
// Why combine: the cron and finding paths share evaluation primitives
// (the same EvalBool used by step `when` gates) and both need to
// stamp `last_fired_at` for dedupe / next-fire calculation. One small
// module beats two near-duplicate ones.

package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// FindingPayload is the subset of a finding the trigger filter sees.
// We deliberately keep it tight — the filter expression is executed
// on every finding, so cheap field access matters.
type FindingPayload struct {
	ID         int64             `json:"id"`
	Severity   string            `json:"severity"`
	Title      string            `json:"title"`
	Agent      string            `json:"agent"`
	Host       string            `json:"host"`
	Category   string            `json:"category"`
	DedupKey   string            `json:"dedup_key"`
	Resource   string            `json:"resource"`
	Attributes map[string]any    `json:"attributes,omitempty"`
}

// EvaluateFindingFilter runs an orchestration's `trigger.filter`
// against a finding. Returns true if the orchestration should fire.
// Empty filter returns false (operators must explicitly opt in).
//
// The expression sees `finding.<field>` — same dotted-path syntax as
// step templates. Examples:
//
//   finding.severity in ['HIGH', 'CRITICAL']
//   finding.agent == 'edr' && finding.host contains 'prod'
func EvaluateFindingFilter(filter string, payload FindingPayload) (bool, error) {
	if strings.TrimSpace(filter) == "" {
		return false, nil
	}
	env := Env{"finding": findingMap(payload)}
	return EvalBool(filter, env)
}

func findingMap(p FindingPayload) map[string]any {
	return map[string]any{
		"id":         p.ID,
		"severity":   p.Severity,
		"title":      p.Title,
		"agent":      p.Agent,
		"host":       p.Host,
		"category":   p.Category,
		"dedup_key":  p.DedupKey,
		"resource":   p.Resource,
		"attributes": p.Attributes,
	}
}

// FindingTriggerPayload is the JSON the engine's run record stores
// on a finding-triggered run, mirroring the shape `manual` payloads
// have for inputs but tagged with `kind: finding`. Step prompts then
// see `{{trigger.<field>}}` for every finding field.
func FindingTriggerPayload(p FindingPayload) string {
	out := map[string]any{
		"kind":       "finding",
		"finding_id": p.ID,
		"severity":   p.Severity,
		"title":      p.Title,
		"agent":      p.Agent,
		"host":       p.Host,
		"category":   p.Category,
		"dedup_key":  p.DedupKey,
		"resource":   p.Resource,
	}
	if len(p.Attributes) > 0 {
		out["attributes"] = p.Attributes
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// EnrichmentPayload is the snapshot of a single vendor's enrichment
// result that triggered the orchestration. Same dotted-path access
// as findings: filter expressions read `enrichment.<field>`.
//
// Common filter shapes:
//
//	enrichment.verdict == 'malicious'
//	enrichment.adapter == 'virustotal' && enrichment.score > 80
//	enrichment.kind == 'sha256' && enrichment.verdict in ['malicious','suspicious']
type EnrichmentPayload struct {
	IOCID           int64  `json:"ioc_id"`
	IOCKind         string `json:"kind"`
	NormalizedValue string `json:"normalized_value"`
	Adapter         string `json:"adapter"`
	Verdict         string `json:"verdict"`
	Score           int64  `json:"score"`
}

// EvaluateEnrichmentFilter runs an orchestration's `trigger.filter`
// against an enrichment write. Returns true if the orchestration
// should fire. Empty filter returns true — unlike findings (where
// the firehose demands an opt-in), enrichment writes are rare
// enough that a no-filter trigger ("fire on any enrichment") is a
// reasonable default. Operators wanting tighter scope add a filter.
func EvaluateEnrichmentFilter(filter string, payload EnrichmentPayload) (bool, error) {
	if strings.TrimSpace(filter) == "" {
		return true, nil
	}
	env := Env{"enrichment": enrichmentMap(payload)}
	return EvalBool(filter, env)
}

func enrichmentMap(p EnrichmentPayload) map[string]any {
	return map[string]any{
		"ioc_id":           p.IOCID,
		"kind":             p.IOCKind,
		"normalized_value": p.NormalizedValue,
		"adapter":          p.Adapter,
		"verdict":          p.Verdict,
		"score":            p.Score,
	}
}

// EnrichmentTriggerPayload is the JSON the engine's run record
// stores on an enrichment-triggered run. Step prompts then see
// `{{trigger.<field>}}` for each field above plus `kind` (the
// trigger kind, "ioc_enriched") and `ioc_id` re-named so it doesn't
// collide with `kind` (the IOC kind).
func EnrichmentTriggerPayload(p EnrichmentPayload) string {
	out := map[string]any{
		"kind":             "ioc_enriched",
		"ioc_id":           p.IOCID,
		"ioc_kind":         p.IOCKind,
		"normalized_value": p.NormalizedValue,
		"adapter":          p.Adapter,
		"verdict":          p.Verdict,
		"score":            p.Score,
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// CronTriggerPayload is the JSON for cron-triggered runs. `tick` is
// the unix-ms timestamp the scheduler decided to fire — useful for
// templates that want to scope queries to a window.
func CronTriggerPayload(tickAt time.Time) string {
	b, _ := json.Marshal(map[string]any{
		"kind": "cron",
		"tick": tickAt.UTC().Unix(),
		"at":   tickAt.UTC().Format(time.RFC3339),
	})
	return string(b)
}

// ── cron evaluator ──────────────────────────────────────────────────

// NextCronFire is the exported alias for callers outside this
// package (the api-side coordinator's cron probe). Internally we
// keep the lowercase name for symmetry with the rest of the file.
func NextCronFire(expr string, from time.Time) (time.Time, error) {
	return nextCronFire(expr, from)
}

// nextCronFire is a tiny 5-field cron evaluator (minute hour dom mon
// dow). Supports `*`, fixed values, ranges (`1-5`), step values
// (`*/15`), and lists (`1,3,5`). No second-level granularity (the
// orchestration use case doesn't need it) and no special characters
// like `?`/`L` (those exist for true cron quirks our scheduler doesn't
// need to honour).
//
// Returns the smallest time strictly greater than `from` that
// matches the schedule. A bad expression returns an error and the
// scheduler logs+skips that orchestration on the next tick.
func nextCronFire(expr string, from time.Time) (time.Time, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return time.Time{}, fmt.Errorf("cron: %q must have 5 fields (minute hour day-of-month month day-of-week)", expr)
	}
	min, err := parseField(fields[0], 0, 59)
	if err != nil {
		return time.Time{}, fmt.Errorf("cron minute: %w", err)
	}
	hour, err := parseField(fields[1], 0, 23)
	if err != nil {
		return time.Time{}, fmt.Errorf("cron hour: %w", err)
	}
	dom, err := parseField(fields[2], 1, 31)
	if err != nil {
		return time.Time{}, fmt.Errorf("cron day-of-month: %w", err)
	}
	mon, err := parseField(fields[3], 1, 12)
	if err != nil {
		return time.Time{}, fmt.Errorf("cron month: %w", err)
	}
	dow, err := parseField(fields[4], 0, 6) // 0=Sun..6=Sat
	if err != nil {
		return time.Time{}, fmt.Errorf("cron day-of-week: %w", err)
	}

	// Walk forward minute-by-minute up to a year before giving up
	// (catches expressions that can never match: e.g. day-of-month=31
	// with month=February). At 525 600 iterations / sec this is
	// negligible cost on the rare cron evaluation path.
	t := from.Add(time.Minute).Truncate(time.Minute)
	limit := t.Add(366 * 24 * time.Hour)
	for t.Before(limit) {
		if min[t.Minute()] && hour[t.Hour()] && mon[int(t.Month())] && dom[t.Day()] && dow[int(t.Weekday())] {
			return t, nil
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}, fmt.Errorf("cron: %q has no match within a year", expr)
}

// parseField turns a single cron field into a 0..n bitmap. Errors are
// caught at evaluation time, not at parse — saves us a separate
// validation pass since the scheduler retries failed orchestrations.
func parseField(f string, lo, hi int) ([]bool, error) {
	out := make([]bool, hi+1)
	for _, part := range strings.Split(f, ",") {
		part = strings.TrimSpace(part)
		// step: rangeOrStar/N
		step := 1
		if i := strings.Index(part, "/"); i >= 0 {
			s := part[i+1:]
			n := 0
			for _, c := range s {
				if c < '0' || c > '9' {
					return nil, fmt.Errorf("bad step %q", s)
				}
				n = n*10 + int(c-'0')
			}
			if n <= 0 {
				return nil, fmt.Errorf("step must be positive")
			}
			step = n
			part = part[:i]
		}
		// range: a-b, single, or *
		var a, b int
		if part == "*" {
			a, b = lo, hi
		} else if i := strings.Index(part, "-"); i >= 0 {
			va, err := parseInt(part[:i], lo, hi)
			if err != nil {
				return nil, err
			}
			vb, err := parseInt(part[i+1:], lo, hi)
			if err != nil {
				return nil, err
			}
			a, b = va, vb
		} else {
			v, err := parseInt(part, lo, hi)
			if err != nil {
				return nil, err
			}
			a, b = v, v
		}
		for v := a; v <= b; v += step {
			if v >= lo && v <= hi {
				out[v] = true
			}
		}
	}
	return out, nil
}

func parseInt(s string, lo, hi int) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("empty value")
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a number: %q", s)
		}
		n = n*10 + int(c-'0')
	}
	if n < lo || n > hi {
		return 0, fmt.Errorf("%d out of range [%d, %d]", n, lo, hi)
	}
	return n, nil
}

// quiet unused-import vet noise on builds where context isn't reached.
var _ = context.Background
