// Finalize handler — converts a war-room draft into one immutable
// InvestigationNote row, broadcasts session-end to all connected
// clients, tears down the room.
package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/federation"
)

type finalizeRequest struct {
	Author string `json:"author"`
}

type finalizeResponse struct {
	NoteID int64 `json:"note_id"`
}

// GetInvestigationDraftFinalizeHandler is the local handler.
func GetInvestigationDraftFinalizeHandler(store *db.Store, hub *RelayHub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		invID, err := investigationIDFromChi(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if _, err := store.GetInvestigation(invID); err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		var req finalizeRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		author := strings.TrimSpace(req.Author)
		if author == "" {
			author = userIdentityFromContext(r.Context())
		}
		if author == "" {
			author = "operator"
		}

		// Try to get a snapshot from a live in-memory room first;
		// fall back to the persisted DB snapshot.
		var (
			snapshot []byte
			liveRoom *room
		)
		hub.mu.RLock()
		liveRoom = hub.rooms[invID]
		hub.mu.RUnlock()

		if liveRoom != nil {
			if !liveRoom.MarkFinalizing() {
				http.Error(w, "another finalize is in flight", http.StatusConflict)
				return
			}
			snapshot = liveRoom.SnapshotBytes()
			// If the in-memory snapshot is empty, fall back to the DB.
			if len(snapshot) == 0 {
				if b, dbErr := store.GetInvestigationNoteDraft(invID); dbErr == nil {
					snapshot = b
				}
			}
		} else {
			b, err := store.GetInvestigationNoteDraft(invID)
			if err != nil {
				http.Error(w, "no draft to finalize", http.StatusNotFound)
				return
			}
			snapshot = b
		}

		body, err := decodeYTextBody(snapshot, "body")
		if err != nil {
			http.Error(w, "decode draft body: "+err.Error(), http.StatusInternalServerError)
			return
		}
		body = strings.TrimSpace(body)
		if body == "" {
			http.Error(w, "draft is empty", http.StatusBadRequest)
			return
		}

		noteID, err := store.AddInvestigationNote(invID, author, body)
		if err != nil {
			http.Error(w, "add note: "+err.Error(), http.StatusInternalServerError)
			return
		}
		_ = store.DeleteInvestigationNoteDraft(invID)

		// Broadcast session-end to all connected clients and tear
		// down the room.
		if liveRoom != nil {
			endFrame := []byte{2}
			endFrame = append(endFrame, []byte(`{"note_id":`+strconv.FormatInt(noteID, 10)+`}`)...)
			liveRoom.broadcastAll(endFrame)
			hub.removeRoom(invID)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(finalizeResponse{NoteID: noteID})
	}
}

// FederatedInvestigationDraftFinalize — parent-side wrapper using the
// existing JSON proxy pattern.
func FederatedInvestigationDraftFinalize(store *db.Store, hub *RelayHub, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.Replace(r.URL.Path, "/api/investigations/", "/api/v1/federation/investigations/", 1)
		if handled, _ := proxyToCPByQueryPost(w, r, agg, path); handled {
			return
		}
		GetInvestigationDraftFinalizeHandler(store, hub).ServeHTTP(w, r)
	}
}

// FederationInvestigationDraftFinalize — child-side, token-authed.
func FederationInvestigationDraftFinalize(store *db.Store, hub *RelayHub) http.HandlerFunc {
	return requireFederationToken(store, GetInvestigationDraftFinalizeHandler(store, hub))
}
