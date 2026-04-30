# Bucket Provisioning in Settings — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the broken `Settings → Cloud → Object storage buckets → Add bucket` link (currently navigates to `/nodes`) with a dedicated wizard that uses configured cloud credentials + the cloud SDK to discover existing or create new buckets, plus add safe edit + delete actions.

**Architecture:** New `BucketProvisioner` interface alongside the existing `Provisioner` (which handles VMs). Three implementations: AWS (S3 SDK), OCI (ObjectStorage + IAM Customer Secret Keys), MinIO (S3 SDK with custom endpoint). New HTTP endpoints for cloud-provider listing, bucket discovery, and provisioning that write `transport_configs` rows. Existing `DELETE /api/transport-configs/{id}` gets an in-use guard; new `PATCH` adds safe partial-update semantics. New `AddBucketWizard` frontend component with two paths: configured-provider and manual-entry. Edit/delete row actions on the existing Settings list.

**Tech Stack:**
- Go (`controlplane/cpprovision/`, `controlplane/api/`, `controlplane/db/`)
- AWS SDK v2 (`github.com/aws/aws-sdk-go-v2/service/s3` — new dependency)
- OCI Go SDK v65 (`github.com/oracle/oci-go-sdk/v65/objectstorage`, `/identity` — new packages from existing v65 module)
- React + TypeScript + Tailwind (light-theme platform tokens)
- chi router; existing `Provisioner` registry pattern

---

## Discovery: existing state vs spec

The spec was written before reading the existing endpoints. Adjustments:

- `PUT /api/transport-configs/{id}` and `DELETE /api/transport-configs/{id}` already exist (`controlplane/api/transport_configs.go:183, 241`). PUT overwrites identity fields; DELETE has no in-use guard. The spec called them out as new endpoints.
- **Plan:** keep `PUT` untouched for backwards compat (frontend never used it; external API consumers might). Add a new `PATCH` route with safe partial-update semantics. Tighten the existing `DELETE` in place to add the in-use guard.

FK shapes already in place:
- `nodes.transport_config_id` — REFERENCES (default RESTRICT)
- `enrollment_packages.transport_config_id` — NOT NULL REFERENCES (RESTRICT)
- `federation_peers.transport_config_id` — REFERENCES ON DELETE SET NULL
- `cp_provisions.transport_config_id` — REFERENCES ON DELETE SET NULL

The DELETE pre-flight check has to look at `nodes` and `enrollment_packages` (which would FK-error today) — the SET-NULL ones are not blockers but should still be surfaced so the operator knows what gets disconnected.

---

## File Structure

**Backend create:**
- `controlplane/cpprovision/bucket_provisioner.go` — `BucketProvisioner` interface, `BucketInfo`, `BucketRegistry`
- `controlplane/cpprovision/aws/bucket.go` — AWS S3 implementation
- `controlplane/cpprovision/oci/bucket.go` — OCI ObjectStorage + IAM implementation
- `controlplane/cpprovision/minio/bucket.go` — MinIO via S3 SDK with custom endpoint
- `controlplane/api/buckets.go` — three new HTTP handlers (cloud-providers, discover, provision)
- `controlplane/api/buckets_test.go`, plus per-provisioner `*_test.go` files

**Backend modify:**
- `controlplane/db/cloud_credentials.go` — add `"minio"` to `AllowedCloudKinds`
- `controlplane/db/transport_configs.go` — `TransportConfigReferences(id)` query + tighten `DeleteTransportConfig` to use it
- `controlplane/api/transport_configs.go` — add `TransportConfigPatch` handler; tighten `TransportConfigDelete`
- `controlplane/server.go` — register the bucket provisioners at boot; mount new routes
- `go.mod` / `go.sum` — add S3 + OCI ObjectStorage + OCI Identity packages

**Frontend create:**
- `web/src/components/AddBucketWizard.tsx` — two-step modal
- `web/src/components/EditBucketModal.tsx` — small edit modal (name + scanner_interval_ms only)
- `web/src/components/DeleteBucketDialog.tsx` — confirmation with in-use list

**Frontend modify:**
- `web/src/api.ts` — new types + helpers for buckets endpoints + PATCH + DELETE
- `web/src/pages/settings/Cloud.tsx` — wire `BucketsSection` to the wizard and edit/delete dialogs (replace the `<Link to="/nodes">`)
- `web/src/pages/settings/Cloud.tsx` Add Provider form — add MinIO branch

---

## Task Group A — DB layer additions

### Task A1: Add "minio" to AllowedCloudKinds

**Files:**
- Modify: `controlplane/db/cloud_credentials.go`
- Modify: `controlplane/db/cloud_credentials_test.go` (or appropriate existing test file — verify presence)

- [ ] **Step A1.1: Update the whitelist**

In `controlplane/db/cloud_credentials.go`, find:

```go
var AllowedCloudKinds = []string{"oci", "aws", "gcp", "azure", "digitalocean"}
```

Replace with:

```go
var AllowedCloudKinds = []string{"oci", "aws", "gcp", "azure", "digitalocean", "minio"}
```

- [ ] **Step A1.2: Add a test that "minio" is accepted**

Append to the existing `controlplane/db/cloud_credentials_test.go` (verify the file path with `ls controlplane/db/cloud_credentials_test.go`; if absent, create it with `package db` + `import "testing"`):

```go
func TestCloudCredentialKinds_AcceptsMinio(t *testing.T) {
	found := false
	for _, k := range AllowedCloudKinds {
		if k == "minio" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'minio' in AllowedCloudKinds; got %v", AllowedCloudKinds)
	}
}
```

- [ ] **Step A1.3: Run + commit**

```bash
go test ./controlplane/db/ -run TestCloudCredentialKinds -v -count=1
```

```bash
pwd && git status   # MUST show feat/bucket-provisioning
git add controlplane/db/cloud_credentials.go controlplane/db/cloud_credentials_test.go
git commit -m "db(cloud_credentials): allow 'minio' as a cloud kind"
```

### Task A2: TransportConfigReferences + tighten DeleteTransportConfig

**Files:**
- Modify: `controlplane/db/transport_configs.go`
- Modify: `controlplane/db/transport_configs_test.go` (or create)

- [ ] **Step A2.1: Add the references query type**

In `controlplane/db/transport_configs.go`, append:

```go
// TransportConfigReferences enumerates rows in other tables that
// point to a given transport_config_id. Used by the API delete
// handler to return a 409 with an actionable list rather than
// surfacing a raw FK error.
type TransportConfigReferences struct {
	Nodes              []NamedRef
	EnrollmentPackages []NamedRef
	FederationPeers    []NamedRef
	CPProvisions       []NamedRef
}

// IsEmpty returns true when nothing references this transport_config.
func (r TransportConfigReferences) IsEmpty() bool {
	return len(r.Nodes) == 0 && len(r.EnrollmentPackages) == 0 &&
		len(r.FederationPeers) == 0 && len(r.CPProvisions) == 0
}

// HasBlockers returns true when at least one reference would
// prevent deletion via FK constraint (nodes, enrollment_packages).
// federation_peers + cp_provisions use ON DELETE SET NULL so they
// don't block, but we still surface them as info.
func (r TransportConfigReferences) HasBlockers() bool {
	return len(r.Nodes) > 0 || len(r.EnrollmentPackages) > 0
}

// NamedRef is a generic id+display tuple for cross-table reference lists.
type NamedRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}
```

If `NamedRef` already exists in another file in the `db` package, reuse it instead of redefining. Search first:

```bash
grep -n "type NamedRef\b" controlplane/db/*.go
```

- [ ] **Step A2.2: Implement TransportConfigReferences query**

Append to the same file:

```go
// TransportConfigReferences returns every row in nodes,
// enrollment_packages, federation_peers, cp_provisions that
// references the given transport_config id.
func (s *Store) TransportConfigReferences(id int64) (TransportConfigReferences, error) {
	var out TransportConfigReferences

	// nodes
	rows, err := s.Query(`SELECT id, name FROM nodes WHERE transport_config_id = ? ORDER BY id`, id)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var r NamedRef
		if err := rows.Scan(&r.ID, &r.Name); err != nil {
			rows.Close()
			return out, err
		}
		out.Nodes = append(out.Nodes, r)
	}
	rows.Close()

	// enrollment_packages
	rows, err = s.Query(`SELECT id, COALESCE(display_name,'') FROM enrollment_packages WHERE transport_config_id = ? ORDER BY id`, id)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var r NamedRef
		if err := rows.Scan(&r.ID, &r.Name); err != nil {
			rows.Close()
			return out, err
		}
		out.EnrollmentPackages = append(out.EnrollmentPackages, r)
	}
	rows.Close()

	// federation_peers
	rows, err = s.Query(`SELECT id, COALESCE(display_name,'') FROM federation_peers WHERE transport_config_id = ? ORDER BY id`, id)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var r NamedRef
		if err := rows.Scan(&r.ID, &r.Name); err != nil {
			rows.Close()
			return out, err
		}
		out.FederationPeers = append(out.FederationPeers, r)
	}
	rows.Close()

	// cp_provisions
	rows, err = s.Query(`SELECT id, COALESCE(display_name,'') FROM cp_provisions WHERE transport_config_id = ? ORDER BY id`, id)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var r NamedRef
		if err := rows.Scan(&r.ID, &r.Name); err != nil {
			rows.Close()
			return out, err
		}
		out.CPProvisions = append(out.CPProvisions, r)
	}
	rows.Close()

	return out, nil
}
```

Verify the column names actually exist on each table — they probably do but spot-check:

```bash
grep "name TEXT\|display_name TEXT" controlplane/db/migrations/sqlite/*.sql | grep -E "nodes|enrollment_packages|federation_peers|cp_provisions" | head
```

If the column on a table is named differently (e.g., `nodes` uses `hostname` not `name`), adjust the SELECT accordingly.

- [ ] **Step A2.3: Add ErrTransportConfigInUse sentinel + tighten DeleteTransportConfig**

In the same file, add an error sentinel:

```go
// ErrTransportConfigInUse is returned by DeleteTransportConfig when
// at least one row in nodes / enrollment_packages references the
// transport_config. The caller can fetch the references via
// TransportConfigReferences(id) to surface them in a 409.
var ErrTransportConfigInUse = errors.New("transport_config is referenced by nodes or enrollment_packages")
```

Add `"errors"` to the imports if not already present.

Replace the existing `DeleteTransportConfig` function:

```go
// DeleteTransportConfig deletes the row only if no rows in nodes or
// enrollment_packages reference it. Returns ErrTransportConfigInUse
// otherwise. federation_peers + cp_provisions FKs are ON DELETE
// SET NULL so they don't block (callers that care can pre-fetch
// TransportConfigReferences).
func (s *Store) DeleteTransportConfig(id int64) error {
	refs, err := s.TransportConfigReferences(id)
	if err != nil {
		return err
	}
	if refs.HasBlockers() {
		return ErrTransportConfigInUse
	}
	_, err = s.Exec(`DELETE FROM transport_configs WHERE id = ?`, id)
	return err
}
```

- [ ] **Step A2.4: Tests**

Append to `controlplane/db/transport_configs_test.go` (or create with the standard package + imports if absent):

```go
func TestTransportConfigReferences_Empty(t *testing.T) {
	s := openTempStore(t)
	tcID := mustCreateTransportConfig(t, s, "test")
	refs, err := s.TransportConfigReferences(tcID)
	if err != nil {
		t.Fatalf("TransportConfigReferences: %v", err)
	}
	if !refs.IsEmpty() {
		t.Errorf("expected empty refs; got %+v", refs)
	}
}

func TestDeleteTransportConfig_Empty(t *testing.T) {
	s := openTempStore(t)
	tcID := mustCreateTransportConfig(t, s, "test")
	if err := s.DeleteTransportConfig(tcID); err != nil {
		t.Fatalf("DeleteTransportConfig: %v", err)
	}
}

// mustCreateTransportConfig is a tiny helper for these tests. If a
// similar helper already exists in this file, reuse it instead.
func mustCreateTransportConfig(t *testing.T, s *Store, name string) int64 {
	t.Helper()
	tc := TransportConfig{
		Name:     name,
		Kind:     "s3",
		Bucket:   "test-bucket",
		Endpoint: "https://test.example",
		UseSSL:   true,
	}
	id, err := s.CreateTransportConfig(tc)
	if err != nil {
		t.Fatalf("CreateTransportConfig: %v", err)
	}
	return id
}
```

(If `Store.CreateTransportConfig` has a different signature, adjust by reading the existing function in `transport_configs.go`.)

- [ ] **Step A2.5: Run + commit**

```bash
go test ./controlplane/db/ -run "TestTransportConfigReferences|TestDeleteTransportConfig" -v -count=1
go test ./controlplane/db/ -count=1   # full sweep, no regressions
```

```bash
pwd && git status
git add controlplane/db/transport_configs.go controlplane/db/transport_configs_test.go
git commit -m "db(transport_configs): TransportConfigReferences + tighten DeleteTransportConfig with in-use guard"
```

---

## Task Group B — BucketProvisioner interface + Registry

### Task B1: Define the interface

**Files:**
- Create: `controlplane/cpprovision/bucket_provisioner.go`
- Create: `controlplane/cpprovision/bucket_registry_test.go`

- [ ] **Step B1.1: Implement the interface + types + registry**

`controlplane/cpprovision/bucket_provisioner.go`:

```go
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
	// sort.Strings(out)  // imported below
	return out
}
```

Add `"sort"` to imports + uncomment `sort.Strings(out)` if you want sorted output (recommended for stable JSON):

```go
import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)
```

```go
sort.Strings(out)
return out
```

- [ ] **Step B1.2: Tests**

`controlplane/cpprovision/bucket_registry_test.go`:

```go
package cpprovision

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type fakeBucketProvisioner struct{ cloud string }

func (f *fakeBucketProvisioner) Cloud() string { return f.cloud }
func (f *fakeBucketProvisioner) ListBuckets(_ context.Context, _ []byte, _ string) ([]BucketInfo, error) {
	return nil, nil
}
func (f *fakeBucketProvisioner) EnsureBucket(_ context.Context, _ []byte, _, _ string) (*BucketInfo, error) {
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
```

- [ ] **Step B1.3: Run + commit**

```bash
go test ./controlplane/cpprovision/ -run TestBucketRegistry -v -count=1
go vet ./controlplane/cpprovision/
```

```bash
pwd && git status
git add controlplane/cpprovision/bucket_provisioner.go controlplane/cpprovision/bucket_registry_test.go
git commit -m "cpprovision: BucketProvisioner interface + BucketRegistry"
```

---

## Task Group C — AWS BucketProvisioner

### Task C1: Add the S3 SDK dependency

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step C1.1: Add the package**

```bash
go get github.com/aws/aws-sdk-go-v2/service/s3
go mod tidy
```

- [ ] **Step C1.2: Commit the dep change separately**

```bash
git add go.mod go.sum
git commit -m "go.mod: add aws-sdk-go-v2/service/s3 for bucket provisioner"
```

### Task C2: AWS BucketProvisioner implementation

**Files:**
- Create: `controlplane/cpprovision/aws/bucket.go`
- Create: `controlplane/cpprovision/aws/bucket_test.go`

- [ ] **Step C2.1: Implement**

`controlplane/cpprovision/aws/bucket.go`:

```go
// AWS S3 bucket provisioner. Reuses the credential payload shape from
// aws.go (access_key_id + secret_access_key, optionally session_token
// or role_arn) and the same SDK config builder.

package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/section9labs/okesu/controlplane/cpprovision"
)

// BucketProvisioner is the AWS S3 implementation of
// cpprovision.BucketProvisioner.
type BucketProvisioner struct{}

func NewBucketProvisioner() cpprovision.BucketProvisioner { return &BucketProvisioner{} }

func (p *BucketProvisioner) Cloud() string { return "aws" }

func (p *BucketProvisioner) ListBuckets(ctx context.Context, credsRaw []byte, region string) ([]cpprovision.BucketInfo, error) {
	creds, err := decodeCredential(credsRaw)
	if err != nil {
		return nil, err
	}
	if region == "" {
		region = creds.Region
	}
	cfg, err := awsConfigFromCredential(ctx, creds, region)
	if err != nil {
		return nil, err
	}
	client := s3.NewFromConfig(cfg)

	out, err := client.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return nil, fmt.Errorf("aws: list buckets: %w", err)
	}
	infos := make([]cpprovision.BucketInfo, 0, len(out.Buckets))
	for _, b := range out.Buckets {
		name := safeStr(b.Name)
		// LocationConstraint per bucket — not all buckets are in `region`.
		loc, lErr := client.GetBucketLocation(ctx, &s3.GetBucketLocationInput{Bucket: b.Name})
		bucketRegion := region
		if lErr == nil {
			if string(loc.LocationConstraint) != "" {
				bucketRegion = string(loc.LocationConstraint)
			} else {
				// Empty string == us-east-1 in AWS's older convention.
				bucketRegion = "us-east-1"
			}
		}
		infos = append(infos, cpprovision.BucketInfo{
			Name:     name,
			Region:   bucketRegion,
			Endpoint: fmt.Sprintf("https://s3.%s.amazonaws.com", bucketRegion),
		})
	}
	return infos, nil
}

func (p *BucketProvisioner) EnsureBucket(ctx context.Context, credsRaw []byte, name, region string) (*cpprovision.BucketInfo, error) {
	creds, err := decodeCredential(credsRaw)
	if err != nil {
		return nil, err
	}
	if region == "" {
		region = creds.Region
	}
	cfg, err := awsConfigFromCredential(ctx, creds, region)
	if err != nil {
		return nil, err
	}
	client := s3.NewFromConfig(cfg)

	// Idempotency: HEAD first; create on 404.
	if _, err := client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: &name}); err == nil {
		return &cpprovision.BucketInfo{
			Name:     name,
			Region:   region,
			Endpoint: fmt.Sprintf("https://s3.%s.amazonaws.com", region),
		}, nil
	}

	in := &s3.CreateBucketInput{Bucket: &name}
	// us-east-1 doesn't accept a LocationConstraint; everywhere else does.
	if region != "us-east-1" {
		in.CreateBucketConfiguration = &s3types.CreateBucketConfiguration{
			LocationConstraint: s3types.BucketLocationConstraint(region),
		}
	}
	if _, err := client.CreateBucket(ctx, in); err != nil {
		return nil, fmt.Errorf("aws: create bucket %q: %w", name, err)
	}
	return &cpprovision.BucketInfo{
		Name:     name,
		Region:   region,
		Endpoint: fmt.Sprintf("https://s3.%s.amazonaws.com", region),
	}, nil
}

func (p *BucketProvisioner) BucketAccessKeys(_ context.Context, credsRaw []byte) (string, string, error) {
	creds, err := decodeCredential(credsRaw)
	if err != nil {
		return "", "", err
	}
	if creds.AccessKeyID == "" || creds.SecretAccessKey == "" {
		return "", "", errors.New("aws credential has no static access keys (role-only credentials are not supported for bucket provisioning yet)")
	}
	return creds.AccessKeyID, creds.SecretAccessKey, nil
}

func safeStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
```

(`decodeCredential` and `awsConfigFromCredential` already exist in `controlplane/cpprovision/aws/aws.go` from the EC2 path — verify and reuse. If `awsConfigFromCredential` is named differently or lives in a different file, search and adjust.)

- [ ] **Step C2.2: Test the credential-decode path (no SDK calls — that requires real AWS or mock infra outside scope of this task)**

`controlplane/cpprovision/aws/bucket_test.go`:

```go
package aws

import (
	"context"
	"errors"
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
	} else if !errors.Is(err, err) {
		// Just confirm error path triggers; message check is fine
		// since the spec says role-only is unsupported for now.
	}
}

func TestBucketProvisioner_AccessKeys_BadJSON(t *testing.T) {
	p := NewBucketProvisioner()
	if _, _, err := p.BucketAccessKeys(context.Background(), []byte(`not json`)); err == nil {
		t.Error("expected error for bad JSON")
	}
}
```

- [ ] **Step C2.3: Build + commit**

```bash
go build ./controlplane/cpprovision/aws/
go test ./controlplane/cpprovision/aws/ -run TestBucketProvisioner -v -count=1
go vet ./controlplane/cpprovision/aws/
```

```bash
pwd && git status
git add controlplane/cpprovision/aws/bucket.go controlplane/cpprovision/aws/bucket_test.go
git commit -m "cpprovision/aws: BucketProvisioner via S3 SDK"
```

---

## Task Group D — OCI BucketProvisioner

### Task D1: Add OCI SDK packages

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step D1.1: Add the packages**

The `oci-go-sdk/v65` module is already a dep; we just need additional packages from it.

```bash
go get github.com/oracle/oci-go-sdk/v65/objectstorage
go get github.com/oracle/oci-go-sdk/v65/identity
go mod tidy
```

- [ ] **Step D1.2: Commit**

```bash
git add go.mod go.sum
git commit -m "go.mod: add oci-go-sdk objectstorage + identity packages"
```

### Task D2: OCI BucketProvisioner implementation

**Files:**
- Create: `controlplane/cpprovision/oci/bucket.go`
- Create: `controlplane/cpprovision/oci/bucket_test.go`

- [ ] **Step D2.1: Implement**

`controlplane/cpprovision/oci/bucket.go`:

```go
// OCI Object Storage bucket provisioner. Uses the existing OCI
// signing config (tenancy + user + key) to call ListBuckets and
// CreateBucket. For S3-compatible access keys, calls IAM's
// CreateCustomerSecretKey for the user. Customer Secret Keys are
// per-user — a single OCI credential maps to a single S3-compat
// keypair, which is fine for v1.

package oci

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/identity"
	"github.com/oracle/oci-go-sdk/v65/objectstorage"

	"github.com/section9labs/okesu/controlplane/cpprovision"
)

type BucketProvisioner struct{}

func NewBucketProvisioner() cpprovision.BucketProvisioner { return &BucketProvisioner{} }

func (p *BucketProvisioner) Cloud() string { return "oci" }

func (p *BucketProvisioner) ListBuckets(ctx context.Context, credsRaw []byte, region string) ([]cpprovision.BucketInfo, error) {
	creds, err := decodeCredential(credsRaw)
	if err != nil {
		return nil, err
	}
	if region == "" {
		region = creds.Region
	}
	provider := common.NewRawConfigurationProvider(
		creds.TenancyOCID, creds.UserOCID, region, creds.Fingerprint, creds.PrivateKey, nil)

	client, err := objectstorage.NewObjectStorageClientWithConfigurationProvider(provider)
	if err != nil {
		return nil, fmt.Errorf("oci: build objectstorage client: %w", err)
	}

	ns, err := client.GetNamespace(ctx, objectstorage.GetNamespaceRequest{})
	if err != nil {
		return nil, fmt.Errorf("oci: get namespace: %w", err)
	}
	namespace := safeStr(ns.Value)

	resp, err := client.ListBuckets(ctx, objectstorage.ListBucketsRequest{
		NamespaceName: ns.Value,
		CompartmentId: common.String(creds.TenancyOCID), // root compartment by default
	})
	if err != nil {
		return nil, fmt.Errorf("oci: list buckets: %w", err)
	}
	out := make([]cpprovision.BucketInfo, 0, len(resp.Items))
	for _, b := range resp.Items {
		out = append(out, cpprovision.BucketInfo{
			Name:     safeStr(b.Name),
			Region:   region,
			Endpoint: ociS3Endpoint(namespace, region),
		})
	}
	return out, nil
}

func (p *BucketProvisioner) EnsureBucket(ctx context.Context, credsRaw []byte, name, region string) (*cpprovision.BucketInfo, error) {
	creds, err := decodeCredential(credsRaw)
	if err != nil {
		return nil, err
	}
	if region == "" {
		region = creds.Region
	}
	provider := common.NewRawConfigurationProvider(
		creds.TenancyOCID, creds.UserOCID, region, creds.Fingerprint, creds.PrivateKey, nil)

	client, err := objectstorage.NewObjectStorageClientWithConfigurationProvider(provider)
	if err != nil {
		return nil, fmt.Errorf("oci: build objectstorage client: %w", err)
	}

	ns, err := client.GetNamespace(ctx, objectstorage.GetNamespaceRequest{})
	if err != nil {
		return nil, fmt.Errorf("oci: get namespace: %w", err)
	}
	namespace := safeStr(ns.Value)

	// Idempotency: HEAD first; create on 404.
	_, headErr := client.GetBucket(ctx, objectstorage.GetBucketRequest{
		NamespaceName: ns.Value,
		BucketName:    common.String(name),
	})
	if headErr == nil {
		return &cpprovision.BucketInfo{
			Name:     name,
			Region:   region,
			Endpoint: ociS3Endpoint(namespace, region),
		}, nil
	}

	_, err = client.CreateBucket(ctx, objectstorage.CreateBucketRequest{
		NamespaceName: ns.Value,
		CreateBucketDetails: objectstorage.CreateBucketDetails{
			Name:          common.String(name),
			CompartmentId: common.String(creds.TenancyOCID),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("oci: create bucket %q: %w", name, err)
	}
	return &cpprovision.BucketInfo{
		Name:     name,
		Region:   region,
		Endpoint: ociS3Endpoint(namespace, region),
	}, nil
}

func (p *BucketProvisioner) BucketAccessKeys(ctx context.Context, credsRaw []byte) (string, string, error) {
	creds, err := decodeCredential(credsRaw)
	if err != nil {
		return "", "", err
	}
	provider := common.NewRawConfigurationProvider(
		creds.TenancyOCID, creds.UserOCID, creds.Region, creds.Fingerprint, creds.PrivateKey, nil)

	idClient, err := identity.NewIdentityClientWithConfigurationProvider(provider)
	if err != nil {
		return "", "", fmt.Errorf("oci: build identity client: %w", err)
	}

	display := common.String("okesu-bucket-provisioner")
	resp, err := idClient.CreateCustomerSecretKey(ctx, identity.CreateCustomerSecretKeyRequest{
		CreateCustomerSecretKeyDetails: identity.CreateCustomerSecretKeyDetails{
			DisplayName: display,
			UserId:      common.String(creds.UserOCID),
		},
	})
	if err != nil {
		// Surface the verbatim error so operators can act on permission
		// gaps (e.g., "Authorization failed... requires manage
		// customer-secret-keys").
		return "", "", fmt.Errorf("oci: create customer secret key: %w", err)
	}
	access := safeStr(resp.Id)
	secret := safeStr(resp.Key)
	if access == "" || secret == "" {
		return "", "", errors.New("oci: customer secret key creation returned empty access/secret")
	}
	return access, secret, nil
}

// ociS3Endpoint returns the S3-compatible URL for a namespace+region.
// Operators write to this URL with the Customer Secret Keys returned
// by BucketAccessKeys.
func ociS3Endpoint(namespace, region string) string {
	return fmt.Sprintf("https://%s.compat.objectstorage.%s.oraclecloud.com",
		strings.TrimSpace(namespace), strings.TrimSpace(region))
}

func safeStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
```

(`decodeCredential` already exists in `controlplane/cpprovision/oci/oci.go` — verify and reuse.)

- [ ] **Step D2.2: Test the endpoint-formatting path**

`controlplane/cpprovision/oci/bucket_test.go`:

```go
package oci

import "testing"

func TestOciS3Endpoint(t *testing.T) {
	got := ociS3Endpoint("axyz1234", "us-ashburn-1")
	want := "https://axyz1234.compat.objectstorage.us-ashburn-1.oraclecloud.com"
	if got != want {
		t.Errorf("ociS3Endpoint = %q, want %q", got, want)
	}
}
```

(Larger SDK-mocked tests are deferred to handler-level + lab smoke.)

- [ ] **Step D2.3: Build + commit**

```bash
go build ./controlplane/cpprovision/oci/
go test ./controlplane/cpprovision/oci/ -run TestOciS3Endpoint -v -count=1
go vet ./controlplane/cpprovision/oci/
```

```bash
pwd && git status
git add controlplane/cpprovision/oci/bucket.go controlplane/cpprovision/oci/bucket_test.go
git commit -m "cpprovision/oci: BucketProvisioner via ObjectStorage + IAM Customer Secret Keys"
```

---

## Task Group E — MinIO BucketProvisioner

### Task E1: MinIO via S3 SDK with custom endpoint

**Files:**
- Create: `controlplane/cpprovision/minio/bucket.go`
- Create: `controlplane/cpprovision/minio/bucket_test.go`

- [ ] **Step E1.1: Implement**

`controlplane/cpprovision/minio/bucket.go`:

```go
// MinIO is fully S3-compatible — we reuse the AWS S3 SDK with a
// custom endpoint pulled from the operator's MinIO cloud_credential.

package minio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/section9labs/okesu/controlplane/cpprovision"
)

// credentialPayload is what the Add Provider modal stores for MinIO.
// region is optional (most MinIO deployments are regionless).
type credentialPayload struct {
	Endpoint        string `json:"endpoint"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	Region          string `json:"region,omitempty"`
}

func decodeCredential(raw []byte) (*credentialPayload, error) {
	var c credentialPayload
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("minio credential: parse: %w", err)
	}
	if c.Endpoint == "" {
		return nil, errors.New("minio credential missing fields: endpoint")
	}
	if c.AccessKeyID == "" || c.SecretAccessKey == "" {
		return nil, errors.New("minio credential missing fields: access_key_id, secret_access_key")
	}
	return &c, nil
}

// BucketProvisioner is the MinIO implementation of cpprovision.BucketProvisioner.
type BucketProvisioner struct{}

func NewBucketProvisioner() cpprovision.BucketProvisioner { return &BucketProvisioner{} }

func (p *BucketProvisioner) Cloud() string { return "minio" }

func (p *BucketProvisioner) ListBuckets(ctx context.Context, credsRaw []byte, _ string) ([]cpprovision.BucketInfo, error) {
	creds, err := decodeCredential(credsRaw)
	if err != nil {
		return nil, err
	}
	client := s3Client(creds)
	out, err := client.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return nil, fmt.Errorf("minio: list buckets: %w", err)
	}
	infos := make([]cpprovision.BucketInfo, 0, len(out.Buckets))
	for _, b := range out.Buckets {
		name := ""
		if b.Name != nil {
			name = *b.Name
		}
		infos = append(infos, cpprovision.BucketInfo{
			Name:     name,
			Region:   creds.Region,
			Endpoint: creds.Endpoint,
		})
	}
	return infos, nil
}

func (p *BucketProvisioner) EnsureBucket(ctx context.Context, credsRaw []byte, name, _ string) (*cpprovision.BucketInfo, error) {
	creds, err := decodeCredential(credsRaw)
	if err != nil {
		return nil, err
	}
	client := s3Client(creds)

	if _, err := client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: &name}); err == nil {
		return &cpprovision.BucketInfo{Name: name, Region: creds.Region, Endpoint: creds.Endpoint}, nil
	}
	if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: &name}); err != nil {
		return nil, fmt.Errorf("minio: create bucket %q: %w", name, err)
	}
	return &cpprovision.BucketInfo{Name: name, Region: creds.Region, Endpoint: creds.Endpoint}, nil
}

func (p *BucketProvisioner) BucketAccessKeys(_ context.Context, credsRaw []byte) (string, string, error) {
	creds, err := decodeCredential(credsRaw)
	if err != nil {
		return "", "", err
	}
	return creds.AccessKeyID, creds.SecretAccessKey, nil
}

func s3Client(c *credentialPayload) *s3.Client {
	return s3.New(s3.Options{
		Region:       ifEmpty(c.Region, "us-east-1"),
		BaseEndpoint: awssdk.String(c.Endpoint),
		UsePathStyle: true,
		Credentials:  credentials.NewStaticCredentialsProvider(c.AccessKeyID, c.SecretAccessKey, ""),
	})
}

func ifEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
```

- [ ] **Step E1.2: Test the credential-decode path**

`controlplane/cpprovision/minio/bucket_test.go`:

```go
package minio

import (
	"context"
	"testing"
)

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
```

- [ ] **Step E1.3: Build + commit**

```bash
go build ./controlplane/cpprovision/minio/
go test ./controlplane/cpprovision/minio/ -run TestBucketProvisioner -v -count=1
go vet ./controlplane/cpprovision/minio/
```

```bash
pwd && git status
git add controlplane/cpprovision/minio/bucket.go controlplane/cpprovision/minio/bucket_test.go
git commit -m "cpprovision/minio: BucketProvisioner via S3 SDK with custom endpoint"
```

---

## Task Group F — Buckets HTTP handlers

### Task F1: Three new handlers (cloud-providers, discover, provision)

**Files:**
- Create: `controlplane/api/buckets.go`
- Create: `controlplane/api/buckets_test.go`

- [ ] **Step F1.1: Implement**

`controlplane/api/buckets.go`:

```go
// Bucket provisioning HTTP handlers — surface the BucketRegistry to
// the Settings → Add Bucket wizard.

package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/section9labs/okesu/controlplane/cpprovision"
	"github.com/section9labs/okesu/controlplane/db"
)

// bucketProviderJSON is the wire shape returned by
// GET /api/buckets/cloud-providers — same projection as Add-CP uses.
type bucketProviderJSON struct {
	ID          int64  `json:"id"`
	Cloud       string `json:"cloud"`
	DisplayName string `json:"display_name"`
	Region      string `json:"region,omitempty"`
}

// BucketCloudProviders returns cloud_credentials filtered to the
// kinds for which we have a BucketProvisioner registered.
func BucketCloudProviders(store *db.Store, registry *cpprovision.BucketRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		supported := registry.Clouds()
		creds, err := store.ListCloudCredentials()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]bucketProviderJSON, 0)
		for _, c := range creds {
			if !contains(supported, c.Cloud) {
				continue
			}
			out = append(out, bucketProviderJSON{
				ID:          c.ID,
				Cloud:       c.Cloud,
				DisplayName: c.DisplayName,
				Region:      c.Region,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// BucketDiscover lists existing buckets in the credential's account.
func BucketDiscover(store *db.Store, registry *cpprovision.BucketRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := r.URL.Query().Get("cloud_credential_id")
		region := r.URL.Query().Get("region")
		credID, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || credID == 0 {
			http.Error(w, "missing or bad cloud_credential_id", http.StatusBadRequest)
			return
		}
		cred, err := store.GetCloudCredential(credID)
		if err != nil {
			http.Error(w, "cloud credential not found", http.StatusNotFound)
			return
		}
		provisioner, err := registry.Get(cred.Cloud)
		if err != nil {
			http.Error(w, fmt.Sprintf("no bucket provisioner for cloud %q", cred.Cloud), http.StatusBadRequest)
			return
		}
		payload, err := store.DecryptCloudCredentialPayload(cred)
		if err != nil {
			http.Error(w, "decrypt credential: "+err.Error(), http.StatusInternalServerError)
			return
		}
		buckets, err := provisioner.ListBuckets(r.Context(), payload, region)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		if buckets == nil {
			buckets = []cpprovision.BucketInfo{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(buckets)
	}
}

// bucketProvisionReq is the wire shape of POST /api/buckets/provision.
type bucketProvisionReq struct {
	CloudCredentialID int64  `json:"cloud_credential_id"`
	Region            string `json:"region"`
	BucketName        string `json:"bucket_name"`
	Mode              string `json:"mode"` // "discover" | "create"
	DisplayName       string `json:"display_name"`
	GenerateFleetKeys bool   `json:"generate_fleet_keys"`
	ScannerIntervalMs int    `json:"scanner_interval_ms"`
}

// BucketProvision either discovers an existing bucket and writes a
// transport_configs row, or creates a new bucket via SDK and writes
// the row. Returns the new transport_configs row.
func BucketProvision(store *db.Store, registry *cpprovision.BucketRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req bucketProvisionReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if req.CloudCredentialID == 0 || req.BucketName == "" || req.DisplayName == "" {
			http.Error(w, "missing required fields", http.StatusBadRequest)
			return
		}
		switch req.Mode {
		case "discover", "create":
		default:
			http.Error(w, "mode must be 'discover' or 'create'", http.StatusBadRequest)
			return
		}

		cred, err := store.GetCloudCredential(req.CloudCredentialID)
		if err != nil {
			http.Error(w, "cloud credential not found", http.StatusNotFound)
			return
		}
		provisioner, err := registry.Get(cred.Cloud)
		if err != nil {
			http.Error(w, "no bucket provisioner for cloud "+cred.Cloud, http.StatusBadRequest)
			return
		}
		payload, err := store.DecryptCloudCredentialPayload(cred)
		if err != nil {
			http.Error(w, "decrypt credential: "+err.Error(), http.StatusInternalServerError)
			return
		}

		var info *cpprovision.BucketInfo
		switch req.Mode {
		case "discover":
			list, err := provisioner.ListBuckets(r.Context(), payload, req.Region)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			for i := range list {
				if list[i].Name == req.BucketName {
					info = &list[i]
					break
				}
			}
			if info == nil {
				http.Error(w, fmt.Sprintf("bucket %q not found in account", req.BucketName), http.StatusNotFound)
				return
			}
		case "create":
			info, err = provisioner.EnsureBucket(r.Context(), payload, req.BucketName, req.Region)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
		}

		access, secret, err := provisioner.BucketAccessKeys(r.Context(), payload)
		if err != nil {
			http.Error(w, "obtain access keys: "+err.Error(), http.StatusBadGateway)
			return
		}

		tc := db.TransportConfig{
			Name:              strings.TrimSpace(req.DisplayName),
			Kind:              "s3",
			Bucket:            info.Name,
			Endpoint:          info.Endpoint,
			Region:            info.Region,
			UseSSL:            strings.HasPrefix(info.Endpoint, "https://"),
			AccessKey:         nullableString(access),
			SecretKey:         nullableString(secret),
			ScannerIntervalMs: req.ScannerIntervalMs,
		}
		if req.GenerateFleetKeys {
			pubPEM, privPEM, err := generateFleetKeypair()
			if err != nil {
				http.Error(w, "fleet keys: "+err.Error(), http.StatusInternalServerError)
				return
			}
			tc.FleetPubkeyPEM = nullableString(pubPEM)
			tc.FleetPrivkeyPEM = nullableString(privPEM)
		}
		// Random unique CP id to satisfy the schema's expectation.
		tc.CPID = nullableString(randomCPID())
		id, err := store.CreateTransportConfig(tc)
		if err != nil {
			http.Error(w, "create transport_config: "+err.Error(), http.StatusInternalServerError)
			return
		}
		tc.ID = id
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tc)
	}
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// randomCPID returns a hex string suitable for transport_configs.cp_id.
// 16 bytes → 32 hex chars matches the existing format used elsewhere.
func randomCPID() string {
	var buf [16]byte
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}
```

Verify these helper signatures by searching:

```bash
grep -n "func nullableString\|func generateFleetKeypair\|func .s..*ListCloudCredentials\|func .s..*GetCloudCredential\|func .s..*DecryptCloudCredentialPayload\|func .s..*CreateTransportConfig" controlplane/api/*.go controlplane/db/*.go
```

If `DecryptCloudCredentialPayload` is named differently (e.g., `cred.Payload` after a separate decrypt step), adjust. If `nullableString` doesn't exist as an api-package helper, define it locally:

```go
func nullableString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
```

(adding `"database/sql"` to imports.)

- [ ] **Step F1.2: Tests**

`controlplane/api/buckets_test.go`:

```go
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/section9labs/okesu/controlplane/cpprovision"
	"github.com/section9labs/okesu/controlplane/db"
)

// fakeBucketProvisioner is a minimal stub for tests so handlers
// can exercise the wire path without real SDK calls.
type fakeBucketProvisioner struct {
	cloud   string
	buckets []cpprovision.BucketInfo
}

func (f *fakeBucketProvisioner) Cloud() string { return f.cloud }
func (f *fakeBucketProvisioner) ListBuckets(_ context.Context, _ []byte, _ string) ([]cpprovision.BucketInfo, error) {
	return f.buckets, nil
}
func (f *fakeBucketProvisioner) EnsureBucket(_ context.Context, _ []byte, name, region string) (*cpprovision.BucketInfo, error) {
	bi := cpprovision.BucketInfo{Name: name, Region: region, Endpoint: "https://fake.example"}
	return &bi, nil
}
func (f *fakeBucketProvisioner) BucketAccessKeys(_ context.Context, _ []byte) (string, string, error) {
	return "AK", "SK", nil
}

func TestBucketCloudProviders_FiltersByRegistry(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.CreateCloudCredential(&db.CloudCredentialInsert{
		Cloud: "aws", DisplayName: "AWS-prod", Region: "us-east-1", Payload: []byte(`{}`),
	}); err != nil {
		t.Fatalf("CreateCloudCredential aws: %v", err)
	}
	if _, err := st.CreateCloudCredential(&db.CloudCredentialInsert{
		Cloud: "gcp", DisplayName: "GCP-test", Region: "us-central1", Payload: []byte(`{}`),
	}); err != nil {
		t.Fatalf("CreateCloudCredential gcp: %v", err)
	}

	reg := cpprovision.NewBucketRegistry()
	reg.Register(&fakeBucketProvisioner{cloud: "aws"})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/buckets/cloud-providers", nil)
	BucketCloudProviders(st, reg)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var got []bucketProviderJSON
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].Cloud != "aws" {
		t.Errorf("expected one aws row; got %+v", got)
	}
}

func TestBucketProvision_CreateMode(t *testing.T) {
	st := newTestStore(t)
	credID, err := st.CreateCloudCredential(&db.CloudCredentialInsert{
		Cloud: "aws", DisplayName: "aws-prod", Region: "us-east-1",
		Payload: []byte(`{"access_key_id":"AK","secret_access_key":"SK","region":"us-east-1"}`),
	})
	if err != nil {
		t.Fatalf("CreateCloudCredential: %v", err)
	}
	reg := cpprovision.NewBucketRegistry()
	reg.Register(&fakeBucketProvisioner{cloud: "aws"})

	body := bucketProvisionReq{
		CloudCredentialID: credID,
		Region:            "us-east-1",
		BucketName:        "newbucket",
		Mode:              "create",
		DisplayName:       "Prod bucket",
	}
	buf, _ := json.Marshal(body)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/buckets/provision", bytes.NewReader(buf))
	BucketProvision(st, reg)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got db.TransportConfig
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Bucket != "newbucket" || got.Endpoint != "https://fake.example" {
		t.Errorf("unexpected transport_config: %+v", got)
	}
}

func TestBucketProvision_DiscoverMode_NotFound(t *testing.T) {
	st := newTestStore(t)
	credID, _ := st.CreateCloudCredential(&db.CloudCredentialInsert{
		Cloud: "aws", DisplayName: "x", Region: "us-east-1",
		Payload: []byte(`{}`),
	})
	reg := cpprovision.NewBucketRegistry()
	reg.Register(&fakeBucketProvisioner{cloud: "aws", buckets: []cpprovision.BucketInfo{
		{Name: "other", Region: "us-east-1", Endpoint: "https://fake.example"},
	}})

	body := bucketProvisionReq{
		CloudCredentialID: credID,
		Region:            "us-east-1",
		BucketName:        "missing",
		Mode:              "discover",
		DisplayName:       "should not create",
	}
	buf, _ := json.Marshal(body)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/buckets/provision", bytes.NewReader(buf))
	BucketProvision(st, reg)(rec, req)
	if rec.Code != 404 {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestBucketDiscover_BadID(t *testing.T) {
	st := newTestStore(t)
	reg := cpprovision.NewBucketRegistry()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/buckets/discover?cloud_credential_id=abc", nil)
	BucketDiscover(st, reg)(rec, req)
	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
```

If `db.CloudCredentialInsert` field names differ from this snippet, adjust by reading `controlplane/db/cloud_credentials.go`. The tests need to match the existing insert API.

- [ ] **Step F1.3: Run + commit**

```bash
go test ./controlplane/api/ -run "TestBucketCloudProviders|TestBucketProvision|TestBucketDiscover" -v -count=1
go vet ./controlplane/api/
```

```bash
pwd && git status
git add controlplane/api/buckets.go controlplane/api/buckets_test.go
git commit -m "api(buckets): cloud-providers + discover + provision handlers"
```

---

## Task Group G — PATCH + tightened DELETE on transport_configs

### Task G1: PATCH endpoint + DELETE 409 guard

**Files:**
- Modify: `controlplane/api/transport_configs.go`
- Modify: `controlplane/api/transport_configs_test.go` (or create)

- [ ] **Step G1.1: Add PATCH handler with safe partial-update semantics**

In `controlplane/api/transport_configs.go`, append:

```go
// transportConfigPatchReq is the wire shape for PATCH. Only fields
// that are safe to edit post-creation appear here. Identity fields
// (bucket, endpoint, access_key, secret_key, region,
// cloud_credential_id) are intentionally absent — changing them
// would silently break enrollment packages and federated peers
// tied to the old identity. Operators who need a different bucket
// delete + re-add. Key rotation is deferred to a dedicated wizard.
type transportConfigPatchReq struct {
	Name              *string `json:"name,omitempty"`
	ScannerIntervalMs *int    `json:"scanner_interval_ms,omitempty"`
}

// TransportConfigPatch applies partial updates to a transport_config.
// Unrecognized fields in the body are silently ignored (the JSON
// decoder drops them); identity fields are not in transportConfigPatchReq
// so attempting to change them is a no-op.
func TransportConfigPatch(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		existing, err := store.GetTransportConfig(id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		var patch transportConfigPatchReq
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if patch.Name != nil {
			existing.Name = strings.TrimSpace(*patch.Name)
		}
		if patch.ScannerIntervalMs != nil {
			existing.ScannerIntervalMs = *patch.ScannerIntervalMs
		}
		if err := store.UpdateTransportConfig(existing); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(existing)
	}
}
```

Add `"strings"` to imports if not already there.

- [ ] **Step G1.2: Replace TransportConfigDelete with the in-use guard**

Replace the existing `TransportConfigDelete` handler:

```go
// transportConfigDeleteConflict is the body returned by DELETE when
// the row is referenced. Frontend renders an actionable list with
// deep-links to the relevant pages.
type transportConfigDeleteConflict struct {
	Message    string                       `json:"message"`
	ReferencedBy db.TransportConfigReferences `json:"referenced_by"`
}

// TransportConfigDelete removes a transport_config row, refusing
// with 409 + a referencing-resources list if any nodes or
// enrollment_packages reference it. (federation_peers and
// cp_provisions FKs use ON DELETE SET NULL; they're surfaced in the
// list as info but don't block.)
func TransportConfigDelete(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		refs, err := store.TransportConfigReferences(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if refs.HasBlockers() {
			conflict := transportConfigDeleteConflict{
				Message:      "transport_config is referenced by nodes or enrollment_packages; remove those dependencies first",
				ReferencedBy: refs,
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(conflict)
			return
		}
		if err := store.DeleteTransportConfig(id); err != nil {
			if errors.Is(err, db.ErrTransportConfigInUse) {
				// Race: refs query saw it clean but DELETE saw a new
				// dependency. Re-fetch and surface as 409.
				refs, _ := store.TransportConfigReferences(id)
				conflict := transportConfigDeleteConflict{
					Message:      "transport_config became referenced during deletion; retry after removing the new dependencies",
					ReferencedBy: refs,
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(conflict)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
```

Add `"errors"` to imports if not already there.

- [ ] **Step G1.3: Tests**

`controlplane/api/transport_configs_test.go` (create if absent):

```go
package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/db"
)

func TestTransportConfigPatch_NameAndScannerInterval(t *testing.T) {
	st := newTestStore(t)
	tcID, err := st.CreateTransportConfig(db.TransportConfig{
		Name: "old", Kind: "s3", Bucket: "b", Endpoint: "https://e", UseSSL: true,
	})
	if err != nil {
		t.Fatalf("CreateTransportConfig: %v", err)
	}

	r := chi.NewRouter()
	r.Patch("/api/transport-configs/{id}", TransportConfigPatch(st))

	body, _ := json.Marshal(map[string]any{
		"name":                "new-name",
		"scanner_interval_ms": 60000,
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/api/transport-configs/"+strconv.FormatInt(tcID, 10), bytes.NewReader(body))
	r.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	got, _ := st.GetTransportConfig(tcID)
	if got.Name != "new-name" {
		t.Errorf("Name = %q, want new-name", got.Name)
	}
	if got.ScannerIntervalMs != 60000 {
		t.Errorf("ScannerIntervalMs = %d, want 60000", got.ScannerIntervalMs)
	}
}

func TestTransportConfigPatch_IdentityFieldsIgnored(t *testing.T) {
	st := newTestStore(t)
	tcID, _ := st.CreateTransportConfig(db.TransportConfig{
		Name: "test", Kind: "s3", Bucket: "original", Endpoint: "https://original", UseSSL: true,
	})

	r := chi.NewRouter()
	r.Patch("/api/transport-configs/{id}", TransportConfigPatch(st))

	body, _ := json.Marshal(map[string]any{
		"bucket":   "should-be-ignored",
		"endpoint": "https://should-be-ignored",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/api/transport-configs/"+strconv.FormatInt(tcID, 10), bytes.NewReader(body))
	r.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	got, _ := st.GetTransportConfig(tcID)
	if got.Bucket != "original" || got.Endpoint != "https://original" {
		t.Errorf("identity fields should not change; got bucket=%q endpoint=%q", got.Bucket, got.Endpoint)
	}
}

func TestTransportConfigDelete_409WhenReferenced(t *testing.T) {
	st := newTestStore(t)
	tcID, _ := st.CreateTransportConfig(db.TransportConfig{
		Name: "x", Kind: "s3", Bucket: "b", Endpoint: "https://e", UseSSL: true,
	})
	// Insert a node that references this transport_config.
	if _, err := st.InsertNode(&db.NodeInsert{
		Name: "node-1", Hostname: "host-1", TransportConfigID: tcID,
	}); err != nil {
		t.Fatalf("InsertNode: %v", err)
	}

	r := chi.NewRouter()
	r.Delete("/api/transport-configs/{id}", TransportConfigDelete(st))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/transport-configs/"+strconv.FormatInt(tcID, 10), nil)
	r.ServeHTTP(rec, req)
	if rec.Code != 409 {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	var got transportConfigDeleteConflict
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.ReferencedBy.Nodes) != 1 {
		t.Errorf("expected 1 referencing node; got %+v", got.ReferencedBy)
	}
}

func TestTransportConfigDelete_204WhenClean(t *testing.T) {
	st := newTestStore(t)
	tcID, _ := st.CreateTransportConfig(db.TransportConfig{
		Name: "x", Kind: "s3", Bucket: "b", Endpoint: "https://e", UseSSL: true,
	})

	r := chi.NewRouter()
	r.Delete("/api/transport-configs/{id}", TransportConfigDelete(st))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/transport-configs/"+strconv.FormatInt(tcID, 10), nil)
	r.ServeHTTP(rec, req)
	if rec.Code != 204 {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
}
```

If `db.NodeInsert` has different field names (e.g., `TransportConfigID` is `sql.NullInt64`, not `int64`), adjust the test. Read `controlplane/db/nodes.go` to confirm.

- [ ] **Step G1.4: Run + commit**

```bash
go test ./controlplane/api/ -run "TestTransportConfigPatch|TestTransportConfigDelete" -v -count=1
go vet ./controlplane/api/
```

```bash
pwd && git status
git add controlplane/api/transport_configs.go controlplane/api/transport_configs_test.go
git commit -m "api(transport_configs): add PATCH + tighten DELETE with in-use 409 guard"
```

---

## Task Group H — Server.go wiring

### Task H1: Register provisioners + mount routes

**Files:**
- Modify: `controlplane/server.go`

- [ ] **Step H1.1: Add a BucketRegistry to the server struct + init**

In `controlplane/server.go`, find the existing server struct (search `s.fedAgg`, `s.store`, etc. for the pattern). Add:

```go
type Server struct {
    // ... existing fields
    bucketRegistry *cpprovision.BucketRegistry
}
```

In the constructor (or wherever `srv.fedAgg = federation.NewAggregator(store)` is built — typically in a `Serve()` or `New()` function), add:

```go
srv.bucketRegistry = cpprovision.NewBucketRegistry()
srv.bucketRegistry.Register(awscloud.NewBucketProvisioner())
srv.bucketRegistry.Register(ocicloud.NewBucketProvisioner())
srv.bucketRegistry.Register(miniocloud.NewBucketProvisioner())
```

Where `awscloud`, `ocicloud`, `miniocloud` are the package import aliases. The import block at the top of `server.go` will need:

```go
import (
    // ... existing imports
    awscloud "github.com/section9labs/okesu/controlplane/cpprovision/aws"
    miniocloud "github.com/section9labs/okesu/controlplane/cpprovision/minio"
    ocicloud "github.com/section9labs/okesu/controlplane/cpprovision/oci"
)
```

(Check whether the existing imports already alias these packages. If `aws` is imported unqualified, the bucket provisioner constructor is `aws.NewBucketProvisioner()` — match what's already there.)

- [ ] **Step H1.2: Mount the new routes inside the cookie-auth viewer+ group**

Find the existing transport_configs route block (around `r.Get("/api/transport-configs", ...)`). Add the new bucket routes nearby:

```go
// Buckets — Settings → Add Bucket wizard.
r.Get("/api/buckets/cloud-providers", api.BucketCloudProviders(s.store, s.bucketRegistry))
r.Get("/api/buckets/discover", api.BucketDiscover(s.store, s.bucketRegistry))
r.Post("/api/buckets/provision", api.BucketProvision(s.store, s.bucketRegistry))

// Transport configs — existing GET/POST/PUT/DELETE plus new PATCH.
r.Patch("/api/transport-configs/{id}", api.TransportConfigPatch(s.store))
```

(The existing `r.Delete("/api/transport-configs/{id}", api.TransportConfigDelete(s.store))` line stays as-is — Group G's tightened TransportConfigDelete is the same signature.)

- [ ] **Step H1.3: Build + commit**

```bash
go build ./controlplane/...
go test ./controlplane/api/ -count=1
go vet ./controlplane/...
```

```bash
pwd && git status
git add controlplane/server.go
git commit -m "server: register bucket provisioners + mount /api/buckets/* + PATCH /api/transport-configs/{id}"
```

---

## Task Group I — Frontend api.ts additions

### Task I1: Types + helpers

**Files:**
- Modify: `web/src/api.ts`

- [ ] **Step I1.1: Install web deps**

```bash
cd web && (test -d node_modules || npm install --silent) && cd ..
```

- [ ] **Step I1.2: Add types**

In `web/src/api.ts`, near the existing `TransportConfigSummary` / `TransportConfigCreateReq` types (search for them), add:

```ts
// Bucket provisioning (Settings → Add Bucket wizard, post-CRUD-gap fill).
export interface BucketCloudProvider {
  id: number;
  cloud: string;          // 'aws' | 'oci' | 'minio'
  display_name: string;
  region?: string;
}

export interface BucketInfo {
  name: string;
  region: string;
  endpoint: string;
}

export interface BucketProvisionReq {
  cloud_credential_id: number;
  region: string;
  bucket_name: string;
  mode: 'discover' | 'create';
  display_name: string;
  generate_fleet_keys: boolean;
  scanner_interval_ms: number;
}

export interface TransportConfigPatch {
  name?: string;
  scanner_interval_ms?: number;
}

export interface TransportConfigReferences {
  Nodes: { id: number; name: string }[];
  EnrollmentPackages: { id: number; name: string }[];
  FederationPeers: { id: number; name: string }[];
  CPProvisions: { id: number; name: string }[];
}

export interface TransportConfigDeleteConflict {
  message: string;
  referenced_by: TransportConfigReferences;
}
```

- [ ] **Step I1.3: Add api helpers**

Find the existing `transportConfigs:` and `transportConfigCreate:` block. Add alongside:

```ts
  bucketCloudProviders: () =>
    request<BucketCloudProvider[]>('/api/buckets/cloud-providers'),

  bucketsDiscover: (cloudCredentialID: number, region: string) =>
    request<BucketInfo[]>(
      `/api/buckets/discover?cloud_credential_id=${cloudCredentialID}&region=${encodeURIComponent(region)}`,
    ),

  bucketsProvision: (req: BucketProvisionReq) =>
    request<TransportConfigSummary>('/api/buckets/provision', {
      method: 'POST',
      body: JSON.stringify(req),
    }),

  transportConfigPatch: (id: number, patch: TransportConfigPatch) =>
    request<TransportConfigSummary>(`/api/transport-configs/${id}`, {
      method: 'PATCH',
      body: JSON.stringify(patch),
    }),

  // Returns void on 204 success. Throws ApiError(409) on in-use conflict;
  // the caller can catch and decode the body via err.body for the
  // referenced_by list.
  transportConfigDelete: (id: number) =>
    request<void>(`/api/transport-configs/${id}`, { method: 'DELETE' }),
```

- [ ] **Step I1.4: Build + commit**

```bash
cd web && npm run build && cd ..
```

```bash
pwd && git status
git add web/src/api.ts
git commit -m "web(api): bucket provisioning + transport_config PATCH/DELETE helpers"
```

---

## Task Group J — AddBucketWizard component

### Task J1: Two-step wizard

**Files:**
- Create: `web/src/components/AddBucketWizard.tsx`

- [ ] **Step J1.1: Implement the wizard**

`web/src/components/AddBucketWizard.tsx`:

```tsx
import { useEffect, useState } from 'react';
import { Loader2, Plus, X } from 'lucide-react';

import {
  api,
  type BucketCloudProvider,
  type BucketInfo,
  type BucketProvisionReq,
  type TransportConfigCreateReq,
  type TransportConfigSummary,
} from '../api';
import { cn } from '../lib/cn';

type Source = 'configured' | 'manual';
type SubMode = 'discover' | 'create';

interface Props {
  onClose: () => void;
  onCreated: (tc: TransportConfigSummary) => void;
}

export default function AddBucketWizard({ onClose, onCreated }: Props) {
  const [step, setStep] = useState<1 | 2>(1);
  const [source, setSource] = useState<Source>('configured');
  const [providers, setProviders] = useState<BucketCloudProvider[] | null>(null);
  const [providerID, setProviderID] = useState<number | null>(null);
  const [region, setRegion] = useState('');
  const [subMode, setSubMode] = useState<SubMode>('create');
  const [bucketName, setBucketName] = useState('');
  const [discovered, setDiscovered] = useState<BucketInfo[] | null>(null);
  const [discoverLoading, setDiscoverLoading] = useState(false);
  const [displayName, setDisplayName] = useState('');
  const [scannerIntervalMs, setScannerIntervalMs] = useState(30000);
  const [generateFleetKeys, setGenerateFleetKeys] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Manual-entry fields (the existing manual flow's shape).
  const [manual, setManual] = useState<TransportConfigCreateReq>({
    name: '',
    kind: 's3',
    bucket: '',
    endpoint: '',
    region: '',
    use_ssl: true,
    access_key: '',
    secret_key: '',
    generate_fleet_keys: true,
    scanner_interval_ms: 30000,
  });

  useEffect(() => {
    api.bucketCloudProviders()
      .then((p) => {
        setProviders(p ?? []);
        if ((p ?? []).length === 0) setSource('manual');
      })
      .catch((e) => setError(String(e)));
  }, []);

  // When provider+region change in configured mode, kick off discovery.
  useEffect(() => {
    if (source !== 'configured' || !providerID || !region) return;
    if (subMode !== 'discover') return;
    setDiscoverLoading(true);
    setDiscovered(null);
    api.bucketsDiscover(providerID, region)
      .then((rows) => setDiscovered(rows ?? []))
      .catch((e) => setError(String(e)))
      .finally(() => setDiscoverLoading(false));
  }, [source, providerID, region, subMode]);

  function submitConfigured() {
    if (!providerID || !displayName || !bucketName || !region) {
      setError('Pick a provider, region, and bucket; enter a display name.');
      return;
    }
    const req: BucketProvisionReq = {
      cloud_credential_id: providerID,
      region,
      bucket_name: bucketName,
      mode: subMode,
      display_name: displayName,
      generate_fleet_keys: generateFleetKeys,
      scanner_interval_ms: scannerIntervalMs,
    };
    setSubmitting(true);
    setError(null);
    api.bucketsProvision(req)
      .then((tc) => onCreated(tc))
      .catch((e) => setError(String(e)))
      .finally(() => setSubmitting(false));
  }

  function submitManual() {
    if (!manual.name || !manual.bucket || !manual.endpoint) {
      setError('Display name, bucket, and endpoint are required.');
      return;
    }
    setSubmitting(true);
    setError(null);
    api.transportConfigCreate(manual)
      .then((tc) => onCreated(tc))
      .catch((e) => setError(String(e)))
      .finally(() => setSubmitting(false));
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <div className="bg-panel border border-border rounded-lg shadow-lg w-[640px] max-h-[90vh] overflow-y-auto">
        <div className="px-5 py-3 border-b border-border flex items-center justify-between">
          <h2 className="text-sm font-semibold flex items-center gap-2">
            <Plus size={14} /> Add bucket
          </h2>
          <button onClick={onClose} className="text-ink-mute hover:text-ink" aria-label="Close">
            <X size={16} />
          </button>
        </div>

        <div className="px-5 py-4 space-y-4">
          {error && (
            <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">
              {error}
            </div>
          )}

          {step === 1 && (
            <>
              <div className="space-y-2">
                <label className="flex items-start gap-2 text-sm">
                  <input
                    type="radio"
                    checked={source === 'configured'}
                    onChange={() => setSource('configured')}
                    disabled={(providers?.length ?? 0) === 0}
                  />
                  <div>
                    <div className="font-medium">Use a configured cloud provider</div>
                    <div className="text-[11px] text-ink-mute">
                      AWS / OCI / MinIO. Use credentials already saved in Settings → Cloud.
                    </div>
                  </div>
                </label>
                <label className="flex items-start gap-2 text-sm">
                  <input type="radio" checked={source === 'manual'} onChange={() => setSource('manual')} />
                  <div>
                    <div className="font-medium">Manual entry</div>
                    <div className="text-[11px] text-ink-mute">
                      Type bucket + endpoint + access keys by hand. Useful for on-prem or third-party S3-compatible services.
                    </div>
                  </div>
                </label>
              </div>
              {source === 'configured' && (
                <div className="space-y-2">
                  <label className="text-xs text-ink-mute">Provider</label>
                  <select
                    value={providerID ?? ''}
                    onChange={(e) => setProviderID(e.target.value ? Number(e.target.value) : null)}
                    className="w-full text-sm px-2.5 py-1.5 rounded-md ring-1 ring-border bg-white"
                  >
                    <option value="">Pick one…</option>
                    {(providers ?? []).map((p) => (
                      <option key={p.id} value={p.id}>
                        {p.display_name} · {p.cloud}
                        {p.region ? ` · ${p.region}` : ''}
                      </option>
                    ))}
                  </select>
                  {(providers ?? []).length === 0 && (
                    <div className="text-[11px] text-ink-mute">
                      No AWS/OCI/MinIO credentials configured. Add one in Settings → Cloud first.
                    </div>
                  )}
                </div>
              )}
              <div className="flex justify-end gap-2 pt-2">
                <button onClick={onClose} className="text-xs px-3 py-1.5 border border-border rounded-md hover:bg-slate-50">
                  Cancel
                </button>
                <button
                  onClick={() => setStep(2)}
                  disabled={source === 'configured' && !providerID}
                  className="text-xs px-3 py-1.5 bg-brand-600 text-white rounded-md hover:bg-brand-700 disabled:opacity-50"
                >
                  Next →
                </button>
              </div>
            </>
          )}

          {step === 2 && source === 'configured' && (
            <>
              <div className="space-y-2">
                <label className="text-xs text-ink-mute">Region</label>
                <input
                  type="text"
                  value={region}
                  onChange={(e) => setRegion(e.target.value)}
                  placeholder="us-east-1"
                  className="w-full text-sm px-2.5 py-1.5 rounded-md ring-1 ring-border bg-white"
                />
              </div>
              <div className="flex gap-1 border-b border-border">
                <button
                  onClick={() => setSubMode('discover')}
                  className={cn(
                    'px-3 py-1.5 text-xs border-b-2 -mb-px',
                    subMode === 'discover'
                      ? 'border-brand-600 text-brand-700 font-medium'
                      : 'border-transparent text-ink-dim hover:text-ink',
                  )}
                >
                  Discover existing
                </button>
                <button
                  onClick={() => setSubMode('create')}
                  className={cn(
                    'px-3 py-1.5 text-xs border-b-2 -mb-px',
                    subMode === 'create'
                      ? 'border-brand-600 text-brand-700 font-medium'
                      : 'border-transparent text-ink-dim hover:text-ink',
                  )}
                >
                  Create new
                </button>
              </div>
              {subMode === 'discover' && (
                <div>
                  {discoverLoading ? (
                    <div className="flex items-center gap-2 text-xs text-ink-dim">
                      <Loader2 size={12} className="animate-spin" /> Listing buckets…
                    </div>
                  ) : !discovered ? (
                    <div className="text-[11px] text-ink-mute">Pick a region above to list buckets.</div>
                  ) : discovered.length === 0 ? (
                    <div className="text-[11px] text-ink-mute">No buckets found in this account/region.</div>
                  ) : (
                    <ul className="border border-border rounded-md divide-y divide-border bg-white max-h-48 overflow-y-auto">
                      {discovered.map((b) => (
                        <li
                          key={b.name}
                          onClick={() => setBucketName(b.name)}
                          className={cn(
                            'px-3 py-2 text-sm cursor-pointer hover:bg-slate-50',
                            bucketName === b.name && 'bg-brand-50',
                          )}
                        >
                          <div className="font-mono">{b.name}</div>
                          <div className="text-[10px] text-ink-mute">{b.region} · {b.endpoint}</div>
                        </li>
                      ))}
                    </ul>
                  )}
                </div>
              )}
              {subMode === 'create' && (
                <div className="space-y-2">
                  <label className="text-xs text-ink-mute">New bucket name</label>
                  <input
                    type="text"
                    value={bucketName}
                    onChange={(e) => setBucketName(e.target.value)}
                    placeholder="okesu-fleet-prod"
                    className="w-full text-sm px-2.5 py-1.5 rounded-md ring-1 ring-border bg-white"
                  />
                  <div className="text-[10px] text-ink-mute">
                    AWS/MinIO: 3-63 chars, lowercase, no underscores. OCI: more permissive.
                  </div>
                </div>
              )}
              <div className="space-y-2 pt-2 border-t border-border">
                <label className="text-xs text-ink-mute">Display name</label>
                <input
                  type="text"
                  value={displayName}
                  onChange={(e) => setDisplayName(e.target.value)}
                  placeholder="Prod fleet bucket"
                  className="w-full text-sm px-2.5 py-1.5 rounded-md ring-1 ring-border bg-white"
                />
                <label className="text-xs text-ink-mute">Scanner interval (ms)</label>
                <input
                  type="number"
                  value={scannerIntervalMs}
                  onChange={(e) => setScannerIntervalMs(Number(e.target.value))}
                  className="w-full text-sm px-2.5 py-1.5 rounded-md ring-1 ring-border bg-white"
                />
                <label className="flex items-center gap-2 text-xs">
                  <input type="checkbox" checked={generateFleetKeys} onChange={(e) => setGenerateFleetKeys(e.target.checked)} />
                  Generate fleet keypair
                </label>
              </div>
              <div className="flex justify-between pt-2">
                <button onClick={() => setStep(1)} className="text-xs px-3 py-1.5 border border-border rounded-md hover:bg-slate-50">
                  ← Back
                </button>
                <button
                  onClick={submitConfigured}
                  disabled={submitting}
                  className="text-xs px-3 py-1.5 bg-brand-600 text-white rounded-md hover:bg-brand-700 disabled:opacity-50"
                >
                  {submitting ? 'Provisioning…' : 'Add bucket'}
                </button>
              </div>
            </>
          )}

          {step === 2 && source === 'manual' && (
            <>
              <div className="grid grid-cols-2 gap-3 text-xs">
                <Field label="Display name" value={manual.name} onChange={(v) => setManual({ ...manual, name: v })} />
                <Field label="Bucket" value={manual.bucket} onChange={(v) => setManual({ ...manual, bucket: v })} />
                <Field label="Endpoint" value={manual.endpoint} onChange={(v) => setManual({ ...manual, endpoint: v })} />
                <Field label="Region" value={manual.region ?? ''} onChange={(v) => setManual({ ...manual, region: v })} />
                <Field label="Access key" value={manual.access_key ?? ''} onChange={(v) => setManual({ ...manual, access_key: v })} />
                <Field label="Secret key" type="password" value={manual.secret_key ?? ''} onChange={(v) => setManual({ ...manual, secret_key: v })} />
              </div>
              <label className="flex items-center gap-2 text-xs">
                <input type="checkbox" checked={manual.use_ssl} onChange={(e) => setManual({ ...manual, use_ssl: e.target.checked })} />
                Use SSL
              </label>
              <label className="flex items-center gap-2 text-xs">
                <input type="checkbox" checked={!!manual.generate_fleet_keys} onChange={(e) => setManual({ ...manual, generate_fleet_keys: e.target.checked })} />
                Generate fleet keypair
              </label>
              <div className="flex justify-between pt-2">
                <button onClick={() => setStep(1)} className="text-xs px-3 py-1.5 border border-border rounded-md hover:bg-slate-50">
                  ← Back
                </button>
                <button
                  onClick={submitManual}
                  disabled={submitting}
                  className="text-xs px-3 py-1.5 bg-brand-600 text-white rounded-md hover:bg-brand-700 disabled:opacity-50"
                >
                  {submitting ? 'Saving…' : 'Add bucket'}
                </button>
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  );
}

function Field({ label, value, onChange, type = 'text' }: { label: string; value: string; onChange: (v: string) => void; type?: string }) {
  return (
    <label className="flex flex-col gap-1">
      <span className="text-ink-mute">{label}</span>
      <input
        type={type}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="text-sm px-2.5 py-1.5 rounded-md ring-1 ring-border bg-white"
      />
    </label>
  );
}
```

- [ ] **Step J1.2: Build + commit**

```bash
cd web && npm run build && cd ..
```

```bash
pwd && git status
git add web/src/components/AddBucketWizard.tsx
git commit -m "web(settings/buckets): AddBucketWizard component (configured + manual paths)"
```

---

## Task Group K — Settings/Cloud wiring + edit/delete row actions

### Task K1: Wire wizard + add edit/delete dialogs

**Files:**
- Create: `web/src/components/EditBucketModal.tsx`
- Create: `web/src/components/DeleteBucketDialog.tsx`
- Modify: `web/src/pages/settings/Cloud.tsx`

- [ ] **Step K1.1: Edit modal**

`web/src/components/EditBucketModal.tsx`:

```tsx
import { useState } from 'react';
import { X } from 'lucide-react';

import { api, type TransportConfigSummary } from '../api';

interface Props {
  bucket: TransportConfigSummary;
  onClose: () => void;
  onUpdated: (tc: TransportConfigSummary) => void;
}

export default function EditBucketModal({ bucket, onClose, onUpdated }: Props) {
  const [name, setName] = useState(bucket.name);
  const [scannerIntervalMs, setScannerIntervalMs] = useState(bucket.scanner_interval_ms ?? 30000);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  function submit() {
    setSubmitting(true);
    setError(null);
    api.transportConfigPatch(bucket.id, { name, scanner_interval_ms: scannerIntervalMs })
      .then(onUpdated)
      .catch((e) => setError(String(e)))
      .finally(() => setSubmitting(false));
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <div className="bg-panel border border-border rounded-lg shadow-lg w-[420px]">
        <div className="px-5 py-3 border-b border-border flex items-center justify-between">
          <h2 className="text-sm font-semibold">Edit bucket</h2>
          <button onClick={onClose} className="text-ink-mute hover:text-ink"><X size={16} /></button>
        </div>
        <div className="px-5 py-4 space-y-3">
          {error && (
            <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">{error}</div>
          )}
          <label className="flex flex-col gap-1 text-xs">
            <span className="text-ink-mute">Display name</span>
            <input
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              className="text-sm px-2.5 py-1.5 rounded-md ring-1 ring-border bg-white"
            />
          </label>
          <label className="flex flex-col gap-1 text-xs">
            <span className="text-ink-mute">Scanner interval (ms)</span>
            <input
              type="number"
              value={scannerIntervalMs}
              onChange={(e) => setScannerIntervalMs(Number(e.target.value))}
              className="text-sm px-2.5 py-1.5 rounded-md ring-1 ring-border bg-white"
            />
          </label>
          <div className="text-[10px] text-ink-mute">
            Identity fields (bucket, endpoint, access keys) can't be edited — delete + re-add if you need a different bucket.
            Key rotation lands as a separate "Rotate keys" wizard later.
          </div>
        </div>
        <div className="px-5 py-3 border-t border-border flex justify-end gap-2">
          <button onClick={onClose} className="text-xs px-3 py-1.5 border border-border rounded-md hover:bg-slate-50">Cancel</button>
          <button
            onClick={submit}
            disabled={submitting}
            className="text-xs px-3 py-1.5 bg-brand-600 text-white rounded-md hover:bg-brand-700 disabled:opacity-50"
          >
            {submitting ? 'Saving…' : 'Save'}
          </button>
        </div>
      </div>
    </div>
  );
}
```

- [ ] **Step K1.2: Delete dialog**

`web/src/components/DeleteBucketDialog.tsx`:

```tsx
import { useState } from 'react';
import { X } from 'lucide-react';
import { Link } from 'react-router-dom';

import { api, ApiError, type TransportConfigSummary, type TransportConfigDeleteConflict } from '../api';

interface Props {
  bucket: TransportConfigSummary;
  onClose: () => void;
  onDeleted: () => void;
}

export default function DeleteBucketDialog({ bucket, onClose, onDeleted }: Props) {
  const [submitting, setSubmitting] = useState(false);
  const [conflict, setConflict] = useState<TransportConfigDeleteConflict | null>(null);
  const [error, setError] = useState<string | null>(null);

  function submit() {
    setSubmitting(true);
    setError(null);
    setConflict(null);
    api.transportConfigDelete(bucket.id)
      .then(() => onDeleted())
      .catch((e) => {
        if (e instanceof ApiError && e.status === 409) {
          // Server returns the conflict body as JSON; ApiError stores it
          // in e.body if the helper supports that. Otherwise re-fetch is
          // unnecessary — show e.message and let the operator inspect.
          try {
            const parsed = JSON.parse(e.body) as TransportConfigDeleteConflict;
            setConflict(parsed);
          } catch {
            setError(e.message);
          }
        } else {
          setError(String(e));
        }
      })
      .finally(() => setSubmitting(false));
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <div className="bg-panel border border-border rounded-lg shadow-lg w-[480px]">
        <div className="px-5 py-3 border-b border-border flex items-center justify-between">
          <h2 className="text-sm font-semibold">Delete bucket?</h2>
          <button onClick={onClose} className="text-ink-mute hover:text-ink"><X size={16} /></button>
        </div>
        <div className="px-5 py-4 space-y-3">
          <div className="text-sm">
            This removes the transport configuration for <code className="font-mono text-xs">{bucket.bucket}</code> at{' '}
            <code className="font-mono text-xs">{bucket.endpoint}</code> from Okesu.
            The bucket itself in your cloud account is NOT deleted.
          </div>
          {error && (
            <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">{error}</div>
          )}
          {conflict && (
            <div className="text-xs text-yellow-700 bg-yellow-50 border border-yellow-200 px-3 py-2 rounded-md space-y-2">
              <div className="font-medium">{conflict.message}</div>
              {conflict.referenced_by.Nodes.length > 0 && (
                <div>
                  <div className="font-medium text-[10px] uppercase tracking-wider">Nodes</div>
                  <ul className="list-disc ml-4">
                    {conflict.referenced_by.Nodes.map((r) => (
                      <li key={r.id}><Link to="/nodes" className="text-brand-700 hover:underline">{r.name || `#${r.id}`}</Link></li>
                    ))}
                  </ul>
                </div>
              )}
              {conflict.referenced_by.EnrollmentPackages.length > 0 && (
                <div>
                  <div className="font-medium text-[10px] uppercase tracking-wider">Enrollment packages</div>
                  <ul className="list-disc ml-4">
                    {conflict.referenced_by.EnrollmentPackages.map((r) => (
                      <li key={r.id}><Link to="/federation" className="text-brand-700 hover:underline">{r.name || `#${r.id}`}</Link></li>
                    ))}
                  </ul>
                </div>
              )}
              {conflict.referenced_by.FederationPeers.length > 0 && (
                <div>
                  <div className="font-medium text-[10px] uppercase tracking-wider">Federation peers (will be set to NULL)</div>
                  <ul className="list-disc ml-4">
                    {conflict.referenced_by.FederationPeers.map((r) => (
                      <li key={r.id}>{r.name || `#${r.id}`}</li>
                    ))}
                  </ul>
                </div>
              )}
              {conflict.referenced_by.CPProvisions.length > 0 && (
                <div>
                  <div className="font-medium text-[10px] uppercase tracking-wider">CP provisions (will be set to NULL)</div>
                  <ul className="list-disc ml-4">
                    {conflict.referenced_by.CPProvisions.map((r) => (
                      <li key={r.id}>{r.name || `#${r.id}`}</li>
                    ))}
                  </ul>
                </div>
              )}
            </div>
          )}
        </div>
        <div className="px-5 py-3 border-t border-border flex justify-end gap-2">
          <button onClick={onClose} className="text-xs px-3 py-1.5 border border-border rounded-md hover:bg-slate-50">Cancel</button>
          <button
            onClick={submit}
            disabled={submitting}
            className="text-xs px-3 py-1.5 bg-red-600 text-white rounded-md hover:bg-red-700 disabled:opacity-50"
          >
            {submitting ? 'Deleting…' : 'Delete'}
          </button>
        </div>
      </div>
    </div>
  );
}
```

**Note on `ApiError.body`:** if the existing `ApiError` class doesn't expose the response body (it might just store the message), check `web/src/api.ts` and either extend it (preferred — add a `body: string` field populated from `await res.text()`) or fall back to a separate `fetch` in this dialog. If extending, do it carefully — many other consumers throw and catch `ApiError`. The cheapest extension: add `body: string` (default `""`), populate at error time, leave `message` as-is.

- [ ] **Step K1.3: Wire BucketsSection in Cloud.tsx**

In `web/src/pages/settings/Cloud.tsx`, find the existing `BucketsSection` function. Replace the broken `<Link to="/nodes">` with state-driven modals:

```tsx
import AddBucketWizard from '../../components/AddBucketWizard';
import EditBucketModal from '../../components/EditBucketModal';
import DeleteBucketDialog from '../../components/DeleteBucketDialog';
```

Inside `BucketsSection`:

```tsx
const [items, setItems] = useState<TransportConfigSummary[] | null>(null);
const [error, setError] = useState<string | null>(null);
const [showAdd, setShowAdd] = useState(false);
const [editing, setEditing] = useState<TransportConfigSummary | null>(null);
const [deleting, setDeleting] = useState<TransportConfigSummary | null>(null);

const reload = () => {
  setError(null);
  api.transportConfigs()
    .then(setItems)
    .catch((e) => setError(String(e)));
};

useEffect(() => { reload(); }, []);
```

Replace the existing `<Link to="/nodes">` "Add bucket" button:

```tsx
<button
  onClick={() => setShowAdd(true)}
  className="text-xs px-2 py-1 bg-brand-50 hover:bg-brand-100 text-brand-700 rounded inline-flex items-center gap-1"
>
  <Plus size={12} /> Add bucket
</button>
```

Per-row, replace the existing "Manage → /nodes" link with Edit + Delete buttons (keep the Manage link for now alongside them, OR drop it — see spec out-of-scope. For v1: keep Manage link, add Edit + Delete next to it):

```tsx
<button onClick={() => setEditing(tc)} className="text-xs px-2 py-1 border border-border hover:bg-slate-50 rounded">
  Edit
</button>
<button onClick={() => setDeleting(tc)} className="text-xs px-2 py-1 border border-red-200 text-red-700 hover:bg-red-50 rounded">
  Delete
</button>
```

Render the modals at the bottom of `BucketsSection`'s JSX:

```tsx
{showAdd && (
  <AddBucketWizard
    onClose={() => setShowAdd(false)}
    onCreated={(tc) => {
      setShowAdd(false);
      reload();
    }}
  />
)}
{editing && (
  <EditBucketModal
    bucket={editing}
    onClose={() => setEditing(null)}
    onUpdated={() => {
      setEditing(null);
      reload();
    }}
  />
)}
{deleting && (
  <DeleteBucketDialog
    bucket={deleting}
    onClose={() => setDeleting(null)}
    onDeleted={() => {
      setDeleting(null);
      reload();
    }}
  />
)}
```

- [ ] **Step K1.4: Build + commit**

```bash
cd web && npm run build && cd ..
```

```bash
pwd && git status
git add web/src/components/EditBucketModal.tsx \
        web/src/components/DeleteBucketDialog.tsx \
        web/src/pages/settings/Cloud.tsx
git commit -m "web(settings/cloud): wire AddBucketWizard + edit + delete row actions"
```

---

## Task Group L — Add Provider modal: MinIO branch

### Task L1: MinIO option in the existing Add Provider form

**Files:**
- Modify: `web/src/pages/settings/Cloud.tsx` (the Add Provider modal logic)

- [ ] **Step L1.1: Find the Add Provider modal**

Search for the existing form that creates a `cloud_credential`:

```bash
grep -n "cloudCredentialCreate\|AddProvider\|cloud.*credentials\|cloud_credential" web/src/pages/settings/Cloud.tsx
```

If the form has a switch on `cloud` value with branches for `oci`, `aws`, `gcp`, etc., add a `minio` branch.

- [ ] **Step L1.2: Add MinIO field set**

Find the existing `oci` or `aws` branch and add a sibling `minio` branch with these fields:

```tsx
{kind === 'minio' && (
  <>
    <Field label="Endpoint" value={form.endpoint ?? ''} onChange={(v) => setForm({ ...form, endpoint: v })} />
    <Field label="Access key" value={form.access_key_id ?? ''} onChange={(v) => setForm({ ...form, access_key_id: v })} />
    <Field label="Secret key" type="password" value={form.secret_access_key ?? ''} onChange={(v) => setForm({ ...form, secret_access_key: v })} />
  </>
)}
```

The exact `form` shape and field names will match what the existing AWS/OCI branches use; copy that pattern. If the form uses a single JSON-payload textarea instead of typed fields per cloud, fall back to the same pattern (operators paste a JSON blob).

Add `'minio'` to the cloud-kind dropdown list:

```tsx
<option value="minio">MinIO (S3-compatible)</option>
```

- [ ] **Step L1.3: Build + commit**

```bash
cd web && npm run build && cd ..
```

```bash
pwd && git status
git add web/src/pages/settings/Cloud.tsx
git commit -m "web(settings/cloud): MinIO option in Add Provider form"
```

---

## Task Group Z — Docs + final test sweep + PR body

### Task Z1: Architecture doc

**Files:**
- Modify: `docs/architecture.md`

- [ ] **Step Z1.1: Append a section**

Append after the existing Catalog Federation / latest section:

```markdown
## Bucket provisioning native to Settings

Operators add object-storage buckets directly from `Settings → Cloud → Object storage buckets` via a two-step wizard (`web/src/components/AddBucketWizard.tsx`), mirroring the Add-CP flow.

The wizard offers two paths:
- **Configured cloud provider** — pick an AWS / OCI / MinIO `cloud_credential`, then either discover existing buckets or create a new one. Backend dispatches via the new `BucketProvisioner` interface (`controlplane/cpprovision/bucket_provisioner.go`) to per-cloud implementations.
- **Manual entry** — preserved for on-prem / third-party S3-compatible services without an Okesu credential. Same shape as the legacy form.

OCI's S3-compatible endpoint requires Customer Secret Keys, which the OCI provisioner auto-creates via the IAM SDK. MinIO is treated as S3-compatible and reuses the AWS S3 SDK with a custom endpoint pulled from the credential.

`PATCH /api/transport-configs/{id}` permits partial updates of `name` and `scanner_interval_ms`. Identity fields (bucket, endpoint, access keys, region) are immutable — operators delete + re-add for identity changes. `DELETE` returns `409 Conflict` with a referencing-resources list if any node or enrollment_package references the row; federation_peers and cp_provisions FKs are `ON DELETE SET NULL` and don't block.
```

Commit:

```bash
git add docs/architecture.md
git commit -m "docs(architecture): bucket provisioning native to settings"
```

### Task Z2: Full test sweep (verification only)

```bash
go test ./... 2>&1 | tail -50
cd web && npm run build && cd ..
```

Both must pass. Pre-existing UI dist embed false positive only fires if `controlplane/ui/dist` doesn't exist — running `npm run build` first writes it.

### Task Z3: PR body

**Create:** `docs/superpowers/plans/2026-04-30-bucket-provisioning-settings-pr-body.md`:

```markdown
## Summary

Native Settings UI for adding, editing, and deleting object-storage
buckets. Replaces the broken "Add bucket" link in
`Settings → Cloud → Object storage buckets` (which navigated to
`/nodes`) with a dedicated wizard that uses configured cloud
credentials + the cloud SDK to discover existing or create new
buckets — same UX pattern as Add-CP.

- New `BucketProvisioner` interface in `controlplane/cpprovision/`
  with implementations for AWS (S3 SDK), OCI (ObjectStorage +
  Customer Secret Keys via IAM), and MinIO (S3 SDK with custom
  endpoint).
- `"minio"` added to `db.AllowedCloudKinds`; Add Provider form gets
  a MinIO branch.
- New endpoints: `GET /api/buckets/cloud-providers`,
  `GET /api/buckets/discover`, `POST /api/buckets/provision`.
- New `PATCH /api/transport-configs/{id}` with safe partial-update
  semantics (name + scanner_interval_ms only). Identity fields stay
  immutable.
- Existing `DELETE /api/transport-configs/{id}` tightened with an
  in-use guard returning 409 + a referencing-resources list when
  nodes or enrollment_packages reference the row.
- Frontend: `AddBucketWizard`, `EditBucketModal`, `DeleteBucketDialog`
  components. Settings → Cloud bucket section wires them up; Add
  Provider form gains a MinIO branch.

Build matrix unchanged: `CGO_ENABLED=0` everywhere.

## Test plan

Lab smoke (post-merge):

- [ ] `go test ./...` passes
- [ ] `npm run build` clean in `web/`
- [ ] Configure an AWS credential. Open Settings → Cloud → Add bucket.
      Pick AWS → Discover existing → pick a bucket → success: row
      appears in the list with the chosen bucket.
- [ ] Configure an OCI credential. Add bucket → OCI → Create new →
      enter name → success: bucket created in OCI tenancy with
      Customer Secret Keys auto-generated.
- [ ] Add a MinIO provider (Add Provider form, new MinIO branch).
      Add bucket → MinIO → Discover.
- [ ] Edit a bucket: change display name → save. Verify list reflects.
- [ ] Delete a bucket referenced by a node: 409 dialog shows the node
      with a deep-link.
- [ ] Delete an unreferenced bucket: confirms and removes.

## Files

- New backend: `controlplane/cpprovision/bucket_provisioner.go`,
  `controlplane/cpprovision/aws/bucket.go`,
  `controlplane/cpprovision/oci/bucket.go`,
  `controlplane/cpprovision/minio/bucket.go`,
  `controlplane/api/buckets.go`.
- New endpoints: `GET /api/buckets/cloud-providers`,
  `GET /api/buckets/discover`,
  `POST /api/buckets/provision`,
  `PATCH /api/transport-configs/{id}` (DELETE existing endpoint
  tightened in place).
- New frontend: `web/src/components/AddBucketWizard.tsx`,
  `web/src/components/EditBucketModal.tsx`,
  `web/src/components/DeleteBucketDialog.tsx`.

## Spec / plan

- Spec: `docs/superpowers/specs/2026-04-30-bucket-provisioning-settings-design.md`
- Plan: `docs/superpowers/plans/2026-04-30-bucket-provisioning-settings.md`

## Open follow-ups

- GCP / Azure / DigitalOcean BucketProvisioner implementations
- Dedicated "Manage bucket" page in Settings (replaces deep-link to /nodes)
- Bucket-level metrics in the Settings list (last-write timestamp, object count)
- "Rotate keys" wizard with explicit re-enrollment guidance
- MinIO embedded provisioning (run MinIO as a Docker sidecar on the parent host)
```

Commit:

```bash
git add docs/superpowers/plans/2026-04-30-bucket-provisioning-settings-pr-body.md
git commit -m "docs: bucket provisioning settings PR body"
```

### Task Z4: DO NOT push or open the PR

The controller (the human operator) handles `git push` and `gh pr create`.

---

## Self-review checklist

- [ ] Spec coverage:
  - **Bug fix** (broken Add bucket link) → Group K1.3
  - **Configured-provider wizard with discover + create tabs** → Group J1
  - **Manual-entry path preserved** → Group J1 (manual branch)
  - **AWS / OCI / MinIO BucketProvisioner implementations** → Groups C, D, E
  - **MinIO as cloud kind** → Groups A1, L1
  - **Edit + delete row actions** → Group K (modals) + Group G (backend)
  - **In-use 409 guard on DELETE** → Group G1
- [ ] No build matrix changes — all packages still build with `CGO_ENABLED=0`
- [ ] No placeholders in any task; every step shows literal code/command
- [ ] Backend tests cover: registry, credential decode, handler 200/404/409 paths, PATCH ignores identity fields
- [ ] Frontend builds cleanly after each group's commits
- [ ] Legacy `PUT /api/transport-configs/{id}` route stays mounted untouched (backwards compat)
