package agent

import (
	"encoding/json"
	"reflect"
	"testing"
)

// findDef returns the ToolDef named name, or fails the test.
func findDef(t *testing.T, defs []ToolDef, name string) ToolDef {
	t.Helper()
	for _, d := range defs {
		if d.Name == name {
			return d
		}
	}
	t.Fatalf("tool %q not present in %d defs", name, len(defs))
	return ToolDef{}
}

// props returns the "properties" sub-map of a ToolDef's JSON Schema.
func props(t *testing.T, d ToolDef) map[string]interface{} {
	t.Helper()
	p, ok := d.Parameters["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("tool %q has no properties map (got %T)", d.Name, d.Parameters["properties"])
	}
	return p
}

func contains(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}

func TestWithReasonParamInjectsRequiredReason(t *testing.T) {
	defs := WithReasonParam(ActiveTools(nil), map[string]bool{"bash": true})

	bash := findDef(t, defs, "bash")
	reason, ok := props(t, bash)["reason"]
	if !ok {
		t.Fatal("bash is missing the injected `reason` property")
	}
	rm, ok := reason.(map[string]interface{})
	if !ok {
		t.Fatalf("injected reason property is %T, want map[string]interface{}", reason)
	}
	if rm["type"] != "string" {
		t.Errorf("reason type = %v, want \"string\"", rm["type"])
	}
	if desc, _ := rm["description"].(string); desc == "" {
		t.Error("injected reason property has no description for the model to read")
	}
	if !contains(bash.Required, "reason") {
		t.Errorf("bash.Required = %v, want it to include \"reason\"", bash.Required)
	}
	if !contains(bash.Required, "command") {
		t.Errorf("bash.Required = %v, want the pre-existing \"command\" preserved", bash.Required)
	}
}

func TestWithReasonParamLeavesOtherToolsAlone(t *testing.T) {
	defs := WithReasonParam(ActiveTools(nil), map[string]bool{"bash": true})

	rf := findDef(t, defs, "read_file")
	if _, ok := props(t, rf)["reason"]; ok {
		t.Error("read_file gained a `reason` property but was not in the required set")
	}
	if contains(rf.Required, "reason") {
		t.Errorf("read_file.Required = %v, want no \"reason\"", rf.Required)
	}
}

func TestWithReasonParamEmptySetIsANoOp(t *testing.T) {
	before := ActiveTools(nil)
	after := WithReasonParam(before, nil)

	if len(after) != len(before) {
		t.Fatalf("len(after) = %d, want %d", len(after), len(before))
	}
	for i := range after {
		if _, ok := props(t, after[i])["reason"]; ok {
			t.Errorf("tool %q gained a reason property with an empty required set", after[i].Name)
		}
	}
}

// TestWithReasonParamDoesNotMutateGlobalTools is the regression guard for the
// aliasing hazard: ToolDef.Parameters is a map on the package-level Tools var
// and ActiveTools copies the structs but shares that map by reference. If
// WithReasonParam injects in place, one agent's policy leaks into every other
// agent in the process.
func TestWithReasonParamDoesNotMutateGlobalTools(t *testing.T) {
	snapshot, err := json.Marshal(Tools)
	if err != nil {
		t.Fatalf("snapshot Tools: %v", err)
	}

	_ = WithReasonParam(ActiveTools(nil), map[string]bool{
		"bash": true, "read_file": true, "write_file": true,
		"list_files": true, "search": true,
	})

	after, err := json.Marshal(Tools)
	if err != nil {
		t.Fatalf("re-marshal Tools: %v", err)
	}
	if string(snapshot) != string(after) {
		t.Errorf("WithReasonParam mutated the package-level Tools var\nbefore: %s\nafter:  %s", snapshot, after)
	}
}

// TestWithReasonParamIsIndependentPerCall proves two concurrent agents with
// different policies do not observe each other's injected schema.
func TestWithReasonParamIsIndependentPerCall(t *testing.T) {
	strict := WithReasonParam(ActiveTools(nil), map[string]bool{"bash": true})
	relaxed := WithReasonParam(ActiveTools(nil), nil)

	if _, ok := props(t, findDef(t, relaxed, "bash"))["reason"]; ok {
		t.Error("relaxed policy sees the strict policy's injected reason property")
	}
	if _, ok := props(t, findDef(t, strict, "bash"))["reason"]; !ok {
		t.Error("strict policy lost its injected reason property")
	}
}

// TestActiveToolsForAppliesPolicy covers the helper both provider runners
// call: filter by the agent's tool list, then apply require_reason.
func TestActiveToolsForAppliesPolicy(t *testing.T) {
	policy := &RBACPolicy{Allow: []RBACRule{
		{Tool: "bash", RequireReason: true},
		{Tool: "read_file"},
	}}

	defs := activeToolsFor([]string{"bash", "read_file"}, policy)

	if len(defs) != 2 {
		t.Fatalf("len(defs) = %d, want 2 (AllowedTools filter applied)", len(defs))
	}
	if !contains(findDef(t, defs, "bash").Required, "reason") {
		t.Error("bash did not gain a required reason under a require_reason policy")
	}
	if contains(findDef(t, defs, "read_file").Required, "reason") {
		t.Error("read_file gained a required reason it was not policed for")
	}
}

func TestActiveToolsForNilPolicyIsUnchanged(t *testing.T) {
	defs := activeToolsFor(nil, nil)

	for _, d := range defs {
		if contains(d.Required, "reason") {
			t.Errorf("tool %q requires a reason under a nil policy", d.Name)
		}
	}
}

// TestBuildBetaToolsCarriesReasonSchema proves the Claude runner's tool
// params actually reach the API with reason marked required — the wiring,
// not just the helper.
func TestBuildBetaToolsCarriesReasonSchema(t *testing.T) {
	policy := &RBACPolicy{Allow: []RBACRule{{Tool: "bash", RequireReason: true}}}

	tools, err := buildBetaTools(activeToolsFor([]string{"bash"}, policy), policy, false, "t", "h", 0, nil)
	if err != nil {
		t.Fatalf("buildBetaTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("len(tools) = %d, want 1", len(tools))
	}
	if !contains(tools[0].InputSchema().Required, "reason") {
		t.Errorf("BetaTool InputSchema.Required = %v, want it to include \"reason\"", tools[0].InputSchema().Required)
	}
}

// TestBuildResponsesToolsCarriesReasonSchema is the same assertion for the
// OpenAI runner, which builds its schema through a separate path.
func TestBuildResponsesToolsCarriesReasonSchema(t *testing.T) {
	policy := &RBACPolicy{Allow: []RBACRule{{Tool: "bash", RequireReason: true}}}

	defs := activeToolsFor([]string{"bash"}, policy)
	schema := buildResponsesSchema(defs[0])

	req, ok := schema["required"].([]string)
	if !ok {
		t.Fatalf("schema[\"required\"] is %T, want []string", schema["required"])
	}
	if !contains(req, "reason") {
		t.Errorf("responses schema required = %v, want it to include \"reason\"", req)
	}
	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("schema[\"properties\"] is %T", schema["properties"])
	}
	if _, ok := props["reason"]; !ok {
		t.Error("responses schema properties missing `reason`")
	}
}

// TestWithReasonParamPreservesExistingSchema guards against the injection
// dropping sibling properties such as bash's timeout_seconds.
func TestWithReasonParamPreservesExistingSchema(t *testing.T) {
	orig := props(t, findDef(t, ActiveTools(nil), "bash"))
	got := props(t, findDef(t, WithReasonParam(ActiveTools(nil), map[string]bool{"bash": true}), "bash"))

	for k, v := range orig {
		gv, ok := got[k]
		if !ok {
			t.Errorf("injection dropped property %q", k)
			continue
		}
		if !reflect.DeepEqual(v, gv) {
			t.Errorf("property %q changed: %v -> %v", k, v, gv)
		}
	}
}
