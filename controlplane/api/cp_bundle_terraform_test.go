package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// renderTerraformForTest is the test harness around writeTerraformBundle.
// We need a real on-disk image tar so writeComposeBundle has something
// to read; the test fixtures are tiny (a 4-byte tar) and we never
// untar them — they just need to exist.
func renderTerraformForTest(t *testing.T, cloud string) ([]byte, *BundleCache, int64) {
	t.Helper()
	tmp := t.TempDir()
	imgPath := filepath.Join(tmp, "okesu-cp-image.tar")
	if err := os.WriteFile(imgPath, []byte("FAKE"), 0o644); err != nil {
		t.Fatalf("write fixture image: %v", err)
	}
	cache := NewBundleCache()
	const tokenID int64 = 42
	b := bundleVars{
		DisplayName:    "us-east-prod",
		Region:         "us-east-1",
		ParentURL:      "https://parent.example:8443",
		BootstrapToken: "okesu_cpboot_testtokenplaintext",
		AdminPassword:  "adminpw",
		WebhookSecret:  "ws",
		SessionKey:     "sk",
		ChildPort:      8443,
		MgmtPort:       8444,
		Version:        "test",
		IssuedAt:       "2026-04-29T00:00:00Z",
	}
	cfg := CPBundleConfig{
		LinuxImageTarPath: imgPath,
		ParentMgmtURL:     "https://parent.example:8443",
		Version:           "test",
	}
	var buf bytes.Buffer
	if err := writeTerraformBundle(&buf, b, cloud, cfg, cache, "https://parent.example:8443", tokenID); err != nil {
		t.Fatalf("writeTerraformBundle(%s): %v", cloud, err)
	}
	return buf.Bytes(), cache, tokenID
}

func tarFiles(t *testing.T, gzBytes []byte) map[string]string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(gzBytes))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	out := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("tar read %s: %v", hdr.Name, err)
		}
		out[hdr.Name] = string(body)
	}
	return out
}

func TestWriteTerraformBundleOCI(t *testing.T) {
	gzBytes, cache, tokenID := renderTerraformForTest(t, "oci")
	files := tarFiles(t, gzBytes)
	root := "okesu-cp-us-east-prod"
	for _, name := range []string{
		"versions.tf", "variables.tf", "main.tf", "outputs.tf",
		"cloud-init.sh", "README.md", "terraform.tfvars.example",
	} {
		full := root + "/" + name
		if _, ok := files[full]; !ok {
			t.Errorf("missing file in tarball: %s", full)
		}
	}
	main := files[root+"/main.tf"]
	if !strings.Contains(main, `oci_core_instance`) {
		t.Errorf("OCI main.tf missing oci_core_instance: %s", main)
	}
	if !strings.Contains(main, `okesu-cp-us-east-prod`) {
		t.Errorf("OCI main.tf missing display_name slug: %s", main)
	}
	cloudInit := files[root+"/cloud-init.sh"]
	if !strings.Contains(cloudInit, "okesu_cpboot_testtokenplaintext") {
		t.Errorf("cloud-init missing bootstrap token: %s", cloudInit)
	}
	if !strings.Contains(cloudInit, "https://parent.example:8443/api/federation/cp-bundle/download") {
		t.Errorf("cloud-init missing bundle URL: %s", cloudInit)
	}
	if _, _, ok := cache.Get(tokenID); !ok {
		t.Error("bundle cache was not populated for tokenID")
	}
}

func TestWriteTerraformBundleAWS(t *testing.T) {
	gzBytes, _, _ := renderTerraformForTest(t, "aws")
	files := tarFiles(t, gzBytes)
	root := "okesu-cp-us-east-prod"
	main := files[root+"/main.tf"]
	if !strings.Contains(main, `aws_instance`) {
		t.Errorf("AWS main.tf missing aws_instance: %s", main)
	}
	if !strings.Contains(main, `vpc_security_group_ids`) {
		t.Errorf("AWS main.tf missing vpc_security_group_ids: %s", main)
	}
	vars := files[root+"/variables.tf"]
	if !strings.Contains(vars, `variable "ami_id"`) {
		t.Errorf("AWS variables.tf missing ami_id: %s", vars)
	}
	example := files[root+"/terraform.tfvars.example"]
	if !strings.Contains(example, `region              = "us-east-1"`) {
		t.Errorf("AWS tfvars example missing region default: %s", example)
	}
}

func TestWriteTerraformBundleUnknownCloud(t *testing.T) {
	tmp := t.TempDir()
	imgPath := filepath.Join(tmp, "okesu-cp-image.tar")
	_ = os.WriteFile(imgPath, []byte("FAKE"), 0o644)
	cfg := CPBundleConfig{LinuxImageTarPath: imgPath}
	cache := NewBundleCache()
	b := bundleVars{DisplayName: "x", Region: "us", IssuedAt: "now"}
	var buf bytes.Buffer
	// Unknown clouds get rendered with placeholder content rather than
	// erroring — the public handler validates cloud up-front, so this
	// path is only reachable from a programmer mistake. We want it to
	// not panic and the marker text to make the mistake obvious.
	if err := writeTerraformBundle(&buf, b, "gcp", cfg, cache, "https://parent.example", 1); err != nil {
		t.Fatalf("writeTerraformBundle(gcp) errored: %v", err)
	}
	files := tarFiles(t, buf.Bytes())
	if !strings.Contains(files["okesu-cp-x/main.tf"], "unsupported cloud") {
		t.Errorf("expected unsupported-cloud marker in main.tf, got %q", files["okesu-cp-x/main.tf"])
	}
}
