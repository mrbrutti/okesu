package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/section9labs/okesu/controlplane/ports"
)

// EventsList returns recent events as a JSON array.
//
//	GET /api/events?limit=N&before_ts=<unix-ms>
//
// `before_ts` is a cursor — only events strictly older than that
// timestamp come back. Used by the UI's infinite-scroll pagination so
// that new events arriving via the SSE stream don't shift the offset
// window during scroll-down.
//
// Read-side counterpart of WebhookHandler — both go through the
// EventStore port so swapping the events firehose between SQLite (dev)
// and ClickHouse (production) is a config decision, not a code change.
// eventJSON is the wire shape for /api/events list rows. Extracted from
// EventsList so the federation aggregator can deserialize the same
// shape for cross-CP merging.
type eventJSON struct {
	ID       int64           `json:"id"`
	Ts       int64           `json:"ts"`
	Type     string          `json:"type"`
	Agent    string          `json:"agent,omitempty"`
	Host     string          `json:"host,omitempty"`
	Severity string          `json:"severity,omitempty"`
	Title    string          `json:"title,omitempty"`
	Raw      json.RawMessage `json:"raw"`
	// Phase 9.6: federation source — populated when this row was
	// fetched from a federated child. Local rows leave this nil.
	CPSource *CPSourceRef `json:"cp_source,omitempty"`
}

func EventsList(store ports.EventStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		if limit <= 0 {
			limit = 100
		}
		beforeTs, _ := strconv.ParseInt(q.Get("before_ts"), 10, 64)
		// Optional server-side filters. The detail-page Messages tab
		// passes agent+host so it gets only one daimon's events
		// instead of fighting for room in the most-recent-1000 window
		// across a busy fleet (where 1000 events ≈ 60s of history at
		// fleet scale, often missing the very daimon the operator
		// just clicked on).
		filterAgent := q.Get("agent")
		filterHost := q.Get("host")
		// Over-fetch when filtering — most rows in the recent window
		// won't match, so we need a wider scoop to land `limit`
		// matching rows. Cap at a generous ceiling so we don't pull
		// the whole table on a busy CP.
		fetchLimit := limit
		if filterAgent != "" || filterHost != "" {
			fetchLimit = limit * 25
			if fetchLimit > 5000 {
				fetchLimit = 5000
			}
		}
		events, err := store.Recent(r.Context(), fetchLimit, beforeTs)
		if err != nil {
			http.Error(w, "query: "+err.Error(), http.StatusInternalServerError)
			return
		}

		out := make([]eventJSON, 0, limit)
		for _, e := range events {
			if filterAgent != "" && e.Agent != filterAgent {
				continue
			}
			if filterHost != "" && e.Host != filterHost {
				continue
			}
			out = append(out, eventJSON{
				ID:       e.ID,
				Ts:       e.Ts,
				Type:     e.Type,
				Agent:    e.Agent,
				Host:     e.Host,
				Severity: e.Severity,
				Title:    e.Title,
				Raw:      json.RawMessage(e.RawJSON),
			})
			if len(out) >= limit {
				break
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// EventsSubscriber is the minimal interface required by EventsStream.
// Implemented by *controlplane.Broadcaster.
type EventsSubscriber interface {
	Subscribe() (<-chan []byte, func())
}

// EventsStream is the SSE endpoint that pushes new events to the browser.
//
//   GET /api/events/stream
//     ?type=finding,error            (any of these event types — empty = all)
//     ?agent=edr,oci-posture         (substring/exact agent name match — empty = all)
//     ?host=prod-                    (host substring — empty = all)
//     ?severity=CRITICAL,HIGH        (severity values — empty = all)
//
// Filtering happens server-side so unrelated events never hit the wire — this
// matters when a busy fleet emits thousands of tick_done events the dashboard
// doesn't care about. Filters AND together; values within a filter OR.
func EventsStream(bcast EventsSubscriber) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		filter := newEventFilter(r)

		ch, cancel := bcast.Subscribe()
		defer cancel()

		// Initial comment to flush headers immediately.
		_, _ = w.Write([]byte(": connected\n\n"))
		flusher.Flush()

		for {
			select {
			case <-r.Context().Done():
				return
			case line, ok := <-ch:
				if !ok {
					return
				}
				if !filter.match(line) {
					continue
				}
				_, _ = w.Write([]byte("data: "))
				_, _ = w.Write(line)
				_, _ = w.Write([]byte("\n\n"))
				flusher.Flush()
			}
		}
	}
}

// eventFilter pre-decodes the per-connection allow-set so each event line is
// just a few map lookups rather than re-parsing the URL each time.
type eventFilter struct {
	types        map[string]struct{}
	severities   map[string]struct{}
	agents       []string // substring match
	hosts        []string // substring match
}

func newEventFilter(r *http.Request) *eventFilter {
	f := &eventFilter{}
	if v := r.URL.Query().Get("type"); v != "" {
		f.types = csvSet(v)
	}
	if v := r.URL.Query().Get("severity"); v != "" {
		f.severities = csvSet(strings.ToUpper(v))
	}
	if v := r.URL.Query().Get("agent"); v != "" {
		f.agents = csvList(v)
	}
	if v := r.URL.Query().Get("host"); v != "" {
		f.hosts = csvList(v)
	}
	return f
}

func (f *eventFilter) empty() bool {
	return f.types == nil && f.severities == nil && f.agents == nil && f.hosts == nil
}

// match decodes only the fields it needs and bails out as early as possible.
func (f *eventFilter) match(line []byte) bool {
	if f.empty() {
		return true
	}
	var ev struct {
		Type     string `json:"type"`
		Agent    string `json:"agent"`
		Host     string `json:"host"`
		Severity string `json:"severity"`
	}
	if err := json.Unmarshal(line, &ev); err != nil {
		// Unparseable payloads always pass — better to over-emit than silently drop.
		return true
	}
	if f.types != nil {
		if _, ok := f.types[ev.Type]; !ok {
			return false
		}
	}
	if f.severities != nil {
		if _, ok := f.severities[strings.ToUpper(ev.Severity)]; !ok {
			return false
		}
	}
	if f.agents != nil && !anyContains(ev.Agent, f.agents) {
		return false
	}
	if f.hosts != nil && !anyContains(ev.Host, f.hosts) {
		return false
	}
	return true
}

func csvSet(v string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, p := range strings.Split(v, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out[p] = struct{}{}
		}
	}
	return out
}

func csvList(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func anyContains(s string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}
