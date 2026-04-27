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
	FleetRollout  fleetRollout       `json:"fleet_rollout"`    // % of fleet on canonical version
}

type dashDaimons struct {
	Total     int `json:"total"`
	Healthy   int `json:"healthy"`     // heartbeated <5min ago
	Unhealthy int `json:"unhealthy"`
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

// fleetRollout drives the donut: what fraction of the fleet runs the
// CP-canonical daemon version vs anything else.
type fleetRollout struct {
	Canonical    int    `json:"canonical"`    // count of agents whose binary version == canonical
	OtherVersion int    `json:"other_version"`// count whose version is set but doesn't match
	Unknown      int    `json:"unknown"`      // count whose version is empty (legacy daemons)
	Total        int    `json:"total"`
	CanonicalRef string `json:"canonical_ref"` // the canonical version string for context
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
		agents, _ := store.ListAgents(1000, 0)
		fiveMinAgo := time.Now().Add(-5 * time.Minute)
		hostsHeartbeating := map[string]bool{}
		for _, a := range agents {
			out.Daimons.Total++
			if a.LastHeartbeatAt.Valid && a.LastHeartbeatAt.Time.After(fiveMinAgo) {
				out.Daimons.Healthy++
				hostsHeartbeating[a.Host] = true
			} else {
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
		// definition_hash to canonical. Build the rollout summary in
		// the same loop.
		canonicalDaemon := ""
		if daemonVersionFn != nil {
			canonicalDaemon = daemonVersionFn()
		}
		out.FleetRollout.CanonicalRef = canonicalDaemon
		canonicalCache := map[string]string{}
		canonical := func(name string) string {
			if h, ok := canonicalCache[name]; ok {
				return h
			}
			h := computeCanonicalHash(daimonFilesDir, name)
			canonicalCache[name] = h
			return h
		}
		for _, a := range agents {
			if !a.LastHeartbeatAt.Valid || a.LastHeartbeatAt.Time.Before(fiveMinAgo) {
				continue
			}
			binary := a.Version.String
			out.FleetRollout.Total++
			switch {
			case binary == "":
				out.FleetRollout.Unknown++
			case canonicalDaemon != "" && binary == canonicalDaemon:
				out.FleetRollout.Canonical++
			default:
				out.FleetRollout.OtherVersion++
			}
			if daimonFilesDir == "" {
				continue
			}
			cur := a.CurrentDefinitionHash.String
			want := canonical(a.Name)
			definitionDrift := want != "" && cur != "" && cur != want
			binaryDrift := canonicalDaemon != "" && binary != "" && binary != canonicalDaemon
			if !definitionDrift && !binaryDrift {
				continue
			}
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

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
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

