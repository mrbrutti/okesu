package render

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// update is package-scoped so all golden tests share the -update flag.
// Don't redeclare it in another *_test.go file in this package.
var update = flag.Bool("update", false, "rewrite testdata/*.golden from current output")

func loadInputs(t *testing.T, name string) Inputs {
	t.Helper()
	path := filepath.Join("testdata", "inputs."+name+".json")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var raw struct {
		Mode         string
		TerraformOut map[string]any
		OperatorEnv  map[string]string
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return Inputs(raw)
}

func assertGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.WriteFile(path, got, 0644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != string(want) {
		t.Fatalf("output drift vs %s\n--- got\n%s\n--- want\n%s", path, got, want)
	}
}

func TestRenderCPYAML_Standalone(t *testing.T) {
	got, err := RenderCPYAML(loadInputs(t, "standalone"))
	if err != nil {
		t.Fatalf("RenderCPYAML: %v", err)
	}
	assertGolden(t, "cp.yaml.standalone", got)
}

func TestRenderCPYAML_Parent(t *testing.T) {
	// Federation moved to env file — cp.yaml is mode-agnostic; all modes share this golden.
	got, err := RenderCPYAML(loadInputs(t, "parent"))
	if err != nil {
		t.Fatalf("RenderCPYAML: %v", err)
	}
	assertGolden(t, "cp.yaml.standalone", got)
}

func TestRenderCPYAML_Child(t *testing.T) {
	// Federation moved to env file — cp.yaml is mode-agnostic; all modes share this golden.
	got, err := RenderCPYAML(loadInputs(t, "child"))
	if err != nil {
		t.Fatalf("RenderCPYAML: %v", err)
	}
	assertGolden(t, "cp.yaml.standalone", got)
}

func TestRenderEnvFile(t *testing.T) {
	cases := []struct {
		name string
		mode string
		env  map[string]string
	}{
		{
			name: "anthropic_only",
			mode: "standalone",
			env:  map[string]string{"ANTHROPIC_API_KEY": "sk-ant-test"},
		},
		{
			name: "with_openai",
			mode: "standalone",
			env: map[string]string{
				"ANTHROPIC_API_KEY": "sk-ant-test",
				"OPENAI_API_KEY":    "sk-openai-test",
			},
		},
		{
			name: "with_openai_and_oidc",
			mode: "standalone",
			env: map[string]string{
				"ANTHROPIC_API_KEY":  "sk-ant-test",
				"OPENAI_API_KEY":     "sk-openai-test",
				"OIDC_CLIENT_SECRET": "oidc-secret-test",
			},
		},
		{
			name: "parent_with_federation",
			mode: "parent",
			env: map[string]string{
				"ANTHROPIC_API_KEY": "sk-ant-test",
				"FEDERATION_TOKEN":  "fed-token-parent-test",
				"CP_INSTANCE_ID":    "cp-parent-001",
			},
		},
		{
			name: "child_with_full_federation",
			mode: "child",
			env: map[string]string{
				"ANTHROPIC_API_KEY":           "sk-ant-test",
				"PARENT_FEDERATION_TOKEN":     "fed-token-child-test",
				"PARENT_FEDERATION_BUCKET":    "okesu-parent-bucket",
				"PARENT_FEDERATION_ENDPOINT":  "ns.compat.objectstorage.us-ashburn-1.oraclecloud.com",
				"PARENT_FEDERATION_REGION":    "us-ashburn-1",
				"PARENT_FEDERATION_ACCESS_KEY": "PARENT-AKID-EXAMPLE",
				"PARENT_FEDERATION_SECRET_KEY": "parent-secret-key-test",
				"CP_INSTANCE_ID":              "cp-child-001",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := loadInputs(t, "standalone")
			in.Mode = tc.mode
			in.OperatorEnv = tc.env
			got, err := RenderEnvFile(in)
			if err != nil {
				t.Fatalf("RenderEnvFile: %v", err)
			}
			assertGolden(t, "okesu-cp.env."+tc.name, got)
		})
	}
}

func TestRenderEnvFile_RequiresAnthropic(t *testing.T) {
	in := Inputs{Mode: "standalone", TerraformOut: map[string]any{}, OperatorEnv: map[string]string{}}
	_, err := RenderEnvFile(in)
	if err == nil {
		t.Fatalf("RenderEnvFile with no ANTHROPIC_API_KEY should error")
	}
}

func TestRenderEnvFile_RequiresFederationTokenForParent(t *testing.T) {
	in := Inputs{
		Mode:         "parent",
		TerraformOut: map[string]any{},
		OperatorEnv:  map[string]string{"ANTHROPIC_API_KEY": "sk-ant-test"},
	}
	_, err := RenderEnvFile(in)
	if err == nil {
		t.Fatalf("RenderEnvFile with mode=parent and no FEDERATION_TOKEN should error")
	}
	if !strings.Contains(err.Error(), "FEDERATION_TOKEN") {
		t.Fatalf("expected error to mention FEDERATION_TOKEN, got: %v", err)
	}
}

func TestRenderEnvFile_RequiresAllFederationFieldsForChild(t *testing.T) {
	fullEnv := map[string]string{
		"ANTHROPIC_API_KEY":           "sk-ant-test",
		"PARENT_FEDERATION_TOKEN":     "fed-token-child-test",
		"PARENT_FEDERATION_BUCKET":    "okesu-parent-bucket",
		"PARENT_FEDERATION_ENDPOINT":  "ns.compat.objectstorage.us-ashburn-1.oraclecloud.com",
		"PARENT_FEDERATION_REGION":    "us-ashburn-1",
		"PARENT_FEDERATION_ACCESS_KEY": "PARENT-AKID-EXAMPLE",
		"PARENT_FEDERATION_SECRET_KEY": "parent-secret-key-test",
	}
	requiredFields := []string{
		"PARENT_FEDERATION_TOKEN",
		"PARENT_FEDERATION_BUCKET",
		"PARENT_FEDERATION_ENDPOINT",
		"PARENT_FEDERATION_REGION",
		"PARENT_FEDERATION_ACCESS_KEY",
		"PARENT_FEDERATION_SECRET_KEY",
	}
	for _, missing := range requiredFields {
		t.Run("missing_"+missing, func(t *testing.T) {
			env := make(map[string]string, len(fullEnv))
			for k, v := range fullEnv {
				env[k] = v
			}
			delete(env, missing)
			in := Inputs{
				Mode:         "child",
				TerraformOut: map[string]any{},
				OperatorEnv:  env,
			}
			_, err := RenderEnvFile(in)
			if err == nil {
				t.Fatalf("RenderEnvFile with mode=child missing %s should error", missing)
			}
			if !strings.Contains(err.Error(), missing) {
				t.Fatalf("expected error to mention %s, got: %v", missing, err)
			}
		})
	}
}

func TestValidateCPYAML_AllModes(t *testing.T) {
	for _, mode := range []string{"standalone", "parent", "child"} {
		t.Run(mode, func(t *testing.T) {
			body, err := RenderCPYAML(loadInputs(t, mode))
			if err != nil {
				t.Fatalf("RenderCPYAML: %v", err)
			}
			if err := ValidateCPYAML(body); err != nil {
				t.Fatalf("ValidateCPYAML(%s): %v\n--- yaml\n%s", mode, err, body)
			}
		})
	}
}
