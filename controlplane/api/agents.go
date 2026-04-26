package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/audit"
	"github.com/section9labs/okesu/controlplane/db"
)

// agentJSON is the wire shape returned by the UI-facing agents API.
type agentJSON struct {
	Name             string `json:"name"`
	Host             string `json:"host"`
	Provider         string `json:"provider,omitempty"`
	Model            string `json:"model,omitempty"`
	Version          string `json:"version,omitempty"`
	RegisteredAt     string `json:"registered_at"`
	LastHeartbeatAt  string `json:"last_heartbeat_at,omitempty"`
	LastTickCount    int64  `json:"last_tick_count"`
	HeartbeatAgeSec  int64  `json:"heartbeat_age_sec"`
	Healthy          bool   `json:"healthy"`
	DesiredMaxTurns  *int64 `json:"desired_max_turns,omitempty"`
	DesiredEffort    string `json:"desired_effort,omitempty"`
	DesiredSuspended bool   `json:"desired_suspended"`
	ConfigUpdatedAt  string `json:"config_updated_at,omitempty"`
}

// AgentsList returns registered agents, paginated.
// GET /api/agents?limit=N&offset=N
func AgentsList(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		agents, err := store.ListAgents(limit, offset)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]agentJSON, 0, len(agents))
		for _, a := range agents {
			out = append(out, toAgentJSON(a))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// AgentDetail returns a single agent.
// GET /api/agents/{name}
func AgentDetail(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		a, err := store.AgentByName(name)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toAgentJSON(a))
	}
}

// AgentConfigUpdate sets the desired runtime config for an agent.
// PATCH /api/agents/{name}/config
type configPatch struct {
	MaxTurns  *int64  `json:"max_turns,omitempty"`
	Effort    *string `json:"effort,omitempty"`
	Suspended *bool   `json:"suspended,omitempty"`
}

func AgentConfigUpdate(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		var p configPatch
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if err := store.SetDesiredConfig(name, p.MaxTurns, p.Effort, p.Suspended); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Return the updated record.
		a, err := store.AgentByName(name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "agent.config_update",
			Target: fmt.Sprintf("agent:%s", name),
			Metadata: map[string]any{
				"max_turns": p.MaxTurns,
				"effort":    p.Effort,
				"suspended": p.Suspended,
			},
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toAgentJSON(a))
	}
}

func toAgentJSON(a *db.Agent) agentJSON {
	out := agentJSON{
		Name:             a.Name,
		Host:             a.Host,
		Provider:         a.Provider.String,
		Model:            a.Model.String,
		Version:          a.Version.String,
		RegisteredAt:     a.RegisteredAt.UTC().Format(time.RFC3339),
		LastTickCount:    a.LastTickCount,
		DesiredEffort:    a.DesiredEffort.String,
		DesiredSuspended: a.DesiredSuspended,
	}
	if a.LastHeartbeatAt.Valid {
		out.LastHeartbeatAt = a.LastHeartbeatAt.Time.UTC().Format(time.RFC3339)
		age := time.Since(a.LastHeartbeatAt.Time)
		out.HeartbeatAgeSec = int64(age.Seconds())
		// Healthy if heartbeat seen in the last 5 minutes.
		out.Healthy = age < 5*time.Minute
	}
	if a.DesiredMaxTurns.Valid {
		v := a.DesiredMaxTurns.Int64
		out.DesiredMaxTurns = &v
	}
	if a.ConfigUpdatedAt.Valid {
		out.ConfigUpdatedAt = a.ConfigUpdatedAt.Time.UTC().Format(time.RFC3339)
	}
	return out
}
