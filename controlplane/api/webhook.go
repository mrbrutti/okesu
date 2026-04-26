// Package api implements the HTTP handlers for the Control Plane.
package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/section9labs/okesu/agent"
	"github.com/section9labs/okesu/controlplane/db"
)

// Broadcaster is the subset of controlplane.Broadcaster needed by webhook.
type Broadcaster interface {
	Publish(line []byte)
}

// WebhookHandler returns an http.HandlerFunc that accepts daemon webhook
// events. It verifies HMAC-SHA256 signatures (matching agent/sinks.go),
// persists each event, and re-broadcasts it to live subscribers.
func WebhookHandler(store *db.Store, secret string, bcast Broadcaster) http.HandlerFunc {
	const maxBodyBytes = 1 << 20 // 1 MiB
	const maxClockSkew = 5 * time.Minute

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}

		// Verify timestamp is within tolerable skew.
		if tsHdr := r.Header.Get("X-Okesu-Timestamp"); tsHdr != "" {
			tsMs, err := strconv.ParseInt(tsHdr, 10, 64)
			if err == nil {
				ts := time.UnixMilli(tsMs)
				if delta := time.Since(ts); delta > maxClockSkew || delta < -maxClockSkew {
					http.Error(w, "timestamp skew", http.StatusUnauthorized)
					return
				}
			}
		}

		// Verify HMAC-SHA256 signature against the raw body.
		if !verifySignature(body, r.Header.Get("X-Okesu-Signature"), secret) {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}

		agentName := r.Header.Get("X-Okesu-Agent")
		host := r.Header.Get("X-Okesu-Host")

		// Body is NDJSON — parse one line at a time.
		count := 0
		for _, line := range splitJSONL(body) {
			if len(line) == 0 {
				continue
			}
			ev, err := parseEvent(line)
			if err != nil {
				http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
				return
			}
			// Headers override missing fields in the event.
			if ev.Agent == "" {
				ev.Agent = agentName
			}
			if ev.Host == "" {
				ev.Host = host
			}
			row := &db.Event{
				Ts:       ev.Ts,
				Type:     ev.Type,
				Agent:    sql.NullString{String: ev.Agent, Valid: ev.Agent != ""},
				Host:     sql.NullString{String: ev.Host, Valid: ev.Host != ""},
				Severity: sql.NullString{String: ev.Severity, Valid: ev.Severity != ""},
				Title:    sql.NullString{String: ev.Title, Valid: ev.Title != ""},
				RawJSON:  string(line),
			}
			eventID, err := store.InsertEvent(row)
			if err != nil {
				http.Error(w, "store: "+err.Error(), http.StatusInternalServerError)
				return
			}

			// Project finding events into the structured findings table.
			// Title is re-normalized as a defense-in-depth — older daemon
			// builds and external producers via /api/findings/ingest may
			// not have run the harvester's normalization.
			if ev.Type == "finding" {
				_, _ = store.InsertFinding(&db.FindingInsert{
					EventID:         eventID,
					Ts:              ev.Ts,
					Agent:           ev.Agent,
					Host:            ev.Host,
					Severity:        ev.Severity,
					Title:           agent.NormalizeFindingTitle(ev.Title),
					Resource:        ev.Resource,
					Evidence:        ev.Evidence,
					DedupKey:        ev.DedupKey,
					RawJSON:         string(line),
					Category:        ev.Category,
					ProcessPID:      ev.ProcessPID,
					ProcessName:     ev.ProcessName,
					Path:            ev.Path,
					NetworkEndpoint: ev.NetworkEndpoint,
					CVE:             ev.CVE,
					Tags:            ev.Tags,
					Attributes:      string(ev.Attributes),
				})
			}
			bcast.Publish(line)
			count++
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"accepted":` + strconv.Itoa(count) + `}`))
	}
}

// minimalEvent has just the fields the CP indexes; the full body is preserved
// in raw_json. This lets the CP accept any event shape the daemon emits without
// schema-coupling to agent.Event.
type minimalEvent struct {
	Type     string `json:"type"`
	Ts       int64  `json:"ts"`
	Agent    string `json:"agent"`
	Host     string `json:"host"`
	Severity string `json:"severity"`
	Title    string `json:"title"`

	// finding-only fields
	Resource string `json:"resource"`
	Evidence string `json:"evidence"`
	DedupKey string `json:"dedup_key"`

	// Phase 12 finding enrichment.
	Category        string          `json:"category"`
	ProcessPID      int64           `json:"process_pid"`
	ProcessName     string          `json:"process_name"`
	Path            string          `json:"path"`
	NetworkEndpoint string          `json:"network_endpoint"`
	CVE             string          `json:"cve"`
	Tags            string          `json:"tags"`
	Attributes      json.RawMessage `json:"attributes"`
}

func parseEvent(line []byte) (*minimalEvent, error) {
	e := &minimalEvent{}
	if err := json.Unmarshal(line, e); err != nil {
		return nil, err
	}
	if e.Ts == 0 {
		e.Ts = time.Now().UnixMilli()
	}
	if e.Type == "" {
		e.Type = "unknown"
	}
	return e, nil
}

func splitJSONL(body []byte) [][]byte {
	// Common case: one event = one POST = no newlines.
	if !containsByte(body, '\n') {
		return [][]byte{trim(body)}
	}
	var out [][]byte
	for _, line := range splitBytes(body, '\n') {
		if t := trim(line); len(t) > 0 {
			out = append(out, t)
		}
	}
	return out
}

func verifySignature(body []byte, sigHeader, secret string) bool {
	if sigHeader == "" || secret == "" {
		return false
	}
	const prefix = "sha256="
	if !strings.HasPrefix(sigHeader, prefix) {
		return false
	}
	want, err := hex.DecodeString(sigHeader[len(prefix):])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	got := mac.Sum(nil)
	return hmac.Equal(want, got)
}

func containsByte(b []byte, c byte) bool {
	for _, x := range b {
		if x == c {
			return true
		}
	}
	return false
}

func splitBytes(b []byte, sep byte) [][]byte {
	var out [][]byte
	start := 0
	for i, c := range b {
		if c == sep {
			out = append(out, b[start:i])
			start = i + 1
		}
	}
	out = append(out, b[start:])
	return out
}

func trim(b []byte) []byte {
	i, j := 0, len(b)
	for i < j && (b[i] == ' ' || b[i] == '\r' || b[i] == '\t') {
		i++
	}
	for j > i && (b[j-1] == ' ' || b[j-1] == '\r' || b[j-1] == '\t' || b[j-1] == '\n') {
		j--
	}
	return b[i:j]
}
