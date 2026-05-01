package orchestrator

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildPromptEntities_NoPlaceholders(t *testing.T) {
	got, err := BuildPromptEntities("plain prose, no template", Env{}, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestBuildPromptEntities_UnrecognisedType(t *testing.T) {
	env := Env{"trigger": map[string]any{"host": "edr-1"}}
	got, err := BuildPromptEntities("host={{trigger.host}}", env, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Errorf("plain string should produce no refs, got %+v", got)
	}
}

func TestBuildPromptEntities_SingleFinding(t *testing.T) {
	f := DispatchedFinding{
		Severity:   "HIGH",
		Title:      "Suspicious cron job",
		Category:   "process",
		Attributes: map[string]any{"id": int64(42)},
	}
	env := Env{"trigger": map[string]any{"finding": f}}
	got, err := BuildPromptEntities(
		"investigate: {{trigger.finding}}",
		env,
		"",
	)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil || len(got.Refs) != 1 {
		t.Fatalf("expected 1 ref, got %+v", got)
	}
	r := got.Refs[0]
	if r.Kind != "finding" || r.ID != 42 {
		t.Errorf("ref = %+v", r)
	}
	if r.Snapshot["severity"] != "HIGH" {
		t.Errorf("snapshot.severity = %v, want HIGH", r.Snapshot["severity"])
	}
	if len(r.LiteralHash) != 16 {
		t.Errorf("literal_hash = %q, want 16 hex chars", r.LiteralHash)
	}
}

func TestBuildPromptEntities_FindingArray(t *testing.T) {
	findings := []DispatchedFinding{
		{Severity: "HIGH", Title: "A", Category: "x", Attributes: map[string]any{"id": int64(1)}},
		{Severity: "LOW", Title: "B", Category: "y", Attributes: map[string]any{"id": int64(2)}},
	}
	// Step bindings reach the env as maps (see bindingFromResult in
	// engine.go), so simulate that shape here.
	env := Env{"triage": map[string]any{"findings": findings}}
	got, err := BuildPromptEntities(
		"summary: {{triage.findings}}",
		env,
		"cp-child-1",
	)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil || len(got.Refs) != 2 {
		t.Fatalf("expected 2 refs, got %+v", got)
	}
	for _, r := range got.Refs {
		if r.CPInstanceID != "cp-child-1" {
			t.Errorf("CPInstanceID = %q, want cp-child-1", r.CPInstanceID)
		}
	}
}

func TestBuildPromptEntities_DedupByHash(t *testing.T) {
	f := DispatchedFinding{
		Severity:   "HIGH",
		Title:      "Same finding",
		Category:   "x",
		Attributes: map[string]any{"id": int64(7)},
	}
	env := Env{
		"a": map[string]any{"finding": f},
		"b": map[string]any{"finding": f},
	}
	got, err := BuildPromptEntities(
		"first: {{a.finding}} ... second: {{b.finding}}",
		env,
		"",
	)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil {
		t.Fatal("expected refs")
	}
	if len(got.Refs) != 1 {
		t.Fatalf("expected 1 ref (dedup), got %d", len(got.Refs))
	}
}

func TestBuildPromptEntities_HashStability(t *testing.T) {
	f := DispatchedFinding{
		Severity: "HIGH",
		Title:    "Stable",
		Category: "x",
	}
	env := Env{"trigger": map[string]any{"finding": f}}
	r1, _ := BuildPromptEntities("{{trigger.finding}}", env, "")
	r2, _ := BuildPromptEntities("{{trigger.finding}}", env, "")
	if r1 == nil || r2 == nil {
		t.Fatal("expected refs both runs")
	}
	if r1.Refs[0].LiteralHash != r2.Refs[0].LiteralHash {
		t.Errorf("hashes differ across runs: %q vs %q", r1.Refs[0].LiteralHash, r2.Refs[0].LiteralHash)
	}
}

func TestPromptEntities_RoundTripJSON(t *testing.T) {
	pe := PromptEntities{
		Refs: []PromptEntityRef{{
			Kind:        "finding",
			ID:          42,
			Snapshot:    map[string]any{"severity": "HIGH"},
			LiteralHash: "abc123def4567890",
		}},
	}
	b, err := json.Marshal(pe)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"kind":"finding"`) {
		t.Errorf("missing kind in JSON: %s", b)
	}
	var out PromptEntities
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Refs[0].LiteralHash != "abc123def4567890" {
		t.Errorf("hash lost on roundtrip: %+v", out.Refs[0])
	}
}

func TestClassifyMap_IOC(t *testing.T) {
	m := map[string]any{
		"kind":              "sha256",
		"value":             "deadbeef" + "0000000000000000000000000000000000000000000000000000000000",
		"observation_count": float64(3),
	}
	refs := classifyMap(m, "cp-x")
	if len(refs) != 1 || refs[0].Kind != "ioc" {
		t.Fatalf("expected ioc ref, got %+v", refs)
	}
	if refs[0].IOCKind != "sha256" {
		t.Errorf("ioc_kind = %q", refs[0].IOCKind)
	}
	if refs[0].Snapshot["last4"] != "0000" {
		t.Errorf("last4 = %v", refs[0].Snapshot["last4"])
	}
}

func TestClassifyMap_Node(t *testing.T) {
	m := map[string]any{
		"id":       float64(7),
		"name":     "edr-1",
		"hostname": "edr-1.lab",
		"status":   "online",
	}
	refs := classifyMap(m, "")
	if len(refs) != 1 || refs[0].Kind != "node" || refs[0].ID != 7 {
		t.Fatalf("expected node ref id=7, got %+v", refs)
	}
}

func TestClassifyMap_Daimon(t *testing.T) {
	m := map[string]any{
		"name":      "edr-agent",
		"host":      "edr-1",
		"agent_id":  float64(11),
		"suspended": false,
	}
	refs := classifyMap(m, "")
	if len(refs) != 1 || refs[0].Kind != "daimon" || refs[0].ID != 11 {
		t.Fatalf("expected daimon ref, got %+v", refs)
	}
}

func TestClassifyMap_Run(t *testing.T) {
	m := map[string]any{
		"id":         float64(99),
		"status":     "completed",
		"started_at": "2026-04-30T10:00:00Z",
		"agent_name": "edr-agent",
	}
	refs := classifyMap(m, "")
	if len(refs) != 1 || refs[0].Kind != "run" || refs[0].ID != 99 {
		t.Fatalf("expected run ref, got %+v", refs)
	}
}

func TestClassifyMap_Investigation(t *testing.T) {
	m := map[string]any{
		"id":           float64(3),
		"title":        "ransomware Q3",
		"severity":     "HIGH",
		"status":       "open",
		"external_key": "INC-123",
	}
	refs := classifyMap(m, "")
	if len(refs) != 1 || refs[0].Kind != "investigation" || refs[0].ID != 3 {
		t.Fatalf("expected investigation ref, got %+v", refs)
	}
}

func TestClassifyMap_Orchestration(t *testing.T) {
	m := map[string]any{
		"id":      float64(5),
		"name":    "triage-then-quarantine",
		"version": float64(2),
	}
	refs := classifyMap(m, "")
	if len(refs) != 1 || refs[0].Kind != "orchestration" {
		t.Fatalf("expected orchestration ref, got %+v", refs)
	}
}

func TestClassifyMap_NotEntity(t *testing.T) {
	m := map[string]any{"foo": "bar", "baz": float64(1)}
	if refs := classifyMap(m, ""); refs != nil {
		t.Errorf("expected nil refs for non-entity map, got %+v", refs)
	}
}

func TestClassifyMap_FindingNotInvestigation(t *testing.T) {
	// Finding has both severity+status — make sure we don't
	// misclassify as investigation.
	m := map[string]any{
		"id":       float64(1),
		"title":    "x",
		"category": "process",
		"severity": "LOW",
		"status":   "open",
	}
	refs := classifyMap(m, "")
	if len(refs) != 1 || refs[0].Kind != "finding" {
		t.Fatalf("expected finding (not investigation), got %+v", refs)
	}
}

func TestBuildPromptEntities_FindingMapInTrigger(t *testing.T) {
	env := Env{"trigger": map[string]any{
		"finding": map[string]any{
			"id":       float64(42),
			"severity": "HIGH",
			"title":    "x",
			"category": "y",
		},
	}}
	got, _ := BuildPromptEntities("look: {{trigger.finding}}", env, "")
	if got == nil || len(got.Refs) != 1 || got.Refs[0].Kind != "finding" {
		t.Fatalf("expected 1 finding ref, got %+v", got)
	}
}
