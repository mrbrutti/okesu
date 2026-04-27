package api

import (
	"encoding/json"
	"net/http"
	"runtime"
)

// AboutInfo describes the build, runtime, and feature flags.
// Visible to every authenticated user (Phase 7 — only the local-admin
// flow is implemented; we don't expose secrets here).
type AboutInfo struct {
	Version          string        `json:"version"`           // okesu-cp binary version
	DaemonVersion    string        `json:"daemon_version"`    // okesu-daemon binary the CP would push on a deploy / update
	GoVersion        string        `json:"go_version"`
	OS               string        `json:"os"`
	Arch             string        `json:"arch"`
	Features         AboutFeatures `json:"features"`
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

// AboutHandler returns build + capabilities info. The cpVersion arg is
// the okesu-cp binary's own version (passed from controlplane.Version()
// at wire-up time to avoid a circular import). daemonVersionFn returns
// the version of the daemon binary the CP would push during a deploy
// or update — typically lazily resolved by introspecting the file at
// --daemon-binary or --daemon-binaries-dir.
func AboutHandler(cpVersion string, daemonVersionFn func() string, features AboutFeatures) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		dv := ""
		if daemonVersionFn != nil {
			dv = daemonVersionFn()
		}
		info := AboutInfo{
			Version:       cpVersion,
			DaemonVersion: dv,
			GoVersion:     runtime.Version(),
			OS:            runtime.GOOS,
			Arch:          runtime.GOARCH,
			Features:      features,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(info)
	}
}
