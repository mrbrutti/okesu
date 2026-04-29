// Package s3transport implements the bucket-based "dead-drop"
// transport for nodes that can't reach the CP directly.
//
// See docs/s3-transport.md for the full wire-format spec. This file
// holds the Go types that match the JSON schemas — both ends import
// these so a schema change is a single PR rather than two.
//
// All keys, intervals, and field names below are part of the wire
// contract — change them only via a documented protocol bump.

package s3transport

import "time"

// Object key prefixes — the full layout is at docs/s3-transport.md.
const (
	// PrefixCPRoot is the per-CP namespace under the bucket root.
	// Substituted with the CP id at runtime via CPPrefix().
	PrefixCPRoot = "cp/%s/"

	// PathEnrollmentPubkey holds the fleet trust anchor — a public
	// key the CP publishes once. Packages embed this so any signed
	// content can be cross-checked offline.
	PathEnrollmentPubkey = "enrollment/pubkey.pem"

	// PathRegistrationDir is the inbox where new nodes write their
	// registration request. CP scanner sweeps this prefix.
	PathRegistrationDir = "registration/"

	// PathRegistrationResultDir holds CP responses keyed by the same
	// node-uuid the request used. Carries the issued node_id.
	PathRegistrationResultDir = "registration-result/"

	// SubpathNodes is the per-node tree under each CP. Substituted
	// with the integer node id.
	SubpathNodes = "nodes/%d/"

	// Per-node files / sub-prefixes. Joined with the resolved
	// SubpathNodes prefix.
	FileNodeCert       = "cert.pem"
	FileHeartbeat      = "heartbeat.json"
	DirJobsInbox       = "jobs/inbox/"
	DirJobs            = "jobs/"
	DirEvents          = "events/"
	DirFindings        = "findings/"
	FileDesiredConfig  = "config/desired.json"
)

// CPPrefix returns "cp/<id>/" — bucket-relative prefix the CP owns.
func CPPrefix(cpID string) string {
	return "cp/" + cpID + "/"
}

// NodePrefix returns the per-node prefix under a CP.
func NodePrefix(cpID string, nodeID int64) string {
	return CPPrefix(cpID) + nodePrefixOnly(nodeID)
}

func nodePrefixOnly(nodeID int64) string {
	// Bypassing fmt.Sprintf here because this lives in the hot path —
	// the agent rebuilds the prefix on every poll. itoa is cheap.
	return "nodes/" + itoa(nodeID) + "/"
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// ───────────────────────── Registration ─────────────────────────

// RegistrationRequest is what a fresh node writes to
// registration/<node-uuid>.json. All fields except `signature` are
// hashed (canonical JSON) and signed with the package signing key.
type RegistrationRequest struct {
	NodeUUID        string    `json:"node_uuid"`
	Hostname        string    `json:"hostname"`
	OS              string    `json:"os"`
	Arch            string    `json:"arch"`
	CSRPEM          string    `json:"csr_pem"`
	PackageID       int64     `json:"package_id"`
	AgentsRequested []string  `json:"agents_requested,omitempty"`
	SignedAt        time.Time `json:"signed_at"`
	// Signature carries the base64 of an ECDSA-SHA256 signature over
	// the canonical-JSON of the rest of this struct (signature field
	// omitted). Verified by the CP scanner against the package signing
	// cert (which itself is signed by the CP fleet key).
	Signature string `json:"signature"`
	// PackageCertPEM is the package signing cert. Embedded inline so
	// the scanner can verify the signature without a separate lookup;
	// the cert itself is then re-verified against the fleet key.
	PackageCertPEM string `json:"package_cert_pem"`
}

// RegistrationResult is what the CP writes to
// registration-result/<node-uuid>.json once enrollment succeeds.
type RegistrationResult struct {
	NodeUUID  string    `json:"node_uuid"`
	NodeID    int64     `json:"node_id"`
	CertPath  string    `json:"cert_path"`            // bucket-relative; node fetches the PEM from there
	IssuedAt  time.Time `json:"issued_at"`
	Error     string    `json:"error,omitempty"`      // populated when CP rejected the request
}

// ───────────────────────── Heartbeat / config ─────────────────────────

// Heartbeat is overwritten on every poll cycle.
type Heartbeat struct {
	NodeID        int64     `json:"node_id"`
	TS            time.Time `json:"ts"`
	TunnelRunning bool      `json:"tunnel_running"`
	Version       string    `json:"version,omitempty"`
}

// DesiredConfig is what the CP writes to config/desired.json. The
// node hot-applies on every version bump.
type DesiredConfig struct {
	Version int `json:"version"`

	PollIntervalMs        int      `json:"poll_interval_ms,omitempty"`
	HeartbeatIntervalMs   int      `json:"heartbeat_interval_ms,omitempty"`
	ConfigPollIntervalMs  int      `json:"config_poll_interval_ms,omitempty"`
	EventsFlushIntervalMs int      `json:"events_flush_interval_ms,omitempty"`
	EventsFlushMax        int      `json:"events_flush_max,omitempty"`
	OutputFlushIntervalMs int      `json:"output_flush_interval_ms,omitempty"`
	OutputFlushMaxBytes   int      `json:"output_flush_max_bytes,omitempty"`
	Agents                []string `json:"agents,omitempty"`
}

// ───────────────────────── Job claim / exit ─────────────────────────

// ClaimMarker is the small object a node writes to atomically take
// ownership of a queued job. The S3 If-None-Match precondition makes
// the write fail when another caller (rare, but possible across
// concurrent scanner cycles) raced to claim first.
type ClaimMarker struct {
	JobID     string    `json:"job_id"`
	NodeID    int64     `json:"node_id"`
	ClaimedAt time.Time `json:"claimed_at"`
}

// PackageDefaults is stored at package-generation time and travels
// with each enrolled node's first registration request — the CP
// uses it to seed initial config and spawn the requested daimons.
//
// The same struct also matches enrollment_packages.defaults_json on
// the CP side, so it round-trips cleanly.
type PackageDefaults struct {
	Agents              []string `json:"agents,omitempty"`
	PollIntervalMs      int      `json:"poll_interval_ms,omitempty"`
	HeartbeatIntervalMs int      `json:"heartbeat_interval_ms,omitempty"`
	// CPID names which CP scope under the bucket the package targets.
	CPID string `json:"cp_id,omitempty"`
}
