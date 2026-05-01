// prompt_entities.go captures the typed entity refs that get
// templated into a step's prompt string. The orchestrator engine
// calls BuildPromptEntities at render time; the result rides along
// with the prompt to the UI as a side-channel so SmartPayload can
// render entity chips at the JSON-substitution sites without
// re-parsing the prompt text.
package orchestrator

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
)

// PromptEntities is the wire shape persisted on
// orchestration_steps.prompt_entities. JSON-encoded.
type PromptEntities struct {
	Refs []PromptEntityRef `json:"refs"`
}

// PromptEntityRef points at one entity that was templated into the
// step's rendered prompt. Identity (kind + id-or-key) is sufficient
// to navigate to the live entity; the snapshot is the small subset
// of fields the UI chip displays at-a-glance, so the chip renders
// without a fetch.
type PromptEntityRef struct {
	CPInstanceID string         `json:"cp_instance_id,omitempty"`
	Kind         string         `json:"kind"` // finding | ioc | node | daimon | run | investigation | orchestration
	ID           int64          `json:"id,omitempty"`
	IOCKind      string         `json:"ioc_kind,omitempty"`
	IOCValue     string         `json:"ioc_value,omitempty"`
	Snapshot     map[string]any `json:"snapshot"`
	LiteralHash  string         `json:"literal_hash"`
}

// placeholderRe matches `{{path}}` template placeholders. Matches
// the same syntax Render uses (see template.go).
var placeholderRe = regexp.MustCompile(`\{\{\s*([^}|]+?)\s*(?:\|[^}]*)?\}\}`)

// BuildPromptEntities walks the placeholders in `template`, looks
// each path up in env, classifies the typed value (DispatchedFinding,
// map-shaped IOC/Node/etc.), and accumulates entity refs.
//
// cpInstanceID is the CP that owns the entities — passed through
// unchanged. Empty string means same-CP (the local CP); the wire
// shape omits the field via the omitempty tag.
//
// Returns nil when no placeholders resolve to recognised entity types
// so the caller can avoid emitting a noisy `{"refs":[]}` blob.
func BuildPromptEntities(template string, env Env, cpInstanceID string) (*PromptEntities, error) {
	matches := placeholderRe.FindAllStringSubmatch(template, -1)
	if len(matches) == 0 {
		return nil, nil
	}
	var refs []PromptEntityRef
	seen := map[string]bool{}
	for _, m := range matches {
		path := strings.TrimSpace(m[1])
		val, ok := lookupEnvPath(env, path)
		if !ok {
			continue
		}
		these := classify(val, cpInstanceID)
		for _, r := range these {
			if seen[r.LiteralHash] {
				continue
			}
			seen[r.LiteralHash] = true
			refs = append(refs, r)
		}
	}
	if len(refs) == 0 {
		return nil, nil
	}
	return &PromptEntities{Refs: refs}, nil
}

// classify inspects a typed value and returns 0+ entity refs.
// Recognised types: DispatchedFinding(s), map[string]any (shape-
// detected per kind via classifyMap below), and slices of either.
//
// Unrecognised types return nil — the value still gets stringified
// into the prompt as today, just without an entity ref.
func classify(v any, cpInstanceID string) []PromptEntityRef {
	switch x := v.(type) {
	case DispatchedFinding:
		return []PromptEntityRef{findingRef(x, cpInstanceID)}
	case []DispatchedFinding:
		out := make([]PromptEntityRef, 0, len(x))
		for _, f := range x {
			out = append(out, findingRef(f, cpInstanceID))
		}
		return out
	case map[string]any:
		return classifyMap(x, cpInstanceID)
	case []any:
		var out []PromptEntityRef
		for _, item := range x {
			if m, ok := item.(map[string]any); ok {
				out = append(out, classifyMap(m, cpInstanceID)...)
			}
		}
		return out
	case []map[string]any:
		var out []PromptEntityRef
		for _, m := range x {
			out = append(out, classifyMap(m, cpInstanceID)...)
		}
		return out
	}
	return nil
}

func findingRef(f DispatchedFinding, cpInstanceID string) PromptEntityRef {
	body, _ := json.Marshal(f)
	id := findingID(f.Attributes)
	return PromptEntityRef{
		CPInstanceID: cpInstanceID,
		Kind:         "finding",
		ID:           id,
		Snapshot: map[string]any{
			"id":       id,
			"severity": f.Severity,
			"title":    f.Title,
			"category": f.Category,
		},
		LiteralHash: literalHash(body),
	}
}

// findingID extracts the numeric id from a DispatchedFinding's
// Attributes (the engine stores the DB row id there). Returns 0 if
// the attribute is missing or the value isn't numeric.
func findingID(attrs map[string]any) int64 {
	if attrs == nil {
		return 0
	}
	switch v := attrs["id"].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	}
	return 0
}

// literalHash returns the first 16 hex chars of sha256(body) — short
// enough to be readable, long enough to make collision noise. Stable
// across server/client because both sides hash the exact same bytes
// the engine emits via json.Marshal.
func literalHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])[:16]
}

// lookupEnvPath resolves `triage.findings` / `trigger.host` style
// paths against the orchestrator Env. Mirrors what template.go's
// evaluator does internally; reused here so we don't have to expose
// template internals. Returns ok=false when any segment misses.
//
// Step bindings live as `map[string]any` (see bindingFromResult in
// engine.go) — `{{step.findings}}` resolves to the "findings" key
// of the step's binding map. We don't traverse arbitrary structs
// reflectively the way template.go does; the orchestrator engine
// always wraps step bindings as maps before placing them in env, so
// the map-walker below is sufficient for every real call site.
func lookupEnvPath(env Env, path string) (any, bool) {
	parts := strings.Split(path, ".")
	var cur any = map[string]any(env)
	for _, p := range parts {
		switch m := cur.(type) {
		case map[string]any:
			v, ok := m[p]
			if !ok {
				return nil, false
			}
			cur = v
		case Env:
			v, ok := m[p]
			if !ok {
				return nil, false
			}
			cur = v
		default:
			return nil, false
		}
	}
	return cur, true
}

// classifyMap inspects an unstructured map (typical of trigger
// payloads + data: bindings) and returns 0+ entity refs.
//
// Detection is conservative: each kind requires a specific set of
// fields. False positives would incorrectly tag a non-entity as a
// chip; false negatives just leave the value as a stringified JSON
// literal in the prompt (current behavior). When in doubt, skip.
//
// Order matters: finding wins ties over investigation (because
// findings are the more common case and investigation has explicit
// disambiguators).
func classifyMap(m map[string]any, cpInstanceID string) []PromptEntityRef {
	if m == nil {
		return nil
	}
	if isFindingMap(m) {
		return []PromptEntityRef{findingRefFromMap(m, cpInstanceID)}
	}
	if isInvestigationMap(m) {
		return []PromptEntityRef{investigationRefFromMap(m, cpInstanceID)}
	}
	if isIOCMap(m) {
		return []PromptEntityRef{iocRefFromMap(m, cpInstanceID)}
	}
	if isNodeMap(m) {
		return []PromptEntityRef{nodeRefFromMap(m, cpInstanceID)}
	}
	if isDaimonMap(m) {
		return []PromptEntityRef{daimonRefFromMap(m, cpInstanceID)}
	}
	if isRunMap(m) {
		return []PromptEntityRef{runRefFromMap(m, cpInstanceID)}
	}
	if isOrchestrationMap(m) {
		return []PromptEntityRef{orchestrationRefFromMap(m, cpInstanceID)}
	}
	return nil
}

func mapHas(m map[string]any, keys ...string) bool {
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			return false
		}
	}
	return true
}

func mapStr(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

func mapInt(m map[string]any, k string) int64 {
	switch v := m[k].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	}
	return 0
}

// === finding ===

func isFindingMap(m map[string]any) bool {
	return mapHas(m, "id", "severity", "title", "category")
}

func findingRefFromMap(m map[string]any, cp string) PromptEntityRef {
	id := mapInt(m, "id")
	body, _ := json.Marshal(m)
	return PromptEntityRef{
		CPInstanceID: cp,
		Kind:         "finding",
		ID:           id,
		Snapshot: map[string]any{
			"id":       id,
			"severity": mapStr(m, "severity"),
			"title":    mapStr(m, "title"),
			"category": mapStr(m, "category"),
			"status":   mapStr(m, "status"),
			"host":     mapStr(m, "host"),
		},
		LiteralHash: literalHash(body),
	}
}

// === ioc ===

func isIOCMap(m map[string]any) bool {
	if !mapHas(m, "kind", "value") {
		return false
	}
	return mapHas(m, "observation_count") ||
		mapHas(m, "severity_max") ||
		mapStr(m, "type") == "ioc"
}

func iocRefFromMap(m map[string]any, cp string) PromptEntityRef {
	kind := mapStr(m, "kind")
	value := mapStr(m, "value")
	body, _ := json.Marshal(m)
	last4 := value
	if len(last4) > 4 {
		last4 = last4[len(last4)-4:]
	}
	return PromptEntityRef{
		CPInstanceID: cp,
		Kind:         "ioc",
		IOCKind:      kind,
		IOCValue:     value,
		Snapshot: map[string]any{
			"kind":              kind,
			"value":             value,
			"last4":             last4,
			"observation_count": m["observation_count"],
			"severity_max":      m["severity_max"],
		},
		LiteralHash: literalHash(body),
	}
}

// === node ===

func isNodeMap(m map[string]any) bool {
	if !mapHas(m, "id", "hostname") {
		return false
	}
	if _, ok := m["severity"]; ok {
		return false
	}
	return true
}

func nodeRefFromMap(m map[string]any, cp string) PromptEntityRef {
	body, _ := json.Marshal(m)
	return PromptEntityRef{
		CPInstanceID: cp,
		Kind:         "node",
		ID:           mapInt(m, "id"),
		Snapshot: map[string]any{
			"id":       mapInt(m, "id"),
			"name":     mapStr(m, "name"),
			"hostname": mapStr(m, "hostname"),
			"status":   mapStr(m, "status"),
		},
		LiteralHash: literalHash(body),
	}
}

// === daimon ===

func isDaimonMap(m map[string]any) bool {
	if !mapHas(m, "name", "host") {
		return false
	}
	return mapHas(m, "agent_id") || mapHas(m, "suspended")
}

func daimonRefFromMap(m map[string]any, cp string) PromptEntityRef {
	body, _ := json.Marshal(m)
	return PromptEntityRef{
		CPInstanceID: cp,
		Kind:         "daimon",
		ID:           mapInt(m, "agent_id"),
		Snapshot: map[string]any{
			"id":        mapInt(m, "agent_id"),
			"name":      mapStr(m, "name"),
			"host":      mapStr(m, "host"),
			"suspended": m["suspended"],
		},
		LiteralHash: literalHash(body),
	}
}

// === run ===

func isRunMap(m map[string]any) bool {
	if !mapHas(m, "id", "status") {
		return false
	}
	return mapHas(m, "started_at") || mapHas(m, "agent_name")
}

func runRefFromMap(m map[string]any, cp string) PromptEntityRef {
	body, _ := json.Marshal(m)
	return PromptEntityRef{
		CPInstanceID: cp,
		Kind:         "run",
		ID:           mapInt(m, "id"),
		Snapshot: map[string]any{
			"id":         mapInt(m, "id"),
			"status":     mapStr(m, "status"),
			"started_at": mapStr(m, "started_at"),
			"ended_at":   mapStr(m, "ended_at"),
		},
		LiteralHash: literalHash(body),
	}
}

// === investigation ===

func isInvestigationMap(m map[string]any) bool {
	if !mapHas(m, "id", "title") {
		return false
	}
	if _, hasCategory := m["category"]; hasCategory {
		return false
	}
	return mapHas(m, "external_key") || (mapHas(m, "severity") && mapHas(m, "status"))
}

func investigationRefFromMap(m map[string]any, cp string) PromptEntityRef {
	body, _ := json.Marshal(m)
	return PromptEntityRef{
		CPInstanceID: cp,
		Kind:         "investigation",
		ID:           mapInt(m, "id"),
		Snapshot: map[string]any{
			"id":       mapInt(m, "id"),
			"title":    mapStr(m, "title"),
			"status":   mapStr(m, "status"),
			"severity": mapStr(m, "severity"),
		},
		LiteralHash: literalHash(body),
	}
}

// === orchestration ===

func isOrchestrationMap(m map[string]any) bool {
	return mapHas(m, "id", "name", "version")
}

func orchestrationRefFromMap(m map[string]any, cp string) PromptEntityRef {
	body, _ := json.Marshal(m)
	return PromptEntityRef{
		CPInstanceID: cp,
		Kind:         "orchestration",
		ID:           mapInt(m, "id"),
		Snapshot: map[string]any{
			"id":      mapInt(m, "id"),
			"name":    mapStr(m, "name"),
			"version": m["version"],
		},
		LiteralHash: literalHash(body),
	}
}
