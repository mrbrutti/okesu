package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/audit"
	"github.com/section9labs/okesu/controlplane/auth"
	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/notify"
)

// NotifyTester is the subset of notify.Worker the test endpoint needs.
type NotifyTester interface {
	SendNow(ctx context.Context, channel db.NotificationChannel, f notify.Finding) error
}

// ── Channels ───────────────────────────────────────────────────────────────

type channelJSON struct {
	ID             int64           `json:"id"`
	Name           string          `json:"name"`
	Type           string          `json:"type"`
	Config         json.RawMessage `json:"config"`
	Enabled        bool            `json:"enabled"`
	CreatedAt      string          `json:"created_at"`
	UpdatedAt      string          `json:"updated_at"`
	CreatedByEmail string          `json:"created_by_email,omitempty"`
}

func toChannelJSON(c *db.NotificationChannel) channelJSON {
	cfg := json.RawMessage(c.Config)
	if len(cfg) == 0 {
		cfg = json.RawMessage("{}")
	}
	return channelJSON{
		ID:             c.ID,
		Name:           c.Name,
		Type:           c.Type,
		Config:         cfg,
		Enabled:        c.Enabled,
		CreatedAt:      c.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:      c.UpdatedAt.UTC().Format(time.RFC3339),
		CreatedByEmail: c.CreatedByEmail.String,
	}
}

func ChannelsList(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		list, err := store.ListChannels()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]channelJSON, 0, len(list))
		for _, c := range list {
			out = append(out, toChannelJSON(c))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

type channelCreateReq struct {
	Name   string          `json:"name"`
	Type   string          `json:"type"`
	Config json.RawMessage `json:"config"`
}

func ChannelCreate(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req channelCreateReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if req.Name == "" || req.Type == "" {
			http.Error(w, "name and type required", http.StatusBadRequest)
			return
		}
		switch req.Type {
		case db.ChannelTypeSlack, db.ChannelTypeEmail, db.ChannelTypeWebhook:
		default:
			http.Error(w, "type must be slack|email|webhook", http.StatusBadRequest)
			return
		}
		cfg := string(req.Config)
		if cfg == "" {
			cfg = "{}"
		}
		actor := actorEmail(r)
		id, err := store.CreateChannel(req.Name, req.Type, cfg, actor)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		c, err := store.ChannelByID(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action:   "notification.channel_create",
			Target:   fmt.Sprintf("channel:%d", id),
			Metadata: map[string]any{"name": req.Name, "type": req.Type},
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toChannelJSON(c))
	}
}

type channelPatchReq struct {
	Name    *string          `json:"name,omitempty"`
	Config  *json.RawMessage `json:"config,omitempty"`
	Enabled *bool            `json:"enabled,omitempty"`
}

func ChannelPatch(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		c, err := store.ChannelByID(id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var req channelPatchReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		name := c.Name
		if req.Name != nil {
			name = *req.Name
		}
		cfg := c.Config
		if req.Config != nil {
			cfg = string(*req.Config)
		}
		enabled := c.Enabled
		if req.Enabled != nil {
			enabled = *req.Enabled
		}
		if err := store.UpdateChannel(id, name, cfg, enabled); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "notification.channel_update",
			Target: fmt.Sprintf("channel:%d", id),
		})
		updated, _ := store.ChannelByID(id)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toChannelJSON(updated))
	}
}

func ChannelDelete(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		if err := store.DeleteChannel(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "notification.channel_delete",
			Target: fmt.Sprintf("channel:%d", id),
		})
		w.WriteHeader(http.StatusNoContent)
	}
}

// ── Channel test ───────────────────────────────────────────────────────────

// ChannelTest sends a synthetic CRITICAL finding to the configured channel.
// Useful for verifying SMTP / Slack URLs without waiting for a real event.
func ChannelTest(store *db.Store, tester NotifyTester) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		c, err := store.ChannelByID(id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		f := notify.Finding{
			Ts:       time.Now().UnixMilli(),
			Agent:    "okesu-cp",
			Host:     "control-plane",
			Severity: "INFO",
			Title:    "Test alert from Okesu Control Plane",
			Resource: "channel:" + c.Name,
			Evidence: "This is a synthetic notification triggered by the Test button.\nIf you can read this, your channel configuration is correct.",
			Action:   "No action required — this is a test.",
			DedupKey: "channel-test",
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		if err := tester.SendNow(ctx, *c, f); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "notification.channel_test",
			Target: fmt.Sprintf("channel:%d", id),
		})
		w.WriteHeader(http.StatusNoContent)
	}
}

// ── Rules ──────────────────────────────────────────────────────────────────

type ruleJSON struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	ChannelID      int64  `json:"channel_id"`
	MinSeverity    string `json:"min_severity"`
	AgentSubstring string `json:"agent_substring,omitempty"`
	HostSubstring  string `json:"host_substring,omitempty"`
	Enabled        bool   `json:"enabled"`
	CreatedAt      string `json:"created_at"`
}

func toRuleJSON(r *db.NotificationRule) ruleJSON {
	return ruleJSON{
		ID:             r.ID,
		Name:           r.Name,
		ChannelID:      r.ChannelID,
		MinSeverity:    r.MinSeverity,
		AgentSubstring: r.AgentSubstring.String,
		HostSubstring:  r.HostSubstring.String,
		Enabled:        r.Enabled,
		CreatedAt:      r.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func RulesList(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		list, err := store.ListRules()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]ruleJSON, 0, len(list))
		for _, r := range list {
			out = append(out, toRuleJSON(r))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

type ruleCreateReq struct {
	Name           string `json:"name"`
	ChannelID      int64  `json:"channel_id"`
	MinSeverity    string `json:"min_severity"`
	AgentSubstring string `json:"agent_substring"`
	HostSubstring  string `json:"host_substring"`
	Enabled        *bool  `json:"enabled"`
}

func RuleCreate(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req ruleCreateReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if req.Name == "" || req.ChannelID == 0 {
			http.Error(w, "name and channel_id required", http.StatusBadRequest)
			return
		}
		enabled := true
		if req.Enabled != nil {
			enabled = *req.Enabled
		}
		ruleRow := &db.NotificationRule{
			Name:           req.Name,
			ChannelID:      req.ChannelID,
			MinSeverity:    req.MinSeverity,
			AgentSubstring: sql.NullString{String: req.AgentSubstring, Valid: req.AgentSubstring != ""},
			HostSubstring:  sql.NullString{String: req.HostSubstring, Valid: req.HostSubstring != ""},
			Enabled:        enabled,
		}
		id, err := store.CreateRule(ruleRow)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "notification.rule_create",
			Target: fmt.Sprintf("rule:%d", id),
			Metadata: map[string]any{
				"name":         req.Name,
				"channel_id":   req.ChannelID,
				"min_severity": req.MinSeverity,
			},
		})
		// Re-read so created_at reflects the DB value rather than zero.
		all, _ := store.ListRules()
		out := ruleRow
		out.ID = id
		for _, x := range all {
			if x.ID == id {
				out = x
				break
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toRuleJSON(out))
	}
}

type rulePatchReq struct {
	Name           *string `json:"name,omitempty"`
	ChannelID      *int64  `json:"channel_id,omitempty"`
	MinSeverity    *string `json:"min_severity,omitempty"`
	AgentSubstring *string `json:"agent_substring,omitempty"`
	HostSubstring  *string `json:"host_substring,omitempty"`
	Enabled        *bool   `json:"enabled,omitempty"`
}

func RulePatch(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		// Find existing.
		all, err := store.ListRules()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var cur *db.NotificationRule
		for _, x := range all {
			if x.ID == id {
				cur = x
				break
			}
		}
		if cur == nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		var req rulePatchReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if req.Name != nil {
			cur.Name = *req.Name
		}
		if req.ChannelID != nil {
			cur.ChannelID = *req.ChannelID
		}
		if req.MinSeverity != nil {
			cur.MinSeverity = *req.MinSeverity
		}
		if req.AgentSubstring != nil {
			cur.AgentSubstring = sql.NullString{String: *req.AgentSubstring, Valid: *req.AgentSubstring != ""}
		}
		if req.HostSubstring != nil {
			cur.HostSubstring = sql.NullString{String: *req.HostSubstring, Valid: *req.HostSubstring != ""}
		}
		if req.Enabled != nil {
			cur.Enabled = *req.Enabled
		}
		if err := store.UpdateRule(cur); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "notification.rule_update",
			Target: fmt.Sprintf("rule:%d", id),
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toRuleJSON(cur))
	}
}

func RuleDelete(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		if err := store.DeleteRule(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "notification.rule_delete",
			Target: fmt.Sprintf("rule:%d", id),
		})
		w.WriteHeader(http.StatusNoContent)
	}
}

// ── Deliveries ─────────────────────────────────────────────────────────────

type deliveryJSON struct {
	ID         int64  `json:"id"`
	RuleID     int64  `json:"rule_id,omitempty"`
	ChannelID  int64  `json:"channel_id,omitempty"`
	FindingID  int64  `json:"finding_id,omitempty"`
	Severity   string `json:"severity,omitempty"`
	Title      string `json:"title,omitempty"`
	Status     string `json:"status"`
	Attempt    int    `json:"attempt"`
	Error      string `json:"error,omitempty"`
	CreatedAt  string `json:"created_at"`
	FinishedAt string `json:"finished_at,omitempty"`
}

func DeliveriesList(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		list, err := store.ListDeliveries(limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]deliveryJSON, 0, len(list))
		for _, d := range list {
			item := deliveryJSON{
				ID:        d.ID,
				Severity:  d.Severity.String,
				Title:     d.Title.String,
				Status:    d.Status,
				Attempt:   d.Attempt,
				Error:     d.Error.String,
				CreatedAt: d.CreatedAt.UTC().Format(time.RFC3339),
			}
			if d.RuleID.Valid {
				item.RuleID = d.RuleID.Int64
			}
			if d.ChannelID.Valid {
				item.ChannelID = d.ChannelID.Int64
			}
			if d.FindingID.Valid {
				item.FindingID = d.FindingID.Int64
			}
			if d.FinishedAt.Valid {
				item.FinishedAt = d.FinishedAt.Time.UTC().Format(time.RFC3339)
			}
			out = append(out, item)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// ── helpers ────────────────────────────────────────────────────────────────

func actorEmail(r *http.Request) string {
	if u := auth.UserFromContext(r.Context()); u != nil {
		return u.Email
	}
	return ""
}
