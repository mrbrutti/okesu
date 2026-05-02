package cpprovision

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type fakeBucketProvisioner struct{ cloud string }

func (f *fakeBucketProvisioner) Cloud() string { return f.cloud }
func (f *fakeBucketProvisioner) ListBuckets(_ context.Context, _ []byte, _, _ string) ([]BucketInfo, error) {
	return nil, nil
}
func (f *fakeBucketProvisioner) EnsureBucket(_ context.Context, _ []byte, _, _, _ string) (*BucketInfo, error) {
	return nil, nil
}
func (f *fakeBucketProvisioner) BucketAccessKeys(_ context.Context, _ []byte) (string, string, error) {
	return "", "", nil
}

func TestBucketRegistry_Register_Get_Clouds(t *testing.T) {
	r := NewBucketRegistry()
	r.Register(&fakeBucketProvisioner{cloud: "aws"})
	r.Register(&fakeBucketProvisioner{cloud: "oci"})

	if _, err := r.Get("aws"); err != nil {
		t.Errorf("aws should resolve: %v", err)
	}
	if _, err := r.Get("oci"); err != nil {
		t.Errorf("oci should resolve: %v", err)
	}
	if _, err := r.Get("missing"); !errors.Is(err, ErrNoBucketProvisioner) {
		t.Errorf("expected ErrNoBucketProvisioner; got %v", err)
	}

	clouds := r.Clouds()
	if !reflect.DeepEqual(clouds, []string{"aws", "oci"}) {
		t.Errorf("Clouds = %v, want [aws oci] sorted", clouds)
	}
}

func TestBucketRegistry_DuplicateRegistrationPanics(t *testing.T) {
	r := NewBucketRegistry()
	r.Register(&fakeBucketProvisioner{cloud: "aws"})

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate registration")
		}
	}()
	r.Register(&fakeBucketProvisioner{cloud: "aws"})
}
