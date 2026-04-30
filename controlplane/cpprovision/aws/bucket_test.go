package aws

import (
	"context"
	"testing"
)

// BucketAccessKeys is the only path we can fully unit-test without
// wiring an SDK mock. The other methods (ListBuckets, EnsureBucket)
// hit S3 and are validated by handler-level tests + lab smoke.
func TestBucketProvisioner_AccessKeys_StaticCreds(t *testing.T) {
	p := NewBucketProvisioner()
	creds := []byte(`{"access_key_id":"AKIA...","secret_access_key":"secret","region":"us-east-1"}`)
	access, secret, err := p.BucketAccessKeys(context.Background(), creds)
	if err != nil {
		t.Fatalf("BucketAccessKeys: %v", err)
	}
	if access != "AKIA..." || secret != "secret" {
		t.Errorf("got (%q, %q); want (AKIA..., secret)", access, secret)
	}
}

func TestBucketProvisioner_AccessKeys_RoleOnlyRejected(t *testing.T) {
	p := NewBucketProvisioner()
	creds := []byte(`{"role_arn":"arn:aws:iam::123:role/X","region":"us-east-1"}`)
	if _, _, err := p.BucketAccessKeys(context.Background(), creds); err == nil {
		t.Error("expected error for role-only credential")
	}
}

func TestBucketProvisioner_AccessKeys_BadJSON(t *testing.T) {
	p := NewBucketProvisioner()
	if _, _, err := p.BucketAccessKeys(context.Background(), []byte(`not json`)); err == nil {
		t.Error("expected error for bad JSON")
	}
}

func TestBucketProvisioner_Cloud(t *testing.T) {
	p := NewBucketProvisioner()
	if got := p.Cloud(); got != "aws" {
		t.Errorf("Cloud() = %q, want aws", got)
	}
}
