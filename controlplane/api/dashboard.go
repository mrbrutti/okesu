package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/ports"
	"github.com/section9labs/okesu/controlplane/tunnel"
)

// dashboardResponse is the single payload that backs the operator
// dashboard at /. One round-trip avoids a flood of parallel fetches
// on every refresh; the page polls this endpoint every ~15s. The
// timeline charts (events / findings) drive their own /api/insights/*
// endpoints because they have their own range pickers.
type dashboardResponse struct {
	Daimons       dashDaimons        `json:"daimons"`
	Nodes         dashNodes          `json:"nodes"`
	Tunnels       dashTunnels        `json:"tunnels"`
	Findings      dashFindings       `json:"findings"`
	Drift         dashDrift          `json:"drift"`
	TopHosts      []hostFindingCount `json:"top_hosts"`        // open findings by host (top 10)
	OSDistribution []osBucket        `json:"os_distribution"`  // nodes by OS family
	FleetStatus   fleetStatus        `json:"fleet_status"`     // healthy / needs_patching / offline / frozen

	// Phase 22.7 — investigations rollup. Active case count, recent
	// closure rate, autolink activity, and the top-5 most-recently-
	// updated active cases for the "Recent investigations" card.
	// Local-only in v1; federated rollup is a future follow-up.
	Investigations *db.DashboardInvestigations `json:"investigations"`

	// Phase 9.5 — federation rollup. Populated when this CP has
	// registered children. Counts include LOCAL state plus the cached
	// introspect counts from each child, so the parent's Dashboard
	// shows the global view rather than its own (typically empty)
	// local state. Children=0 means "this CP federates from no one,"
	// in which case all the above fields are pure local data.
	Federation dashFederation `json:"federation"`
}

type dashFederation struct {
	Children        int `json:"children"`         // peers registered
	HealthyChildren int `json:"healthy_children"` // peers with last_seen < 60s ago
	// LocalOnly mirrors what /api/dashboard would return on a non-
	// parent CP — the UI can show a "your CP only" toggle from this.
	LocalOnly dashLocalOnly `json:"local_only"`
}

type dashLocalOnly struct {
	DaimonsTotal int `json:"daimons_total"`
	NodesTotal   int `json:"nodes_total"`
	OpenFindings int64 `json:"open_findings"`
}

type dashDaimons struct {
	Total     int `json:"total"`
	Healthy   int `json:"healthy"`     // heartbeated <5min ago AND not suspended
	Unhealthy int `json:"unhealthy"`   // never heartbeated OR stale heartbeat
	Suspended int `json:"suspended"`   // desired_suspended=true (operator paused via UI)
}

type dashNodes struct {
	Total        int `json:"total"`
	Heartbeating int `json:"heartbeating"` // ≥1 daimon on this node heartbeated <5min
}

// dashTunnels is split out of nodes because reverse tunnels (Phase 6,
// for ad-hoc Run Agent) are a separate concept from heartbeating
// daemons. Most production fleets won't have any live tunnels.
type dashTunnels struct {
	Live int `json:"live"`
}

type dashFindings struct {
	Open     int64 `json:"open"`
	Critical int64 `json:"critical"`
	High     int64 `json:"high"`
}

// hostFindingCount is one row of the "top affected hosts" bar chart.
type hostFindingCount struct {
	Host     string `json:"host"`
	Open     int64  `json:"open"`
	Critical int64  `json:"critical"`
}

// osBucket is one slice of the OS-distribution donut. Family names
// are intentionally coarse (Debian, Fedora, Rocky, Ubuntu, RHEL,
// macOS, Other) — the dashboard wants a glanceable break-down, not
// per-version detail. Per-node detail lives on NodeDetail.
type osBucket struct {
	OS    string `json:"os"`
	Count int    `json:"count"`
}

// fleetStatus is the four-bucket node health summary. Each node falls
// into exactly one bucket, ranked by the order below (offline wins
// over frozen, frozen over patching). "Healthy" is the residual: a
// node that's heartbeating, not paused, and on the canonical binary
// + matching daimon hash.
type fleetStatus struct {
	Healthy       int `json:"healthy"`        // heartbeating + on canonical + not frozen
	NeedsPatching int `json:"needs_patching"` // heartbeating + drift (binary or definition)
	Offline       int `json:"offline"`        // no heartbeat in last 5min
	Frozen        int `json:"frozen"`         // auto_update_paused = true
	Total         int `json:"total"`
}

// dashDrift counts agents whose loaded definition_hash doesn't match
// the canonical hash on the CP, plus a short list of stale rows for
// the dashboard's "Stale fleet" panel. Empty list when everything's
// in sync.
type dashDrift struct {
	Total int           `json:"total"`
	Items []driftRow    `json:"items"`
}

type driftRow struct {
	Name              string `json:"name"`
	Host              string `json:"host"`
	CurrentHash       string `json:"current_hash,omitempty"`
	CanonicalHash     string `json:"canonical_hash,omitempty"`
	DefinitionVersion string `json:"definition_version,omitempty"`
	BinaryVersion     string `json:"binary_version,omitempty"`
}


// Dashboard handles GET /api/dashboard. Aggregates everything the
// landing page needs into a single response (stat tiles, drift,
// top-hosts bar, fleet-rollout donut). Timeline charts have their
// own /api/insights/{events,findings} endpoints driven by the
// dashboard's range picker.
//
// daimonFilesDir + daemonVersionFn are passed in so the handler can
// compute drift without reaching back into the controlplane package
// (which imports api, so we'd loop).
func Dashboard(store *db.Store, eventStore ports.EventStore, tunReg *tunnel.Registry, daimonFilesDir string, daemonVersionFn func() string) http.HandlerFunc {
	_ = eventStore // events live behind /api/insights/events now; kept on the signature for back-compat with the wiring.
	return func(w http.ResponseWriter, _ *http.Request) {
		out := dashboardResponse{}

		// Daimons (fetched paginated via existing Store helper, then summarised).
		// Suspended takes precedence over healthy/unhealthy in the bucket
		// counts: a paused daimon still heartbeats, but we don't want to
		// surface it as "healthy" because the operator deliberately stopped
		// its work. Heartbeating-host tracking still uses the live heartbeat
		// signal (paused or not) since heartbeat presence is what tells us
		// the host is reachable for fleet_status / drift purposes.
		agents, _ := store.ListAgents(1000, 0)
		fiveMinAgo := time.Now().Add(-5 * time.Minute)
		hostsHeartbeating := map[string]bool{}
		for _, a := range agents {
			out.Daimons.Total++
			heartbeating := a.LastHeartbeatAt.Valid && a.LastHeartbeatAt.Time.After(fiveMinAgo)
			if heartbeating {
				hostsHeartbeating[a.Host] = true
			}
			switch {
			case a.DesiredSuspended:
				out.Daimons.Suspended++
			case heartbeating:
				out.Daimons.Healthy++
			default:
				out.Daimons.Unhealthy++
			}
		}

		// Nodes — total registered + how many have ≥1 daimon heartbeating
		// in the last 5 min. This is the operator's "are my nodes alive?"
		// question (separate from the reverse-tunnel count below).
		nodes, _ := store.ListNodes(1000, 0)
		out.Nodes.Total = len(nodes)
		for _, n := range nodes {
			daemonHost := n.DaemonHostname.String
			if daemonHost == "" {
				daemonHost = n.Hostname
			}
			if hostsHeartbeating[daemonHost] {
				out.Nodes.Heartbeating++
			}
		}

		// Tunnels — Phase 6 reverse-tunnel count. Used for ad-hoc
		// Run Agent only; most fleets never have any.
		out.Tunnels.Live = len(tunReg.Names())

		// Findings — reuse the existing summary query.
		if fs, err := store.FindingsSummary(); err == nil {
			out.Findings.Open = fs.Open
			out.Findings.Critical = fs.Critical
			out.Findings.High = fs.High
			// Top hosts uses the per-agent breakdown the summary already
			// builds; flatten by host instead.
		}

		// Drift — for each heartbeating agent, compare reported
		// definition_hash to canonical. Same loop also tracks which
		// (name, host) pairs are drifting so we can credit nodes
		// with patching pressure for the per-node fleet status below.
		canonicalDaemon := ""
		if daemonVersionFn != nil {
			canonicalDaemon = daemonVersionFn()
		}
		canonicalCache := map[string]string{}
		canonical := func(name string) string {
			if h, ok := canonicalCache[name]; ok {
				return h
			}
			h := computeCanonicalHash(daimonFilesDir, name)
			canonicalCache[name] = h
			return h
		}
		hostsNeedingPatch := map[string]bool{}
		for _, a := range agents {
			if !a.LastHeartbeatAt.Valid || a.LastHeartbeatAt.Time.Before(fiveMinAgo) {
				continue
			}
			binary := a.Version.String
			cur := a.CurrentDefinitionHash.String
			want := ""
			if daimonFilesDir != "" {
				want = canonical(a.Name)
			}
			definitionDrift := want != "" && cur != "" && cur != want
			binaryDrift := canonicalDaemon != "" && binary != "" && binary != canonicalDaemon
			if !definitionDrift && !binaryDrift {
				continue
			}
			hostsNeedingPatch[a.Host] = true
			out.Drift.Total++
			if len(out.Drift.Items) < 10 {
				out.Drift.Items = append(out.Drift.Items, driftRow{
					Name:              a.Name,
					Host:              a.Host,
					CurrentHash:       cur,
					CanonicalHash:     want,
					DefinitionVersion: a.DefinitionVersion.String,
					BinaryVersion:     binary,
				})
			}
		}

		// Fleet status — bucket each node into exactly one of the four
		// states. Priority: offline → frozen → needs_patching → healthy.
		osCounts := map[string]int{}
		for _, n := range nodes {
			out.FleetStatus.Total++
			daemonHost := n.DaemonHostname.String
			if daemonHost == "" {
				daemonHost = n.Hostname
			}
			switch {
			case !hostsHeartbeating[daemonHost]:
				out.FleetStatus.Offline++
			case n.AutoUpdatePaused:
				out.FleetStatus.Frozen++
			case hostsNeedingPatch[daemonHost]:
				out.FleetStatus.NeedsPatching++
			default:
				out.FleetStatus.Healthy++
			}

			// OS distribution — read from os_release first, fall back
			// to the node name (lab convention is <role>-<distro>-<n>).
			os := classifyOS(n.OSRelease.String, n.Name)
			osCounts[os]++
		}
		for os, n := range osCounts {
			out.OSDistribution = append(out.OSDistribution, osBucket{OS: os, Count: n})
		}
		// Sort by count desc, then name asc for stable rendering.
		sort.SliceStable(out.OSDistribution, func(i, j int) bool {
			if out.OSDistribution[i].Count != out.OSDistribution[j].Count {
				return out.OSDistribution[i].Count > out.OSDistribution[j].Count
			}
			return out.OSDistribution[i].OS < out.OSDistribution[j].OS
		})
		if out.OSDistribution == nil {
			out.OSDistribution = []osBucket{}
		}

		// Top hosts by open findings count (limit 10).
		if rows, err := store.OpenFindingsByHost(10); err == nil {
			out.TopHosts = make([]hostFindingCount, 0, len(rows))
			for _, r := range rows {
				out.TopHosts = append(out.TopHosts, hostFindingCount{
					Host:     r.Host,
					Open:     r.Open,
					Critical: r.Critical,
				})
			}
		}
		if out.TopHosts == nil {
			out.TopHosts = []hostFindingCount{}
		}
		if out.Drift.Items == nil {
			out.Drift.Items = []driftRow{}
		}

		// Phase 22.7 — investigations rollup. Soft-fail: if the
		// query errors (unlikely; same DB the rest of the response
		// just read), we'd rather render the dashboard with an
		// empty Investigations section than 500.
		if invs, err := store.DashboardInvestigationStats(5); err == nil {
			out.Investigations = invs
		} else {
			out.Investigations = &db.DashboardInvestigations{
				RecentActive: []db.DashboardInvestigationItem{},
			}
		}

		// Phase 9.5: fold federated peer counts into the headline
		// numbers. The Dashboard is the operator's primary
		// situational-awareness page; on a parent CP it should answer
		// "what's the state of the WHOLE fleet?" not "what's the
		// state of this one (often empty) CP?".
		out.Federation.LocalOnly = dashLocalOnly{
			DaimonsTotal: out.Daimons.Total,
			NodesTotal:   out.Nodes.Total,
			OpenFindings: out.Findings.Open,
		}
		peers, _ := store.ListFederationPeers()
		out.Federation.Children = len(peers)
		fiveMinAgoT := time.Now().Add(-5 * time.Minute)
		for _, p := range peers {
			if p.LastSeenAt.Valid && p.LastSeenAt.Time.After(fiveMinAgoT) {
				out.Federation.HealthyChildren++
			}
			if p.IntrospectJSON == "" {
				continue
			}
			var snap struct {
				Counts struct {
					Daimons          int   `json:"daimons"`
					DaimonsHealthy   int   `json:"daimons_healthy"`
					Nodes            int   `json:"nodes"`
					OpenFindings     int64 `json:"open_findings"`
					FindingsCritical int64 `json:"findings_critical"`
					FindingsHigh     int64 `json:"findings_high"`
				} `json:"counts"`
				OSDistribution []osBucket         `json:"os_distribution"`
				TopHosts       []hostFindingCount `json:"top_hosts"`
			}
			if err := json.Unmarshal([]byte(p.IntrospectJSON), &snap); err != nil {
				continue
			}
			out.Daimons.Total += snap.Counts.Daimons
			out.Daimons.Healthy += snap.Counts.DaimonsHealthy
			// We don't get a separate "unhealthy" count from introspect
			// today (it's derivable as Total - Healthy on the child but
			// not exposed); approximate it the same way here.
			out.Daimons.Unhealthy += snap.Counts.Daimons - snap.Counts.DaimonsHealthy
			out.Nodes.Total += snap.Counts.Nodes
			// Same for "heartbeating" — approximate as healthy daimons,
			// since a node with ≥1 healthy daimon is reachable.
			if snap.Counts.DaimonsHealthy > 0 {
				out.Nodes.Heartbeating += snap.Counts.Nodes
			}
			out.Findings.Open += snap.Counts.OpenFindings
			out.Findings.Critical += snap.Counts.FindingsCritical
			out.Findings.High += snap.Counts.FindingsHigh
			out.FleetStatus.Total += snap.Counts.Nodes
			out.FleetStatus.Healthy += snap.Counts.Nodes // optimistic until we expose per-status counts
			// OS distribution merge — sum counts by OS family.
			for _, b := range snap.OSDistribution {
				merged := false
				for i := range out.OSDistribution {
					if out.OSDistribution[i].OS == b.OS {
						out.OSDistribution[i].Count += b.Count
						merged = true
						break
					}
				}
				if !merged {
					out.OSDistribution = append(out.OSDistribution, b)
				}
			}
			// Top-hosts merge — sum open/critical by host across CPs.
			// A host name collision across CPs is unusual but harmless;
			// we'd just add the counts (which is what an operator
			// looking at "host fleet impact" would want anyway).
			for _, b := range snap.TopHosts {
				merged := false
				for i := range out.TopHosts {
					if out.TopHosts[i].Host == b.Host {
						out.TopHosts[i].Open += b.Open
						out.TopHosts[i].Critical += b.Critical
						merged = true
						break
					}
				}
				if !merged {
					out.TopHosts = append(out.TopHosts, b)
				}
			}
		}
		// Re-sort + cap top_hosts after the federated merge so the bar
		// chart shows the global top-10 even when the merge introduced
		// new contenders.
		sort.SliceStable(out.TopHosts, func(i, j int) bool {
			if out.TopHosts[i].Open != out.TopHosts[j].Open {
				return out.TopHosts[i].Open > out.TopHosts[j].Open
			}
			return out.TopHosts[i].Host < out.TopHosts[j].Host
		})
		if len(out.TopHosts) > 10 {
			out.TopHosts = out.TopHosts[:10]
		}
		// Re-sort OS distribution after federated merge so the chart
		// order stays count-desc.
		sort.SliceStable(out.OSDistribution, func(i, j int) bool {
			if out.OSDistribution[i].Count != out.OSDistribution[j].Count {
				return out.OSDistribution[i].Count > out.OSDistribution[j].Count
			}
			return out.OSDistribution[i].OS < out.OSDistribution[j].OS
		})

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}


// classifyOS bucketts a node into a coarse OS family (Debian /
// Fedora / Rocky / Ubuntu / RHEL / macOS / Other). Reads os_release
// (the contents of /etc/os-release captured by the Phase 7a tunnel
// probe) when available, otherwise falls back to keywords in the
// node name. Returns "Unknown" only when both are silent.
func classifyOS(osRelease, nodeName string) string {
	osr := strings.ToLower(osRelease)
	for _, m := range []struct{ k, v string }{
		{"ubuntu", "Ubuntu"},
		{"debian", "Debian"},
		{"fedora", "Fedora"},
		{"rocky", "Rocky"},
		{"alma", "AlmaLinux"},
		{"red hat", "RHEL"},
		{"rhel", "RHEL"},
		{"centos", "CentOS"},
		{"alpine", "Alpine"},
		{"arch", "Arch"},
		{"darwin", "macOS"},
		{"mac os", "macOS"},
	} {
		if osr != "" && strings.Contains(osr, m.k) {
			return m.v
		}
	}
	// Fall back to the node name. Lab convention is
	// "<role>-<distro>-<n>" (node-debian, edr-fedora-1, …).
	nn := strings.ToLower(nodeName)
	for _, m := range []struct{ k, v string }{
		{"debian", "Debian"},
		{"ubuntu", "Ubuntu"},
		{"fedora", "Fedora"},
		{"rocky", "Rocky"},
		{"alma", "AlmaLinux"},
		{"rhel", "RHEL"},
		{"centos", "CentOS"},
		{"alpine", "Alpine"},
		{"arch", "Arch"},
		{"mac", "macOS"},
		{"darwin", "macOS"},
	} {
		if strings.Contains(nn, m.k) {
			return m.v
		}
	}
	return "Unknown"
}

// computeCanonicalHash returns the sha256 of the daimon library's
// *.md file for `name`, hex-encoded. Returns "" when the file is
// missing — caller treats that as "no canonical version, no drift."
func computeCanonicalHash(daimonFilesDir, name string) string {
	if daimonFilesDir == "" || name == "" {
		return ""
	}
	path := filepath.Join(daimonFilesDir, name+".md")
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ── /api/insights/findings ─────────────────────────────────────────

// insightsFindingsResp is the response shape for the multi-series
// findings-over-time chart. Each bucket lists the per-series counts
// for that bucket; the UI flattens that into one Recharts dataset.
type insightsFindingsResp struct {
	BucketMs int64                 `json:"bucket_ms"` // bucket size in ms (e.g. 3600000 for 1h)
	GroupBy  string                `json:"group_by"`  // severity | agent | host
	Series   []string              `json:"series"`    // top-N series names ordered by total
	Buckets  []insightsBucket      `json:"buckets"`
}

type insightsBucket struct {
	Ts int64            `json:"ts"`
	By map[string]int64 `json:"by"`
}

// InsightsFindings handles GET /api/insights/findings.
//
// Query params:
//
//	since=24h|7d|30d  (default 24h)
//	group_by=severity|agent|host  (default severity)
//	top=8  (max series; rest folded into "other")
//
// Returns time-bucketed counts of findings, grouped by the requested
// dimension, ready for a multi-line chart.
func InsightsFindings(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		since := r.URL.Query().Get("since")
		if since == "" {
			since = "24h"
		}
		groupBy := r.URL.Query().Get("group_by")
		if groupBy == "" {
			groupBy = "severity"
		}
		topN, _ := strconv.Atoi(r.URL.Query().Get("top"))
		if topN <= 0 || topN > 20 {
			topN = 8
		}

		var dur time.Duration
		var bucketMs int64
		switch since {
		case "30m":
			dur = 30 * time.Minute
			bucketMs = int64(time.Minute / time.Millisecond) // 1m buckets → 30 points
		case "1h":
			dur = time.Hour
			bucketMs = int64(2 * time.Minute / time.Millisecond) // 2m buckets → 30 points
		case "24h":
			dur = 24 * time.Hour
			bucketMs = int64(time.Hour / time.Millisecond)
		case "7d":
			dur = 7 * 24 * time.Hour
			bucketMs = int64(6 * time.Hour / time.Millisecond)
		case "30d":
			dur = 30 * 24 * time.Hour
			bucketMs = int64(24 * time.Hour / time.Millisecond)
		default:
			http.Error(w, "since must be 30m, 1h, 24h, 7d, or 30d", http.StatusBadRequest)
			return
		}

		var dimCol string
		switch groupBy {
		case "severity":
			dimCol = "severity"
		case "agent":
			dimCol = "agent"
		case "host":
			dimCol = "host"
		default:
			http.Error(w, "group_by must be severity, agent, or host", http.StatusBadRequest)
			return
		}

		sinceMs := time.Now().Add(-dur).UnixMilli()
		dbBuckets, series, err := store.FindingsTimeSeries(sinceMs, bucketMs, dimCol, topN)
		if err != nil {
			http.Error(w, "store: "+err.Error(), http.StatusInternalServerError)
			return
		}
		raw := make([]insightsBucket, len(dbBuckets))
		for i, b := range dbBuckets {
			raw[i] = insightsBucket{Ts: b.Ts, By: b.By}
		}

		// Fill empty buckets so the line chart has a continuous X axis.
		filled := fillBucketGrid(raw, sinceMs, bucketMs)
		// Sort series by total descending — the UI uses this to assign colors.
		sort.SliceStable(series, func(i, j int) bool {
			ti, tj := int64(0), int64(0)
			for _, b := range filled {
				ti += b.By[series[i]]
				tj += b.By[series[j]]
			}
			return ti > tj
		})

		// Ensure non-nil slices so the JSON wire shape stays
		// `"series":[]` rather than `"series":null` — the UI's
		// useMemo dereferences these directly and a null crashes
		// the whole Dashboard render.
		if series == nil {
			series = []string{}
		}
		if filled == nil {
			filled = []insightsBucket{}
		}
		out := insightsFindingsResp{
			BucketMs: bucketMs,
			GroupBy:  groupBy,
			Series:   series,
			Buckets:  filled,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// ── /api/insights/orchestrations-top ────────────────────────────────

// orchestrationsTopResp is the shape the dashboard's "Top
// orchestrations" card consumes — a leaderboard of the most-active
// orchestrations in the window, with each row's success/failure
// split + average completed-run duration.
type orchestrationsTopResp struct {
	Since string                     `json:"since"`
	Rows  []orchestrationsTopRow     `json:"rows"`
}

type orchestrationsTopRow struct {
	OrchestrationID  int64 `json:"orchestration_id"`
	Name             string `json:"name"`
	Total            int64 `json:"total"`
	Completed        int64 `json:"completed"`
	Failed           int64 `json:"failed"`
	Running          int64 `json:"running"`
	Cancelled        int64 `json:"cancelled"`
	Pending          int64 `json:"pending"`
	ApprovalRequired int64 `json:"approval_required"`
	AvgDurationMs    int64 `json:"avg_duration_ms,omitempty"`
}

// InsightsOrchestrationsTop handles GET /api/insights/orchestrations-top.
//
// Query params:
//
//	since=30m|1h|24h|7d|30d   default 24h
//	limit=N                   default 10, capped at 50
func InsightsOrchestrationsTop(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		since := r.URL.Query().Get("since")
		if since == "" {
			since = "24h"
		}
		var dur time.Duration
		switch since {
		case "30m":
			dur = 30 * time.Minute
		case "1h":
			dur = time.Hour
		case "24h":
			dur = 24 * time.Hour
		case "7d":
			dur = 7 * 24 * time.Hour
		case "30d":
			dur = 30 * 24 * time.Hour
		default:
			http.Error(w, "since must be 30m, 1h, 24h, 7d, or 30d", http.StatusBadRequest)
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 {
			limit = 10
		}

		sinceMs := time.Now().Add(-dur).UnixMilli()
		stats, err := store.TopOrchestrations(sinceMs, limit)
		if err != nil {
			http.Error(w, "store: "+err.Error(), http.StatusInternalServerError)
			return
		}

		out := orchestrationsTopResp{
			Since: since,
			Rows:  make([]orchestrationsTopRow, len(stats)),
		}
		for i, s := range stats {
			out.Rows[i] = orchestrationsTopRow{
				OrchestrationID:  s.OrchestrationID,
				Name:             s.Name,
				Total:            s.Total,
				Completed:        s.Completed,
				Failed:           s.Failed,
				Running:          s.Running,
				Cancelled:        s.Cancelled,
				Pending:          s.Pending,
				ApprovalRequired: s.ApprovalReq,
			}
			if s.AvgDurationMs.Valid {
				out.Rows[i].AvgDurationMs = s.AvgDurationMs.Int64
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// ── /api/insights/triage-outcomes ───────────────────────────────────

// triageOutcomesResp is the shape the dashboard's "Findings & triage"
// stacked-area chart consumes. Per bucket, `incoming` is the total
// findings ingested in the window; the three auto-* fields are the
// breakdown of outcomes applied during the same window (Tier-0 dedup,
// Tier-1 status closures, Tier-1 tag-only confirmations).
type triageOutcomesResp struct {
	BucketMs int64                   `json:"bucket_ms"`
	Buckets  []triageOutcomesBucket  `json:"buckets"`
	Totals   triageOutcomesTotals    `json:"totals"`
}

type triageOutcomesBucket struct {
	Ts             int64 `json:"ts"`
	Incoming       int64 `json:"incoming"`
	T0Superseded   int64 `json:"t0_superseded"`
	T1Resolved     int64 `json:"t1_resolved"`
	T1Tagged       int64 `json:"t1_tagged"`
}

type triageOutcomesTotals struct {
	Incoming     int64 `json:"incoming"`
	AutoHandled  int64 `json:"auto_handled"`  // sum of the three auto-* columns
	TriageRate   float64 `json:"triage_rate"` // auto_handled / incoming, 0 when incoming==0
}

// InsightsTriageOutcomes handles GET /api/insights/triage-outcomes.
//
// Query params:
//
//	since=30m|1h|24h|7d|30d   default 24h
//
// Returns a per-bucket time series + the window totals so the
// dashboard's Triage rate stat tile and the stacked-area chart can be
// driven by one round-trip.
func InsightsTriageOutcomes(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		since := r.URL.Query().Get("since")
		if since == "" {
			since = "24h"
		}
		var dur time.Duration
		var bucketMs int64
		switch since {
		case "30m":
			dur = 30 * time.Minute
			bucketMs = int64(time.Minute / time.Millisecond)
		case "1h":
			dur = time.Hour
			bucketMs = int64(2 * time.Minute / time.Millisecond)
		case "24h":
			dur = 24 * time.Hour
			bucketMs = int64(time.Hour / time.Millisecond)
		case "7d":
			dur = 7 * 24 * time.Hour
			bucketMs = int64(6 * time.Hour / time.Millisecond)
		case "30d":
			dur = 30 * 24 * time.Hour
			bucketMs = int64(24 * time.Hour / time.Millisecond)
		default:
			http.Error(w, "since must be 30m, 1h, 24h, 7d, or 30d", http.StatusBadRequest)
			return
		}

		now := time.Now().UnixMilli()
		sinceMs := now - dur.Milliseconds()
		dbBuckets, err := store.TriageOutcomes(sinceMs, now, bucketMs)
		if err != nil {
			http.Error(w, "store: "+err.Error(), http.StatusInternalServerError)
			return
		}

		out := triageOutcomesResp{
			BucketMs: bucketMs,
			Buckets:  make([]triageOutcomesBucket, len(dbBuckets)),
		}
		for i, b := range dbBuckets {
			out.Buckets[i] = triageOutcomesBucket{
				Ts:           b.Ts,
				Incoming:     b.Incoming,
				T0Superseded: b.T0Superseded,
				T1Resolved:   b.T1Resolved,
				T1Tagged:     b.T1Tagged,
			}
			out.Totals.Incoming += b.Incoming
			out.Totals.AutoHandled += b.T0Superseded + b.T1Resolved + b.T1Tagged
		}
		if out.Totals.Incoming > 0 {
			out.Totals.TriageRate = float64(out.Totals.AutoHandled) / float64(out.Totals.Incoming)
		}
		// Cap at 1.0 so a brief surge of edits processing pre-window
		// findings doesn't render as 187% triage.
		if out.Totals.TriageRate > 1.0 {
			out.Totals.TriageRate = 1.0
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// ── /api/insights/events ──────────────────────────────────────────

// eventsTimelineResp is the response shape for the events-over-time
// chart. Mirrors insightsFindingsResp's shape but as a single series:
// total events per bucket. The stacked-by-type breakdown lived on
// the dashboard's old events_per_hour card; on the new dashboard the
// chart is intentionally minimalist (one line, total only).
type eventsTimelineResp struct {
	BucketMs int64               `json:"bucket_ms"`
	Buckets  []eventsBucketEntry `json:"buckets"`
}

type eventsBucketEntry struct {
	Ts    int64 `json:"ts"`
	Count int64 `json:"count"`
}

// InsightsEvents handles GET /api/insights/events?since=<range>.
// Returns a single line series (total event count per bucket) over
// the requested range, with the same bucket-ms ladder as
// InsightsFindings so the dashboard can drive both charts from one
// shared range picker.
func InsightsEvents(eventStore ports.EventStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		since := r.URL.Query().Get("since")
		if since == "" {
			since = "24h"
		}
		var dur time.Duration
		var bucketMs int64
		switch since {
		case "30m":
			dur = 30 * time.Minute
			bucketMs = int64(time.Minute / time.Millisecond)
		case "1h":
			dur = time.Hour
			bucketMs = int64(2 * time.Minute / time.Millisecond)
		case "24h":
			dur = 24 * time.Hour
			bucketMs = int64(time.Hour / time.Millisecond)
		case "7d":
			dur = 7 * 24 * time.Hour
			bucketMs = int64(6 * time.Hour / time.Millisecond)
		case "30d":
			dur = 30 * 24 * time.Hour
			bucketMs = int64(24 * time.Hour / time.Millisecond)
		default:
			http.Error(w, "since must be 30m, 1h, 24h, 7d, or 30d", http.StatusBadRequest)
			return
		}

		// Build the bucket grid first so empty windows render as zeros.
		now := time.Now().UnixMilli()
		first := ((now - dur.Milliseconds()) / bucketMs) * bucketMs
		last := (now / bucketMs) * bucketMs
		count := int((last-first)/bucketMs) + 1
		if count > 720 {
			count = 720
		}
		buckets := make([]eventsBucketEntry, count)
		for i := 0; i < count; i++ {
			buckets[i] = eventsBucketEntry{Ts: first + int64(i)*bucketMs, Count: 0}
		}

		// Walk events newest-first via the EventStore until we cross
		// the time window's start.
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		const pageSize = 500
		beforeTs := int64(0)
		for {
			rows, err := eventStore.Recent(ctx, pageSize, beforeTs)
			if err != nil || len(rows) == 0 {
				break
			}
			stop := false
			for _, e := range rows {
				if e.Ts < first {
					stop = true
					break
				}
				idx := int((e.Ts - first) / bucketMs)
				if idx < 0 || idx >= len(buckets) {
					continue
				}
				buckets[idx].Count++
			}
			if stop || len(rows) < pageSize {
				break
			}
			beforeTs = rows[len(rows)-1].Ts
		}

		out := eventsTimelineResp{BucketMs: bucketMs, Buckets: buckets}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// fillBucketGrid pads the result with zero-count buckets so the
// timeline doesn't jump across empty hours. Aligns to bucket-ms.
func fillBucketGrid(rawBuckets []insightsBucket, sinceMs, bucketMs int64) []insightsBucket {
	first := (sinceMs / bucketMs) * bucketMs
	last := (time.Now().UnixMilli() / bucketMs) * bucketMs
	if last < first {
		last = first
	}
	count := int((last-first)/bucketMs) + 1
	if count > 720 { // safety
		count = 720
	}
	have := map[int64]map[string]int64{}
	for _, b := range rawBuckets {
		have[b.Ts] = b.By
	}
	out := make([]insightsBucket, count)
	for i := 0; i < count; i++ {
		ts := first + int64(i)*bucketMs
		by := have[ts]
		if by == nil {
			by = map[string]int64{}
		}
		out[i] = insightsBucket{Ts: ts, By: by}
	}
	return out
}

