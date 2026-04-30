package render

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
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
	got, err := RenderCPYAML(loadInputs(t, "parent"))
	if err != nil {
		t.Fatalf("RenderCPYAML: %v", err)
	}
	assertGolden(t, "cp.yaml.parent", got)
}

func TestRenderCPYAML_Child(t *testing.T) {
	got, err := RenderCPYAML(loadInputs(t, "child"))
	if err != nil {
		t.Fatalf("RenderCPYAML: %v", err)
	}
	assertGolden(t, "cp.yaml.child", got)
}

func TestRenderEnvFile(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
	}{
		{
			name: "anthropic_only",
			env:  map[string]string{"ANTHROPIC_API_KEY": "sk-ant-test"},
		},
		{
			name: "with_openai",
			env: map[string]string{
				"ANTHROPIC_API_KEY": "sk-ant-test",
				"OPENAI_API_KEY":    "sk-openai-test",
			},
		},
		{
			name: "with_openai_and_oidc",
			env: map[string]string{
				"ANTHROPIC_API_KEY":  "sk-ant-test",
				"OPENAI_API_KEY":     "sk-openai-test",
				"OIDC_CLIENT_SECRET": "oidc-secret-test",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := loadInputs(t, "standalone")
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
