// Package cpprovision drives "+ Add CP → Managed deploy".
//
// One Provisioner per cloud (oci, aws, gcp, azure, digitalocean) is
// registered at server boot. The Coordinator picks the right one off
// the registry when an operator submits a managed-deploy request,
// then walks the cp_provisions state machine end-to-end:
//
//   1. Decrypt the cloud_credentials row.
//   2. Mint a bootstrap token + render a cloud-init script that
//      pulls the parent's CP bundle and runs `docker compose up -d`.
//   3. Provisioner.Launch() — cloud-specific API call to create a VM
//      with that cloud-init in user_data. Returns instance_id +
//      console URL for the operator to click into.
//   4. Wait for the new CP to call /api/v1/cp/bootstrap with the
//      token. Bootstrap handler stamps peer_id on the provision row,
//      which the worker observes and flips status → ready.
//
// Phase 21.3a (this PR) ships the framework + cloud-init renderer +
// the worker loop. Phase 21.3b adds the OCI implementation; 21.3c
// adds AWS. Until at least one provisioner registers, the
// /api/federation/cp-provision endpoint returns a 501 with a clear
// "no provisioners registered" message so the UI can show a useful
// fallback.

package cpprovision

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Provisioner is implemented per cloud. Launch is the only required
// method — the worker handles the bootstrap-token + state-machine
// scaffolding in one place.
type Provisioner interface {
	// Cloud is the lowercase cloud kind ("oci", "aws", ...). Must
	// match db.AllowedCloudKinds and cloud_credentials.cloud values.
	Cloud() string

	// Launch creates a VM in the operator's cloud account,
	// configures it with the supplied cloud-init script, and returns
	// the cloud-side instance handle + a console URL the operator
	// can click into. Long-running: the call blocks until the
	// instance is at least in "running" state.
	//
	// Errors are surfaced verbatim into the cp_provisions.error
	// column — make them actionable ("subnet 'xxx' not found in
	// region us-ashburn-1" beats "operation timed out").
	Launch(ctx context.Context, req LaunchRequest, log Logger) (*LaunchResult, error)

	// Destroy tears down a previously-launched instance. Used by
	// the operator's "Destroy" button + cleanup-on-failure paths.
	// resourceID is whatever Launch returned in LaunchResult.ResourceID.
	Destroy(ctx context.Context, resourceID string, region string, credentialPayload []byte) error
}

// LaunchRequest carries everything Provisioner.Launch needs that's
// generic across clouds. Cloud-specific knobs (subnet OCID, AMI id,
// shape, ...) ride along in CloudParams as a free-form map decoded
// per implementation.
type LaunchRequest struct {
	DisplayName       string
	Region            string
	CredentialPayload []byte         // decrypted JSON from cloud_credentials
	CloudParams       map[string]any // per-cloud knobs from the +Add CP modal
	CloudInitScript   string         // rendered by the worker; opaque to the provisioner
}

// LaunchResult is what Provisioner.Launch hands back when the VM is
// up. ResourceID becomes cp_provisions.cloud_resource_id; ConsoleURL
// becomes cloud_resource_url. PublicIP is informational only — the
// child CP calls back to the parent, not the other way around, so we
// don't strictly need it.
type LaunchResult struct {
	ResourceID string
	ConsoleURL string
	PublicIP   string
}

// Logger is the minimal interface Provisioner implementations use to
// append progress lines to the operator-visible job log. The
// coordinator hands one in per-call wired to db.AppendCPProvisionLog.
type Logger interface {
	Logf(format string, args ...any)
}

// Registry tracks which clouds have a Provisioner installed. Built
// once at server boot from the active build's per-cloud packages.
//
// The interface is deliberately tiny: register at boot, look up by
// cloud kind. No deregister — provisioners live for the process
// lifetime.
type Registry struct {
	mu  sync.RWMutex
	all map[string]Provisioner
}

func NewRegistry() *Registry { return &Registry{all: map[string]Provisioner{}} }

// Register installs a Provisioner. Panics on duplicate registration —
// same-cloud collisions are programming errors, not config issues.
func (r *Registry) Register(p Provisioner) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cloud := p.Cloud()
	if _, dup := r.all[cloud]; dup {
		panic(fmt.Sprintf("cpprovision: duplicate registration for cloud %q", cloud))
	}
	r.all[cloud] = p
}

// Get returns the Provisioner for a cloud, or ErrNoProvisioner if
// none is registered (Phase 21.3a expected state — the UI surfaces
// "managed deploy not yet wired for X").
func (r *Registry) Get(cloud string) (Provisioner, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.all[cloud]
	if !ok {
		return nil, fmt.Errorf("%w (cloud %q)", ErrNoProvisioner, cloud)
	}
	return p, nil
}

// Clouds returns the cloud kinds with a registered provisioner,
// sorted ascending. Used by the UI to gate the "Managed deploy" tab —
// only show the cloud picker for entries that can actually be
// provisioned today.
func (r *Registry) Clouds() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.all))
	for k := range r.all {
		out = append(out, k)
	}
	// Simple insertion sort to keep imports flat.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// ErrNoProvisioner is the sentinel Get returns when no Provisioner
// is registered for the requested cloud. Callers (the API handler)
// translate this to a 501 with a helpful body.
var ErrNoProvisioner = errors.New("no provisioner registered for cloud")
