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
// on every refresh; the page polls this endpoint every ~15s.
type dashboardResponse struct {
	Daimons        dashDaimons             `json:"daimons"`
	Nodes          dashNodes               `json:"nodes"`
	Findings       dashFindings            `json:"findings"`
	Drift          dashDrift               `json:"drift"`
	EventsPerHour  []eventBucket           `json:"events_per_hour"`
	RecentCritical []*db.Finding           `json:"recent_critical"`
}

type dashDaimons struct {
	Total     int `json:"total"`
	Healthy   int `json:"healthy"`
	Unhealthy int `json:"unhealthy"`
}

type dashNodes struct {
	Total     int `json:"total"`
	Connected int `json:"connected"`
}

type dashFindings struct {
	Open     int64 `json:"open"`
	Critical int64 `json:"critical"`
	High     int64 `json:"high"`
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

type eventBucket struct {
	HourTs    int64            `json:"hour_ts"` // unix ms (UTC) at the start of the hour
	ByType    map[string]int64 `json:"by_type"`
}

// Dashboard handles GET /api/dashboard. Aggregates everything the
// landing page needs into a single response.
//
// daimonFilesDir + daemonVersionFn are passed in so the handler can
// compute drift without reaching back into the controlplane package
// (which imports api, so we'd loop).
func Dashboard(store *db.Store, eventStore ports.EventStore, tunReg *tunnel.Registry, daimonFilesDir string, daemonVersionFn func() string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		out := dashboardResponse{}

		// Daimons (fetched paginated via existing Store helper, then summarised).
		agents, _ := store.ListAgents(1000, 0)
		fiveMinAgo := time.Now().Add(-5 * time.Minute)
		for _, a := range agents {
			out.Daimons.Total++
			if a.LastHeartbeatAt.Valid && a.LastHeartbeatAt.Time.After(fiveMinAgo) {
				out.Daimons.Healthy++
			} else {
				out.Daimons.Unhealthy++
			}
		}

		// Nodes.
		nodes, _ := store.ListNodes(1000, 0)
		out.Nodes.Total = len(nodes)
		out.Nodes.Connected = len(tunReg.Names())

		// Findings — reuse the existing summary query.
		if fs, err := store.FindingsSummary(); err == nil {
			out.Findings.Open = fs.Open
			out.Findings.Critical = fs.Critical
			out.Findings.High = fs.High
		}

		// Drift — for each agent currently heartbeating, compare its
		// reported definition_hash to the canonical hash. Show up to
		// 10 stale rows; the count is unbounded.
		if daimonFilesDir != "" {
			canonicalCache := map[string]string{}
			canonical := func(name string) string {
				if h, ok := canonicalCache[name]; ok {
					return h
				}
				h := computeCanonicalHash(daimonFilesDir, name)
				canonicalCache[name] = h
				return h
			}
			canonicalDaemon := ""
			if daemonVersionFn != nil {
				canonicalDaemon = daemonVersionFn()
			}
			for _, a := range agents {
				if !a.LastHeartbeatAt.Valid || a.LastHeartbeatAt.Time.Before(fiveMinAgo) {
					continue
				}
				cur := a.CurrentDefinitionHash.String
				want := canonical(a.Name)
				binary := a.Version.String
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
		}

		// Events per hour for the last 24h.
		out.EventsPerHour = eventsPerHour24h(eventStore)

		// Top 10 most-recent open CRITICAL findings.
		if recent, err := store.ListFindings(db.FindingFilter{
			Severities: []string{"CRITICAL"},
			OnlyOpen:   true,
			Limit:      10,
		}); err == nil {
			out.RecentCritical = recent
		}
		if out.RecentCritical == nil {
			out.RecentCritical = []*db.Finding{}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// eventsPerHour24h queries the events firehose for the last 24h and
// buckets the rows by hour + type. 24 buckets total; empty hours
// land as zero-count entries so the chart timeline is continuous.
func eventsPerHour24h(es ports.EventStore) []eventBucket {
	now := time.Now().UTC().Truncate(time.Hour)
	buckets := make([]eventBucket, 24)
	for i := 0; i < 24; i++ {
		buckets[i] = eventBucket{
			HourTs: now.Add(-time.Duration(23-i) * time.Hour).UnixMilli(),
			ByType: map[string]int64{},
		}
	}
	if es == nil {
		return buckets
	}
	cutoff := now.Add(-24 * time.Hour).UnixMilli()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	beforeTs := int64(0)
	const pageSize = 500
	for {
		rows, err := es.Recent(ctx, pageSize, beforeTs)
		if err != nil || len(rows) == 0 {
			break
		}
		stop := false
		for _, e := range rows {
			if e.Ts < cutoff {
				stop = true
				break
			}
			idx := int((e.Ts - buckets[0].HourTs) / int64(time.Hour/time.Millisecond))
			if idx < 0 || idx >= len(buckets) {
				continue
			}
			t := e.Type
			if t == "" {
				t = "unknown"
			}
			buckets[idx].ByType[t]++
		}
		if stop || len(rows) < pageSize {
			break
		}
		beforeTs = rows[len(rows)-1].Ts
	}
	return buckets
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
			http.Error(w, "since must be 24h, 7d, or 30d", http.StatusBadRequest)
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

