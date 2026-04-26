package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/section9labs/okesu/controlplane/db"
)

type auditJSON struct {
	ID        int64           `json:"id"`
	Ts        string          `json:"ts"`
	ActorID   int64           `json:"actor_id,omitempty"`
	Email     string          `json:"actor_email,omitempty"`
	Role      string          `json:"actor_role,omitempty"`
	IP        string          `json:"actor_ip,omitempty"`
	Action    string          `json:"action"`
	Target    string          `json:"target,omitempty"`
	Result    string          `json:"result"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
}

// AuditList returns paginated audit log entries.
//
// Query params:
//
//	actor   email exact match
//	action  exact, or "prefix.*" wildcard (e.g. "user.*")
//	target  exact match
//	result  ok | denied | error
//	since   unix-ms
//	until   unix-ms
//	limit   1..1000 (default 100)
//	offset  pagination
func AuditList(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		f := db.AuditFilter{
			ActorEmail: q.Get("actor"),
			Action:     q.Get("action"),
			Target:     q.Get("target"),
			Result:     q.Get("result"),
		}
		f.SinceMs, _ = strconv.ParseInt(q.Get("since"), 10, 64)
		f.UntilMs, _ = strconv.ParseInt(q.Get("until"), 10, 64)
		f.Limit, _ = strconv.Atoi(q.Get("limit"))
		f.Offset, _ = strconv.Atoi(q.Get("offset"))

		rows, err := store.ListAudit(f)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]auditJSON, 0, len(rows))
		for _, row := range rows {
			item := auditJSON{
				ID:     row.ID,
				Ts:     row.Ts.UTC().Format(time.RFC3339),
				Action: row.Action,
				Result: row.Result,
			}
			if row.ActorID.Valid {
				item.ActorID = row.ActorID.Int64
			}
			item.Email = row.ActorEmail.String
			item.Role = row.ActorRole.String
			item.IP = row.ActorIP.String
			item.Target = row.Target.String
			if row.Metadata.Valid && row.Metadata.String != "" {
				item.Metadata = json.RawMessage(row.Metadata.String)
			}
			out = append(out, item)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}
