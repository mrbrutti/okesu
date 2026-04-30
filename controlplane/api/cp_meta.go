package api

import (
	"encoding/json"
	"net/http"
	"runtime"
	"sort"

	"github.com/section9labs/okesu/controlplane/db"
)

// IntrospectResponse is what GET /api/v1/cp/introspect returns. A
// parent CP polls this on each registered child to keep an aggregate
// view fresh — the response is intentionally compact (counts only, no
// row-level data) so a parent can poll dozens of children frequently
// without copying full event tables.
//
// The shape is also versioned implicitly via the Version + GoVersion
// fields. Future phases that reshape the payload should add new fields
// alongside (never remove or rename) so older parent CPs keep working.
type IntrospectResponse struct {
	// Identity (cp_meta).
	InstanceID  string `json:"instance_id"`
	Region      string `json:"region,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Role        string `json:"role"`

	// Build identity — same fields the local /api/system/about returns
	// so a parent CP can detect drift between federated members.
	Version       string `json:"version"`
	DaemonVersion string `json:"daemon_version,omitempty"`
	GoVersion     string `json:"go_version"`
	OS            string `json:"os"`
	Arch          string `json:"arch"`

	// Capabilities — what surfaces this CP serves. A parent can adapt
	// (e.g., skip an SSE merge for a child that doesn't have it).
	Features AboutFeatures `json:"features"`

	// Aggregates — small enough to fit on one row in the parent UI.
	// More detailed federated views (e.g. drill into an individual
	// child's findings) belong on a separate, paginated endpoint
	// scoped to the federation context — not on this poll.
	Counts IntrospectCounts `json:"counts"`

	// OS distribution — same shape as the local /api/dashboard
	// returns. The parent merges these per-OS counts across all
	// children so its OS chart shows the federated breakdown rather
	// than its own (typically empty) local nodes.
	OSDistribution []osBucket `json:"os_distribution"`

	// Reachability — public URLs the parent can present to operators
	// or hand to a federated client. Filled in only when the operator
	// has set them via --webhook-public-url / --mgmt-public-url; an
	// empty value means "not exposed for federation."
	WebhookPublicURL string `json:"webhook_public_url,omitempty"`
	MgmtPublicURL    string `json:"mgmt_public_url,omitempty"`
}

// IntrospectCounts summarises the live state of this CP. Counts only —
// row-level data crosses a separate, paginated boundary so federation
// polling stays cheap.
type IntrospectCounts struct {
	Daimons       int   `json:"daimons"`
	DaimonsHealthy int  `json:"daimons_healthy"`
	Nodes         int   `json:"nodes"`
	OpenFindings  int64 `json:"open_findings"`
}

// CPIntrospect handles GET /api/v1/cp/introspect.
//
// Auth: a parent CP authenticates with the shared federation token via
// X-Okesu-Federation-Token header. Empty stored hash (federation-token
// not configured on this CP) means we reject every unauthenticated
// caller — operators who want to test from the local UI should use the
// session-authenticated /api/about + /api/dashboard instead, or set
// the federation token to enable this endpoint.
//
// The session-auth path is intentionally NOT mixed in here: introspect
// is for cross-CP traffic. Mixing the two would invite a parent CP to
// scrape introspect with a stolen session cookie. Keeping this endpoint
// strictly token-authed simplifies the threat model and the audit log.
// CPIntrospectDepsValue is a passthrough wiring struct — exported so
// the controlplane package can construct it without duplicating field
// names. Kept separate from the response so future deps can be added
// without churning the wire format.
type CPIntrospectDepsValue struct {
	Store           *db.Store
	Version         string
	DaemonVersionFn func() string
	Features        AboutFeatures
	WebhookURL      string
	MgmtURL         string
}

func CPIntrospect(deps CPIntrospectDepsValue) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		meta, err := deps.Store.CPMeta()
		if err != nil {
			http.Error(w, "cp_meta unavailable", http.StatusInternalServerError)
			return
		}
		// Reject when federation isn't configured. The 401 vs 403
		// distinction matters: 401 tells the parent "you didn't send
		// credentials" (or you sent wrong ones); 403 would imply
		// "you're authenticated but can't see this resource." We mean
		// the former.
		presented := r.Header.Get("X-Okesu-Federation-Token")
		if !meta.VerifyFederationToken(presented) {
			w.Header().Set("WWW-Authenticate", `Federation realm="okesu-cp"`)
			http.Error(w, "federation token invalid or not configured", http.StatusUnauthorized)
			return
		}

		out := BuildIntrospectResponse(deps)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// BuildIntrospectResponse computes the same shape CPIntrospect emits
// over HTTP, factored out so the federation S3 publisher can use it
// to write the bucket-side manifest. The auth/token check stays in
// CPIntrospect — the publisher's auth is "operator-issued bucket
// credentials," same as the existing s3 dead-drop transport.
func BuildIntrospectResponse(deps CPIntrospectDepsValue) IntrospectResponse {
	meta, err := deps.Store.CPMeta()
	if err != nil {
		// Returning a partial response is better than nothing — the
		// publisher's caller logs and retries on next tick.
		return IntrospectResponse{}
	}

	// Tally aggregates. ListAgents/ListNodes return up to (limit,offset)
	// — pass a large limit; the federation contract caps each CP at
	// a reasonable size where we'd prefer a full count anyway.
	agents, _ := deps.Store.ListAgents(10_000, 0)
	var healthy int
	for _, a := range agents {
		if a.LastHeartbeatAt.Valid {
			healthy++
		}
	}
	nodes, _ := deps.Store.ListNodes(10_000, 0)
	var openFindings int64
	if fs, ferr := deps.Store.FindingsSummary(); ferr == nil {
		openFindings = fs.Open
	}

	// Per-OS counts using the same classifier the local dashboard uses.
	osCounts := map[string]int{}
	for _, n := range nodes {
		os := classifyOS(n.OSRelease.String, n.Name)
		osCounts[os]++
	}
	osDist := make([]osBucket, 0, len(osCounts))
	for os, n := range osCounts {
		osDist = append(osDist, osBucket{OS: os, Count: n})
	}
	sort.SliceStable(osDist, func(i, j int) bool {
		if osDist[i].Count != osDist[j].Count {
			return osDist[i].Count > osDist[j].Count
		}
		return osDist[i].OS < osDist[j].OS
	})

	dv := ""
	if deps.DaemonVersionFn != nil {
		dv = deps.DaemonVersionFn()
	}

	return IntrospectResponse{
		InstanceID:    meta.InstanceID,
		Region:        meta.Region,
		DisplayName:   meta.DisplayName,
		Role:          meta.Role,
		Version:       deps.Version,
		DaemonVersion: dv,
		GoVersion:     runtime.Version(),
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		Features:      deps.Features,
		Counts: IntrospectCounts{
			Daimons:        len(agents),
			DaimonsHealthy: healthy,
			Nodes:          len(nodes),
			OpenFindings:   openFindings,
		},
		OSDistribution:   osDist,
		WebhookPublicURL: deps.WebhookURL,
		MgmtPublicURL:    deps.MgmtURL,
	}
}
