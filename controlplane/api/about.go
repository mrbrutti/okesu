package api

import (
	"encoding/json"
	"net/http"
	"runtime"

	"github.com/section9labs/okesu/agent"
)

// AboutInfo describes the build, runtime, and feature flags.
// Visible to every authenticated user (Phase 7 — only the local-admin
// flow is implemented; we don't expose secrets here).
type AboutInfo struct {
	Version    string         `json:"version"`
	GoVersion  string         `json:"go_version"`
	OS         string         `json:"os"`
	Arch       string         `json:"arch"`
	Features   AboutFeatures  `json:"features"`
}

// AboutFeatures lists which optional surfaces are wired up on this CP.
// The UI hides nav items / settings sections for features that are off.
type AboutFeatures struct {
	OIDC               bool `json:"oidc"`
	MgmtPlane          bool `json:"mgmt_plane"`
	Tunnel             bool `json:"tunnel"`
	Deploy             bool `json:"deploy"`
	WebhookIngest      bool `json:"webhook_ingest"`
}

// AboutHandler returns build + capabilities info.
func AboutHandler(features AboutFeatures) http.HandlerFunc {
	info := AboutInfo{
		Version:   agent.Version(),
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		Features:  features,
	}
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(info)
	}
}
