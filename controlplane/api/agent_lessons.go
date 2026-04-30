package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/section9labs/okesu/controlplane/db"
)

// ListAgentLessonsHandler returns the agent's most recent lessons,
// newest first. Bounded by db.MaxAgentLessonsPerAgent server-side.
//
// Path: /api/agents/{name}/lessons (UI plane) or
//
//	/api/v1/agents/{name}/lessons (mgmt plane). The {name} segment
//
// is whatever sits between "agents/" and "/lessons". Daemons hit this
// on each tick to refresh their per-agent lesson set.
//
// The handler parses the path manually rather than relying on chi's
// URLParam so the same handler works in unit tests (which call it
// directly without the chi router) and in production (which mounts it
// behind chi-style {name} URL params on the mgmt plane).
func ListAgentLessonsHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := extractAgentNameFromLessonsPath(r.URL.Path)
		if name == "" || strings.Contains(name, "/") {
			http.Error(w, "agent name is required", http.StatusBadRequest)
			return
		}
		lessons, err := store.ListAgentLessons(name, db.MaxAgentLessonsPerAgent)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(lessons)
	}
}

// extractAgentNameFromLessonsPath finds the segment between "agents/"
// and "/lessons" in the request path. Works for both UI-plane
// ("/api/agents/foo/lessons") and mgmt-plane ("/api/v1/agents/foo/lessons")
// mounts. Returns "" if the path doesn't match the shape.
func extractAgentNameFromLessonsPath(p string) string {
	const marker = "/agents/"
	idx := strings.Index(p, marker)
	if idx < 0 {
		return ""
	}
	rest := p[idx+len(marker):]
	rest = strings.TrimSuffix(rest, "/lessons")
	return strings.TrimSpace(rest)
}
