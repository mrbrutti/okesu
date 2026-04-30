package render

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

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
