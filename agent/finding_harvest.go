package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// KnownIssuesLookup is the interface the harvester uses to consult the
// CP-supplied triage cache before emitting. *MgmtPlane satisfies this.
type KnownIssuesLookup interface {
	KnownIssue(fingerprint string) (KnownIssue, bool)
}

// HarvestFindings scans {stateDir}/findings/ for *.json files written by the
// LLM during this tick, emits one EventFinding for each new finding, and
// moves processed files to {stateDir}/findings/processed/ so they aren't
// re-emitted on the next tick.
//
// The path matches what the LLM is told to write to in the system-prompt
// template (`{{.StateDir}}/findings/{{.TickTime}}.json`), which resolves
// directly to the agent's `stateDir` from its agent file.
//
// Three file shapes are accepted:
//
//   1. Top-level array of finding objects (edr, instance-threat).
//   2. Top-level single finding object (some agents write one per file).
//   3. Top-level wrapper object with a `findings: []` field (sre-health,
//      compliance-auditor — they emit a health/audit report and put
//      individual findings in a nested array).
//
// Each finding's dedup_key is checked against the in-memory dedup cache
// (state.Dedup); duplicates within the configured TTL are silently skipped
// so an LLM that re-reports the same persistent issue every tick doesn't
// flood the Control Plane.
//
// Findings are also enriched on the way through:
//   - Title is normalized (volatile prefixes / suffixes stripped) so the
//     same recurring issue groups together in the dashboard.
//   - When the LLM didn't supply category / process_pid / path / network,
//     the harvester infers them from the resource string heuristically.
func HarvestFindings(state *DaemonState, stateDir, agentName, host string, tickNum int64, dedupTTL time.Duration, known KnownIssuesLookup) int {
	if stateDir == "" || agentName == "" {
		return 0
	}
	dir := filepath.Join(stateDir, "findings")
	processedDir := filepath.Join(dir, "processed")

	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}

	emitted := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		full := filepath.Join(dir, name)
		raw, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		findings, err := parseFindingsFile(raw)
		if err != nil {
			// Move malformed files into processed/<name>.bad so they're not
			// re-read every tick; an operator can inspect them later.
			_ = os.MkdirAll(processedDir, 0755)
			_ = os.Rename(full, filepath.Join(processedDir, name+".bad"))
			continue
		}
		for _, f := range findings {
			f.normalize()
			fp := f.fingerprint()
			// Layer 2: CP-supplied known-issues cache. If the operator has
			// triaged this fingerprint (false_positive, wontfix, resolved,
			// acknowledged, investigating), suppress silently — the
			// operator has already seen it and decided. We do NOT add to
			// the local dedup cache so an un-triage takes effect on the
			// next tick instead of after dedupTTL expires.
			if known != nil && fp != "" {
				if k, ok := known.KnownIssue(fp); ok && k.Status != "" && k.Status != "open" {
					continue
				}
			}
			if f.shouldDedup(state, dedupTTL) {
				continue
			}
			// Wire the canonical fingerprint as the dedup_key so the CP's
			// grouping query collapses LLM variants the same way the
			// daemon-side dedup did. The original `dedup_key` is preserved
			// inside Attributes for forensics.
			ev := Event{
				Type:            EventFinding,
				Agent:           agentName,
				Host:            host,
				Tick:            tickNum,
				Severity:        strings.ToUpper(f.Severity),
				Title:           f.Title,
				Resource:        f.Resource,
				Evidence:        f.evidenceString(),
				DedupKey:        f.fingerprint(),
				Text:            f.RecommendedAction, // surface in event log/UI
				Ts:              time.Now().UnixMilli(),
				Category:        f.Category,
				ProcessPID:      f.ProcessPID,
				ProcessName:     f.ProcessName,
				Path:            f.Path,
				NetworkEndpoint: f.NetworkEndpoint,
				CVE:             f.CVE,
				Tags:            f.tagsString(),
			}
			if extra := f.attributesJSON(); len(extra) > 0 {
				ev.Attributes = extra
			}
			Emit(ev)
			emitted++
		}
		// Move whatever we read into processed/ so it doesn't get re-harvested.
		_ = os.MkdirAll(processedDir, 0755)
		_ = os.Rename(full, filepath.Join(processedDir, name))
	}
	return emitted
}

// findingFile is the on-disk shape the LLM writes per the system prompts.
// All fields are best-effort — agents fill what they extract; the rest stay
// empty and the harvester / server fill in defaults.
type findingFile struct {
	Severity          string      `json:"severity"`
	Title             string      `json:"title"`
	Resource          string      `json:"resource"`
	Evidence          interface{} `json:"evidence"` // string or []string
	RecommendedAction string      `json:"recommended_action"`
	DedupKey          string      `json:"dedup_key"`

	// Phase 12 enrichment — explicit structured fields the LLM may emit.
	Category        string                 `json:"category"`         // process|file|network|cert|cloud|identity|config|other
	ProcessPID      int64                  `json:"process_pid"`
	ProcessName     string                 `json:"process_name"`
	Path            string                 `json:"path"`
	NetworkEndpoint string                 `json:"network_endpoint"` // host:port or URL
	CVE             string                 `json:"cve"`
	Tags            interface{}            `json:"tags"`             // []string preferred, "a,b,c" accepted
	Attributes      map[string]interface{} `json:"attributes"`       // free-form
}

// normalize strips volatile prefixes/suffixes from the title and infers
// missing structured fields from the resource string.
func (f *findingFile) normalize() {
	f.Title = NormalizeFindingTitle(f.Title)
	f.inferFromResource()
	if f.Category == "" {
		f.Category = inferCategory(f.Resource, f.Path, f.NetworkEndpoint, f.ProcessPID)
	}
}

func (f *findingFile) evidenceString() string {
	switch v := f.Evidence.(type) {
	case string:
		return v
	case []interface{}:
		lines := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				lines = append(lines, s)
			}
		}
		return strings.Join(lines, "\n")
	}
	return ""
}

// tagsString returns the tags as a comma-separated list, trimmed and
// lowercased so equality checks are robust.
func (f *findingFile) tagsString() string {
	var raw []string
	switch v := f.Tags.(type) {
	case string:
		raw = strings.Split(v, ",")
	case []interface{}:
		for _, item := range v {
			if s, ok := item.(string); ok {
				raw = append(raw, s)
			}
		}
	}
	out := raw[:0]
	seen := map[string]struct{}{}
	for _, t := range raw {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" {
			continue
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return strings.Join(out, ",")
}

func (f *findingFile) attributesJSON() string {
	if len(f.Attributes) == 0 {
		return ""
	}
	b, err := json.Marshal(f.Attributes)
	if err != nil {
		return ""
	}
	return string(b)
}

// inferFromResource pulls structured values out of common resource shapes
// when the LLM didn't already provide them. This is best-effort — operators
// can override by providing explicit fields.
//
// Recognized shapes (multiple separated by commas can co-exist in resource):
//
//	pid:1337                  → ProcessPID
//	pid:1337, user:root       → ProcessPID
//	path:/etc/cron.d/foo      → Path
//	process:nginx, pid:1337   → ProcessName + ProcessPID
//	binary:/usr/bin/foo       → Path
//	host:api.example.com:443  → NetworkEndpoint
//	cve:CVE-2024-12345        → CVE
//	socket:fd=8 ...           → (ignored, no extraction)
func (f *findingFile) inferFromResource() {
	if f.Resource == "" {
		return
	}
	for _, raw := range strings.Split(f.Resource, ",") {
		part := strings.TrimSpace(raw)
		// Allow either "k:v" or "k=v"
		var key, val string
		if i := strings.IndexAny(part, ":="); i > 0 {
			key = strings.ToLower(strings.TrimSpace(part[:i]))
			val = strings.TrimSpace(part[i+1:])
		} else {
			continue
		}
		switch key {
		case "pid":
			if f.ProcessPID == 0 {
				if n, err := strconv.ParseInt(val, 10, 64); err == nil {
					f.ProcessPID = n
				}
			}
		case "process", "exe", "comm":
			if f.ProcessName == "" {
				f.ProcessName = val
			}
		case "binary":
			if f.Path == "" {
				f.Path = val
			}
			if f.ProcessName == "" {
				f.ProcessName = filepath.Base(val)
			}
		case "path", "file":
			if f.Path == "" {
				f.Path = val
			}
		case "host", "endpoint", "url":
			if f.NetworkEndpoint == "" {
				f.NetworkEndpoint = val
			}
		case "cve":
			if f.CVE == "" {
				f.CVE = val
			}
		}
	}
}

// inferCategory is a coarse classifier when the LLM didn't supply one.
// Order matters — pick the most specific signal first.
func inferCategory(resource, path, network string, pid int64) string {
	r := strings.ToLower(resource)
	switch {
	case pid > 0 || strings.HasPrefix(r, "pid:") || strings.HasPrefix(r, "process:"):
		return "process"
	case path != "" || strings.HasPrefix(r, "path:") || strings.HasPrefix(r, "file:") ||
		strings.HasPrefix(r, "binary:") || strings.HasPrefix(r, "/"):
		return "file"
	case network != "" || strings.Contains(r, "://") || strings.HasPrefix(r, "host:") ||
		strings.HasPrefix(r, "ip:") || strings.HasPrefix(r, "endpoint:"):
		return "network"
	case strings.Contains(r, "cert") || strings.Contains(r, "tls"):
		return "cert"
	case strings.HasPrefix(r, "ocid"):
		return "cloud"
	case strings.HasPrefix(r, "user:") || strings.HasPrefix(r, "iam:") || strings.HasPrefix(r, "role:"):
		return "identity"
	case strings.HasPrefix(r, "cron") || strings.HasPrefix(r, "config:") || strings.HasPrefix(r, "unit:"):
		return "config"
	}
	return ""
}

// shouldDedup checks the in-memory dedup cache. Returns true if this finding
// has been emitted within the TTL window and should be suppressed.
//
// Uses the canonical helpers (IsDuplicate / AddToDedup) so we follow the
// same "store expiry time" convention the rest of the daemon uses —
// PruneDedup interprets each value as `now+ttl` and silently wipes
// anything stored as a "last-seen" timestamp.
//
// Dedup key is the computed fingerprint (see fingerprint method), not the
// LLM's raw dedup_key — LLMs drift the latter between ticks
// ("api.example.com" → "api.example.com:443" → "api.example.com/health")
// and would otherwise defeat the cache. The fingerprint normalizes
// title, resource, and host into a stable hash regardless of which
// variant the model picked this turn.
func (f *findingFile) shouldDedup(state *DaemonState, ttl time.Duration) bool {
	if state == nil || ttl <= 0 {
		return false
	}
	fp := f.fingerprint()
	if fp == "" {
		return false
	}
	if state.IsDuplicate(fp, ttl) {
		return true
	}
	state.AddToDedup(fp, ttl)
	return false
}

// fingerprint computes a stable hash for the finding that survives
// LLM-induced volatility in dedup_key, title, and resource.
//
// The previous version included the LLM's title and dedup_key as
// fingerprint components, which fragmented the cache when the model
// rephrased the same root cause across ticks ("OKESU_HEALTH_URLS
// not set" → "placeholder example.com" → "probing placeholder"
// produced three different fingerprints). With prose-drift
// disabled the same root cause collapses to one fingerprint and
// the CP's SupersedeOpenDedups path can roll up siblings.
//
// Components (all lowercased / trimmed):
//   - severity  (CRITICAL, HIGH, …)
//   - category  (process|file|network|cert|cloud|identity|config|other)
//   - normalized resource root (first key/value token only)
//   - structured signals when present: process_pid, process_name,
//     path, network_endpoint, cve, attributes-hash
//
// Title is used ONLY when no structured signal is present at all —
// otherwise it's a noise source. The LLM's raw dedup_key is dropped
// entirely; it has the same prose-drift problem.
func (f *findingFile) fingerprint() string {
	parts := []string{
		strings.ToUpper(strings.TrimSpace(f.Severity)),
		strings.ToLower(strings.TrimSpace(f.Category)),
		strings.ToLower(resourceRoot(f.Resource)),
	}
	structuredSignal := false
	if f.ProcessPID > 0 {
		parts = append(parts, "pid:"+strconv.FormatInt(f.ProcessPID, 10))
		structuredSignal = true
	}
	if f.ProcessName != "" {
		parts = append(parts, "proc:"+strings.ToLower(strings.TrimSpace(f.ProcessName)))
		structuredSignal = true
	}
	if f.Path != "" {
		parts = append(parts, "path:"+strings.ToLower(f.Path))
		structuredSignal = true
	}
	if f.NetworkEndpoint != "" {
		parts = append(parts, "ep:"+normalizeEndpoint(f.NetworkEndpoint))
		structuredSignal = true
	}
	if f.CVE != "" {
		parts = append(parts, "cve:"+strings.ToLower(strings.TrimSpace(f.CVE)))
		structuredSignal = true
	}
	if h := stableAttributesHash(f.Attributes); h != "" {
		parts = append(parts, "attr:"+h)
		structuredSignal = true
	}
	// Title fallback — only when we have NO structured signal, NO
	// process/path/endpoint/cve/attributes. Without this the
	// fingerprint would collapse "every INFO finding from sre-health
	// on host X" into one row, which is too aggressive for findings
	// the LLM emitted with prose only.
	if !structuredSignal {
		title := NormalizeFindingTitle(f.Title)
		if title != "" {
			parts = append(parts, "t:"+strings.ToLower(title))
		}
	}
	return strings.Join(parts, "|")
}

// stableAttributesHash hashes the attributes map with sorted keys so
// the result is independent of LLM key-ordering whim. Returns ""
// when there are no attributes — caller treats absence as "no
// structured signal."
func stableAttributesHash(attrs map[string]interface{}) string {
	if len(attrs) == 0 {
		return ""
	}
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{0})
		// Stringify the value deterministically. json.Marshal sorts
		// nested map keys per spec — combined with our outer sort
		// the output is stable regardless of LLM emission order.
		b, err := json.Marshal(attrs[k])
		if err != nil {
			continue
		}
		h.Write(b)
		h.Write([]byte{0})
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:8]) // first 16 hex chars — collision-tolerant for fleet sizes we care about
}

// resourceRoot extracts the first stable token from a "k:v[, k:v]*" resource
// string. Used so "host:api.example.com" and "host:api.example.com,
// url:https://api.example.com/health" hash to the same fingerprint.
func resourceRoot(resource string) string {
	if resource == "" {
		return ""
	}
	first := strings.SplitN(strings.TrimSpace(resource), ",", 2)[0]
	first = strings.TrimSpace(first)
	if i := strings.IndexAny(first, ":="); i > 0 {
		key := strings.ToLower(strings.TrimSpace(first[:i]))
		val := strings.TrimSpace(first[i+1:])
		// Strip path / fragment from URLs and host:port.
		val = strings.TrimPrefix(val, "https://")
		val = strings.TrimPrefix(val, "http://")
		if j := strings.IndexAny(val, "/?#"); j >= 0 {
			val = val[:j]
		}
		// Strip well-known default ports so :443 / :80 don't fork the key.
		val = strings.TrimSuffix(val, ":443")
		val = strings.TrimSuffix(val, ":80")
		return key + ":" + strings.ToLower(val)
	}
	return strings.ToLower(first)
}

// normalizeEndpoint strips protocol, default ports, and paths so endpoint
// strings hash the same regardless of LLM phrasing.
func normalizeEndpoint(ep string) string {
	ep = strings.TrimSpace(strings.ToLower(ep))
	ep = strings.TrimPrefix(ep, "https://")
	ep = strings.TrimPrefix(ep, "http://")
	if j := strings.IndexAny(ep, "/?#"); j >= 0 {
		ep = ep[:j]
	}
	ep = strings.TrimSuffix(ep, ":443")
	ep = strings.TrimSuffix(ep, ":80")
	return ep
}

// normalizeDedupKey lowercases and strips trailing port/path noise so
// "endpoint+api.example.com" and "endpoint+api.example.com:443" collapse.
func normalizeDedupKey(k string) string {
	k = strings.ToLower(strings.TrimSpace(k))
	k = strings.TrimSuffix(k, ":443")
	k = strings.TrimSuffix(k, ":80")
	if j := strings.IndexAny(k, "/?#"); j >= 0 {
		k = k[:j]
	}
	return k
}

// parseFindingsFile accepts the three on-disk shapes documented above.
func parseFindingsFile(data []byte) ([]findingFile, error) {
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "[") {
		var arr []findingFile
		if err := json.Unmarshal(data, &arr); err != nil {
			return nil, err
		}
		return filterNonEmpty(arr), nil
	}
	// Try the wrapper-with-nested-array shape first; fall back to single object.
	var wrapper struct {
		Findings []findingFile `json:"findings"`
	}
	if err := json.Unmarshal(data, &wrapper); err == nil && len(wrapper.Findings) > 0 {
		return filterNonEmpty(wrapper.Findings), nil
	}
	var one findingFile
	if err := json.Unmarshal(data, &one); err != nil {
		return nil, err
	}
	return filterNonEmpty([]findingFile{one}), nil
}

// filterNonEmpty drops entries that lack both severity and title — they
// almost certainly came from a wrapper file (status report, audit summary)
// where the top-level shape isn't a finding.
func filterNonEmpty(in []findingFile) []findingFile {
	out := in[:0]
	for _, f := range in {
		if f.Severity == "" && f.Title == "" {
			continue
		}
		out = append(out, f)
	}
	return out
}

// ── Title normalization ────────────────────────────────────────────────────

// Volatile prefix patterns LLMs love to invent: "PERSISTENT (TICK 87):",
// "ONGOING —", "SUSTAINED:", "PROLONGED OUTAGE (TICK 85):", "[Tick 4]",
// "CONTINUING (Tick 12):". These describe state of the same underlying
// finding and would create a fresh group every tick — strip them.
var volatilePrefixRE = regexp.MustCompile(
	`^\s*(?i)` +
		`(?:` +
		`(?:persistent|ongoing|sustained|continuing|prolonged|recurring|repeating|active|new)` +
		`(?:\s+(?:outage|incident|alert|alarm|finding))?` +
		`(?:\s*\(?\s*tick[\s_-]*\d+\s*\)?)?` +
		`(?:\s*\[\s*tick[\s_-]*\d+\s*\])?` +
		`\s*[—:\-–]\s*` +
		`)+`,
)

// Volatile suffix patterns: trailing "— 7th Consecutive Tick", "(5+ ticks)",
// "[since tick 12]", "(persistent for 30+ minutes)".
var volatileSuffixRE = regexp.MustCompile(
	`(?i)` +
		`\s*(?:` +
		`[—\-–]\s*\d+\s*(?:st|nd|rd|th)\s*consecutive\s*tick` +
		`|\(\s*\d+\+?\s*(?:ticks?|minutes?|hours?|m|h)\b[^)]*\)` +
		`|\[\s*(?:since\s+)?tick[\s_-]*\d+\s*\]` +
		`|\(\s*tick\s*\d+\s*\)` +
		`)\s*$`,
)

// Multi-space collapse — after stripping the prefixes/suffixes we may have
// double spaces or stray punctuation.
var collapseSpaceRE = regexp.MustCompile(`\s{2,}`)

// NormalizeFindingTitle removes per-tick volatility from a finding title so
// the same recurring issue collapses into one group. Idempotent — applying
// it twice yields the same string.
//
// Exposed so the CP webhook receiver can re-normalize titles received from
// older or external producers that don't run the harvester.
func NormalizeFindingTitle(t string) string {
	if t == "" {
		return t
	}
	prev := ""
	out := strings.TrimSpace(t)
	// Apply repeatedly until stable — stacked prefixes like
	// "PERSISTENT (Tick 43): SUSTAINED:" do exist.
	for prev != out {
		prev = out
		out = volatilePrefixRE.ReplaceAllString(out, "")
		out = volatileSuffixRE.ReplaceAllString(out, "")
		out = strings.TrimSpace(out)
	}
	out = collapseSpaceRE.ReplaceAllString(out, " ")
	return out
}
