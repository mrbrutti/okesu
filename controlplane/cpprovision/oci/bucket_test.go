package oci

import "testing"

func TestOciS3Endpoint(t *testing.T) {
	got := ociS3Endpoint("axyz1234", "us-ashburn-1")
	want := "https://axyz1234.compat.objectstorage.us-ashburn-1.oraclecloud.com"
	if got != want {
		t.Errorf("ociS3Endpoint = %q, want %q", got, want)
	}
}

func TestBucketProvisioner_Cloud(t *testing.T) {
	p := NewBucketProvisioner()
	if got := p.Cloud(); got != "oci" {
		t.Errorf("Cloud() = %q, want oci", got)
	}
}
