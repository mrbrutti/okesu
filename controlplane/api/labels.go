// Generic labels API. Phase 22.9.
//
// Pairs with controlplane/db/labels.go: one CRUD surface per kind via
// path-templated endpoints rather than per-kind handlers. The kind
// is part of the path so the route layer can apply per-kind RBAC at
// the gate (admin-only on most; nodes already shipped with a finer
// RBAC story that we'll preserve).
//
// Identity in the path: {id_or_key} is parsed as int64 first; on
// failure it's treated as a string target_key (used by daimon =
// "name@host", federation peer = instance UUID, etc.).
//
// Selector search is read-only and admin-gated — selector strings
// are easy to fingerprint a fleet with (e.g., "env=prod" reveals
// the prod-tagged surface area). Operator and viewer roles can list
// labels on entities they already see, but can't enumerate by
// selector.

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/audit"
	"github.com/section9labs/okesu/controlplane/db"
)

// allowedLabelKinds gates which target_kind strings are accepted.
// Adding a new kind needs (1) a const in db/labels.go, (2) the kind
// added here, (3) the per-entity wiring (label strip on the detail
// page, batch lookup on the list page).
var allowedLabelKinds = map[string]bool{
	db.LabelKindNode:          true,
	db.LabelKindDaimon:        true,
	db.LabelKindFinding:       true,
	db.LabelKindInvestigation: true,
	db.LabelKindRun:           true,
	db.LabelKindOrchestration: true,
	db.LabelKindCP:            true,
	db.LabelKindSecret:        true,
	db.LabelKindGroup:         true,
}

// parseLabelTarget extracts the {kind} + {id_or_key} URL params and
// returns the (kind, target_id, target_key) tuple the store helpers
// expect. The id_or_key is parsed as int64 first; on failure it's
// used as the string target_key.
func parseLabelTarget(r *http.Request) (kind string, id int64, key string, err error) {
	kind = chi.URLParam(r, "kind")
	if !allowedLabelKinds[kind] {
		return "", 0, "", fmt.Errorf("unsupported label kind %q", kind)
	}
	idOrKey := chi.URLParam(r, "id")
	if idOrKey == "" {
		return "", 0, "", errors.New("target id or key required")
	}
	if n, perr := strconv.ParseInt(idOrKey, 10, 64); perr == nil {
		return kind, n, "", nil
	}
	return kind, 0, idOrKey, nil
}

// ListLabelsHandler — GET /api/labels/{kind}/{id_or_key}
func ListLabelsHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kind, id, key, err := parseLabelTarget(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		labels, err := store.ListLabels(kind, id, key)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, labels)
	}
}

// SetLabelHandler — PUT /api/labels/{kind}/{id_or_key}
// Body: {key, value, source?}
func SetLabelHandler(store *db.Store) http.HandlerFunc {
	type req struct {
		Key    string `json:"key"`
		Value  string `json:"value"`
		Source string `json:"source"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		kind, id, targetKey, err := parseLabelTarget(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var body req
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := store.SetLabel(kind, id, targetKey, body.Key, body.Value, body.Source); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "label.set",
			Target: fmt.Sprintf("%s:%s", kind, targetIdent(id, targetKey)),
			Metadata: map[string]any{
				"key":    body.Key,
				"value":  body.Value,
				"source": body.Source,
			},
		})
		w.WriteHeader(http.StatusNoContent)
	}
}

// DeleteLabelHandler — DELETE /api/labels/{kind}/{id_or_key}/{key}
func DeleteLabelHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kind, id, targetKey, err := parseLabelTarget(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		key := chi.URLParam(r, "key")
		if key == "" {
			http.Error(w, "label key required", http.StatusBadRequest)
			return
		}
		if err := store.DeleteLabel(kind, id, targetKey, key); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "label.delete",
			Target: fmt.Sprintf("%s:%s", kind, targetIdent(id, targetKey)),
			Metadata: map[string]any{"key": key},
		})
		w.WriteHeader(http.StatusNoContent)
	}
}

// SearchLabelsHandler — GET /api/labels/search?kind=&selector=
// Returns the identity tuples matching the selector. Admin-only at
// the route layer (selectors are a fingerprintable surface).
func SearchLabelsHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kind := r.URL.Query().Get("kind")
		if !allowedLabelKinds[kind] {
			http.Error(w, "kind required", http.StatusBadRequest)
			return
		}
		raw := r.URL.Query().Get("selector")
		sel, err := db.ParseSelector(raw)
		if err != nil {
			http.Error(w, "bad selector: "+err.Error(), http.StatusBadRequest)
			return
		}
		targets, err := store.FindTargetsBySelector(kind, sel)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, targets)
	}
}

// ListAllLabelsHandler — GET /api/labels/all?kind=&key=&value=&limit=
// Returns every label row matching the filters. Drives the
// Settings → Labels admin page. Admin-only at the route layer
// (selectors are a fingerprintable surface).
func ListAllLabelsHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		f := db.LabelFilter{
			Kind:  q.Get("kind"),
			Key:   q.Get("key"),
			Value: q.Get("value"),
		}
		if f.Kind != "" && !allowedLabelKinds[f.Kind] {
			http.Error(w, "unsupported kind", http.StatusBadRequest)
			return
		}
		if v := q.Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				f.Limit = n
			}
		}
		rows, err := store.ListAllLabels(f)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, rows)
	}
}

// LabelKeysHandler — GET /api/labels/keys?kind=
// Distinct label keys for autocomplete.
func LabelKeysHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kind := r.URL.Query().Get("kind")
		if !allowedLabelKinds[kind] {
			http.Error(w, "kind required", http.StatusBadRequest)
			return
		}
		keys, err := store.DistinctLabelKeys(kind)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, keys)
	}
}

// LabelValuesHandler — GET /api/labels/values?kind=&key=
// Distinct values for one (kind, key) pair, for autocomplete.
func LabelValuesHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kind := r.URL.Query().Get("kind")
		key := r.URL.Query().Get("key")
		if !allowedLabelKinds[kind] || key == "" {
			http.Error(w, "kind and key required", http.StatusBadRequest)
			return
		}
		values, err := store.DistinctLabelValues(kind, key)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, values)
	}
}

// targetIdent renders the audit-log target portion: numeric id when
// present, else the string key.
func targetIdent(id int64, key string) string {
	if id != 0 {
		return strconv.FormatInt(id, 10)
	}
	return key
}
