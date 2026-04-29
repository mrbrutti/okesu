// AutoDeployer concrete implementation backed by the CP's
// fleet-level SSH credential. Triggered by the orchestrator's
// routingDispatcher when a step targets a node with no tunnel and
// no recent jobs-runtime poll.
//
// Invariant: this only runs when the CP operator explicitly
// configured --fleet-ssh-key-path. Without that, the dispatcher's
// AutoDeployer is nil and operators get a "manual install required"
// error pointing at the Nodes UI instead of an unauthorised SSH
// attempt.

package api

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/sshdeploy"
)

// FleetAutoDeployer installs the jobs runtime on a node using a
// shared SSH private key the CP loaded at startup. Per-node locks
// prevent multiple in-flight installs on the same target — a common
// case when several orchestration steps fan out to the same fresh
// host simultaneously.
type FleetAutoDeployer struct {
	store    *db.Store
	issuer   CertIssuer
	sshKey   []byte // PEM-encoded private key, loaded once at boot
	mgmtURL  string
	binPath  string
	resolver sshdeploy.DaemonBinaryResolver

	// API keys forwarded to the runtime via /etc/okesu/jobs.env so
	// spawned `okesu claude` jobs can authenticate. Empty disables.
	anthropicKey string
	openaiKey    string

	// per-node locks so concurrent dispatches against the same
	// fresh host don't trigger N parallel SSH installs.
	locks sync.Map // nodeName → *sync.Mutex
}

// NewFleetAutoDeployer builds the deployer. Returns nil + err when
// the SSH key file can't be read; callers treat that as "auto-deploy
// disabled" and fall back to the manual install path.
func NewFleetAutoDeployer(
	store *db.Store,
	issuer CertIssuer,
	sshKeyPath string,
	mgmtURL string,
	binPath string,
	resolver sshdeploy.DaemonBinaryResolver,
	anthropicKey, openaiKey string,
) (*FleetAutoDeployer, error) {
	if sshKeyPath == "" {
		return nil, nil
	}
	key, err := os.ReadFile(sshKeyPath)
	if err != nil {
		return nil, fmt.Errorf("read fleet ssh key %q: %w", sshKeyPath, err)
	}
	return &FleetAutoDeployer{
		store:        store,
		issuer:       issuer,
		sshKey:       key,
		mgmtURL:      mgmtURL,
		binPath:      binPath,
		resolver:     resolver,
		anthropicKey: anthropicKey,
		openaiKey:    openaiKey,
	}, nil
}

// AutoDeploy installs the jobs runtime on the named node and waits
// up to 120s (the timeout we agreed in the design discussion) for
// the runtime to start polling. The orchestrator's routingDispatcher
// re-checks freshness on return.
func (a *FleetAutoDeployer) AutoDeploy(ctx context.Context, nodeName string) error {
	// Per-node serialisation. If a sibling step is already
	// auto-deploying this node, wait for them to finish; the second
	// call's freshness probe will see the runtime alive and skip
	// the re-install.
	mu := a.lockFor(nodeName)
	mu.Lock()
	defer mu.Unlock()

	// If the freshness check already passes (sibling install
	// completed while we were waiting on the lock), fast-path out.
	if jobsRuntimeFreshOnThisCP(a.store, nodeName) {
		return nil
	}

	n, err := a.store.NodeByID(0) // dummy — actual lookup follows
	_ = n
	row := a.store.QueryRow(`SELECT id, name, hostname, ssh_user, ssh_port FROM nodes WHERE name = ?`, nodeName)
	var (
		id             int64
		name, hostname string
		sshUser        string
		sshPort        int
	)
	if err := row.Scan(&id, &name, &hostname, &sshUser, &sshPort); err != nil {
		return fmt.Errorf("node row %q not found: %w", nodeName, err)
	}
	if hostname == "" {
		return fmt.Errorf("node %q has no SSH hostname recorded; cannot auto-deploy", nodeName)
	}
	if sshUser == "" || sshPort == 0 {
		return fmt.Errorf("node %q is missing SSH user/port; cannot auto-deploy", nodeName)
	}

	clientCert, clientKey, caCert, err := a.issuer.IssueClientCert(name)
	if err != nil {
		return fmt.Errorf("issue cert: %w", err)
	}

	ireq := sshdeploy.InstallJobsRequest{
		Cred: sshdeploy.Credential{
			User:       sshUser,
			Host:       hostname,
			Port:       sshPort,
			PrivateKey: a.sshKey,
		},
		NodeName:             name,
		CPMgmtURL:            a.mgmtURL,
		DaemonBinaryResolver: a.resolver,
		DaemonBinaryPath:     a.binPath,
		ClientCertPEM:        clientCert,
		ClientKeyPEM:         clientKey,
		CACertPEM:            caCert,
		AnthropicAPIKey:      a.anthropicKey,
		OpenAIAPIKey:         a.openaiKey,
	}
	installCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	if _, err := sshdeploy.InstallJobsRuntime(installCtx, ireq, nil); err != nil {
		return fmt.Errorf("install jobs runtime: %w", err)
	}

	// Wait for the runtime to register a poll. Up to 30s after
	// install — the systemd unit should start within ~5s on a
	// healthy host.
	deadline := time.Now().Add(30 * time.Second)
	t := time.NewTicker(1 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
		if jobsRuntimeFreshOnThisCP(a.store, nodeName) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("jobs runtime installed but didn't poll within 30s")
		}
	}
}

func (a *FleetAutoDeployer) lockFor(nodeName string) *sync.Mutex {
	v, _ := a.locks.LoadOrStore(nodeName, &sync.Mutex{})
	return v.(*sync.Mutex)
}
