// Node labels API. Phase 22.8 PR β.
//
// Read endpoints are open to any logged-in user (the labels appear
// in node detail views). Mutation is admin-only — gated at the route
// layer in server.go.

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/db"
)

// ListNodeLabelsHandler returns the label map for a node.
// Path: /api/nodes/{id}/labels
func ListNodeLabelsHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := nodeIDFromChi(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		labels, err := store.ListNodeLabels(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, labels)
	}
}

// SetNodeLabelHandler upserts a single label. Body: {key, value}.
// Admin only. PUT semantics — calling twice with the same payload
// is idempotent.
//
// Path: /api/nodes/{id}/labels
func SetNodeLabelHandler(store *db.Store) http.HandlerFunc {
	type req struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := nodeIDFromChi(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var body req
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := store.SetNodeLabel(id, body.Key, body.Value); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// DeleteNodeLabelHandler removes a label.
// Path: /api/nodes/{id}/labels/{key}
func DeleteNodeLabelHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := nodeIDFromChi(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		key := chi.URLParam(r, "key")
		if key == "" {
			http.Error(w, "key required", http.StatusBadRequest)
			return
		}
		if err := store.DeleteNodeLabel(id, key); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// nodeIDFromChi reads {id} and parses as int64. We don't use the
// existing investigationIDFromChi helper because it errors with
// "investigation id" — a confusing message for a node endpoint.
func nodeIDFromChi(r *http.Request) (int64, error) {
	raw := chi.URLParam(r, "id")
	if raw == "" {
		return 0, errors.New("node id required")
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, errors.New("bad node id")
	}
	return id, nil
}
