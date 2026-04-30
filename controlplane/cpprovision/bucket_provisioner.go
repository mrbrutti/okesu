// Bucket provisioner — sibling of Provisioner (which handles VMs).
//
// A BucketProvisioner can list and create object-storage buckets in
// the operator's cloud account using the credentials we already have
// in cloud_credentials. Implementations live alongside Provisioner
// implementations (cpprovision/aws, /oci, /minio).

package cpprovision

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// BucketProvisioner is implemented per cloud kind that supports
// S3-compatible object storage.
type BucketProvisioner interface {
	// Cloud is the lowercase cloud kind ("aws" | "oci" | "minio").
	// Must match cloud_credentials.cloud values.
	Cloud() string

	// ListBuckets returns every bucket visible to the credential.
	// Region narrows the listing for clouds that scope buckets per
	// region (AWS, OCI). MinIO ignores region.
	ListBuckets(ctx context.Context, creds []byte, region string) ([]BucketInfo, error)

	// EnsureBucket creates a bucket with the given name + region,
	// or returns the existing bucket if one already exists with the
	// same name in this credential's account. Idempotent.
	EnsureBucket(ctx context.Context, creds []byte, name, region string) (*BucketInfo, error)

	// BucketAccessKeys returns S3-compatible keys scoped to the
	// credential. AWS / MinIO return the credential's own keys
	// verbatim. OCI auto-creates Customer Secret Keys via IAM and
	// returns those. Errors are surfaced verbatim to the operator.
	BucketAccessKeys(ctx context.Context, creds []byte) (accessKey, secretKey string, err error)
}

// BucketInfo is the cross-cloud projection of a bucket. Endpoint is
// the S3-compatible URL hosts will write to.
type BucketInfo struct {
	Name     string `json:"name"`
	Region   string `json:"region"`
	Endpoint string `json:"endpoint"`
}

// ErrNoBucketProvisioner is returned by BucketRegistry.Get when the
// requested cloud has no implementation registered.
var ErrNoBucketProvisioner = errors.New("no bucket provisioner registered")

// BucketRegistry tracks which clouds have a BucketProvisioner installed.
type BucketRegistry struct {
	mu  sync.RWMutex
	all map[string]BucketProvisioner
}

func NewBucketRegistry() *BucketRegistry {
	return &BucketRegistry{all: map[string]BucketProvisioner{}}
}

func (r *BucketRegistry) Register(p BucketProvisioner) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cloud := p.Cloud()
	if _, dup := r.all[cloud]; dup {
		panic(fmt.Sprintf("cpprovision: duplicate bucket provisioner for cloud %q", cloud))
	}
	r.all[cloud] = p
}

func (r *BucketRegistry) Get(cloud string) (BucketProvisioner, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.all[cloud]
	if !ok {
		return nil, fmt.Errorf("%w (cloud %q)", ErrNoBucketProvisioner, cloud)
	}
	return p, nil
}

// Clouds returns the sorted list of registered cloud kinds. Used by
// the API to surface which providers support bucket provisioning.
func (r *BucketRegistry) Clouds() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.all))
	for k := range r.all {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
