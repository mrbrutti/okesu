package render

import "testing"

func TestPackageCompiles(t *testing.T) {
	in := Inputs{Mode: "standalone", TerraformOut: map[string]any{}, OperatorEnv: map[string]string{}}
	_ = in
}
