package minio

import (
	"context"
	"testing"
)

func TestBucketProvisioner_Cloud(t *testing.T) {
	p := NewBucketProvisioner()
	if got := p.Cloud(); got != "minio" {
		t.Errorf("Cloud() = %q, want minio", got)
	}
}

func TestBucketProvisioner_AccessKeys(t *testing.T) {
	p := NewBucketProvisioner()
	creds := []byte(`{"endpoint":"https://minio.example","access_key_id":"AK","secret_access_key":"SK"}`)
	access, secret, err := p.BucketAccessKeys(context.Background(), creds)
	if err != nil {
		t.Fatalf("BucketAccessKeys: %v", err)
	}
	if access != "AK" || secret != "SK" {
		t.Errorf("got (%q, %q); want (AK, SK)", access, secret)
	}
}

func TestBucketProvisioner_AccessKeys_MissingEndpoint(t *testing.T) {
	p := NewBucketProvisioner()
	creds := []byte(`{"access_key_id":"AK","secret_access_key":"SK"}`)
	if _, _, err := p.BucketAccessKeys(context.Background(), creds); err == nil {
		t.Error("expected error for missing endpoint")
	}
}

func TestBucketProvisioner_AccessKeys_MissingKeys(t *testing.T) {
	p := NewBucketProvisioner()
	creds := []byte(`{"endpoint":"https://minio.example"}`)
	if _, _, err := p.BucketAccessKeys(context.Background(), creds); err == nil {
		t.Error("expected error for missing access/secret keys")
	}
}

func TestBucketProvisioner_AccessKeys_BadJSON(t *testing.T) {
	p := NewBucketProvisioner()
	if _, _, err := p.BucketAccessKeys(context.Background(), []byte(`not json`)); err == nil {
		t.Error("expected error for bad JSON")
	}
}
