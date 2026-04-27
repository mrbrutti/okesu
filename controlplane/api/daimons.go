// Package api — daimon library CRUD.
//
// "Daimon" in our model is the long-running, scheduled agent format
// (mode: daemon, with full frontmatter — schedule, mgmt, RBAC, outputs,
// collectors). Files live in --daimon-files-dir and are uploaded onto a
// node when the operator deploys.
//
// The library handlers expose the contents of that directory as a CRUD
// surface so operators can author/edit definitions through the UI
// instead of needing CP-host filesystem access.
package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"

	"github.com/section9labs/okesu/controlplane/audit"
	"github.com/section9labs/okesu/controlplane/auth"
	"github.com/section9labs/okesu/controlplane/db"
)

// daimonSummary is the light-weight per-file shape used in list responses.
// We parse just enough frontmatter to populate the table — full content is
// only loaded on detail/edit fetches.
type daimonSummary struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version,omitempty"`
	Provider    string `json:"provider,omitempty"`
	Model       string `json:"model,omitempty"`
	Mode        string `json:"mode,omitempty"`
	Interval    string `json:"interval,omitempty"`
	ModifiedAt  string `json:"modified_at"`
	SizeBytes   int64  `json:"size_bytes"`
	// Hash is the canonical sha256 of the file content. Daimon Library
	// rows compare this against each registered daemon's
	// current_definition_hash to compute drift ("8 of 10 on current").
	Hash string `json:"hash,omitempty"`
	// PreviousVersion is the operator-set version label of the
	// <name>.previous.md slot — non-empty when a one-click rollback is
	// available. Empty when no previous slot exists (first save) or
	// when the previous file lacked a version frontmatter.
	PreviousVersion  string `json:"previous_version,omitempty"`
	PreviousModified string `json:"previous_modified_at,omitempty"`
}

// daimonDetail extends daimonSummary with the raw file content. Saved
// edits go through PUT with the same shape.
type daimonDetail struct {
	daimonSummary
	Content string `json:"content"`
	// Warnings is populated on save (and only on save) when the new
	// content changes a field that doesn't hot-reload — operators
	// need to restart the daemon for it to take effect. UI surfaces
	// these as a yellow notice next to the Save button.
	Warnings []string `json:"warnings,omitempty"`
}

// frontmatter captures the keys we surface in the list summary. Unknown
// keys are tolerated — yaml.v3 ignores fields that don't map.
type frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Version     string `yaml:"version"`
	Provider    string `yaml:"provider"`
	Model       string `yaml:"model"`
	Mode        string `yaml:"mode"`
	Interval    string `yaml:"interval"`
	StateDir    string `yaml:"stateDir"`
}

// splitFrontmatter pulls the YAML frontmatter from a markdown file.
// Returns ("", body) if no frontmatter is present so we can still display
// the file in the editor.
func splitFrontmatter(content []byte) (yamlStr, body string) {
	s := string(content)
	if !strings.HasPrefix(s, "---") {
		return "", s
	}
	rest := strings.TrimPrefix(s, "---")
	rest = strings.TrimLeft(rest, "\n\r")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", s
	}
	return rest[:end], strings.TrimLeft(rest[end+4:], "\n\r")
}

// validDaimonName enforces a tight name regex so the URL param maps to a
// safe filesystem path. We don't allow path separators or hidden files.
func validDaimonName(name string) bool {
	if name == "" || strings.ContainsAny(name, `/\.\\`) {
		return false
	}
	for _, r := range name {
		if !(r == '-' || r == '_' ||
			(r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

// readDaimonFile loads a single file and returns its summary + content,
// resolving the path against the configured library dir. Returns
// fs.ErrNotExist if the file isn't present.
func readDaimonFile(dir, name string) (daimonDetail, error) {
	path := filepath.Join(dir, name+".md")
	st, err := os.Stat(path)
	if err != nil {
		return daimonDetail{}, err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return daimonDetail{}, err
	}
	yamlStr, _ := splitFrontmatter(content)
	var fm frontmatter
	if yamlStr != "" {
		_ = yaml.Unmarshal([]byte(yamlStr), &fm)
	}
	if fm.Name == "" {
		fm.Name = name
	}
	hash := computeContentHash(content)

	// Inspect <name>.previous.md so the UI knows whether a rollback
	// is possible and what version it would restore to.
	prevVersion, prevModified := readPreviousMeta(dir, name)

	return daimonDetail{
		daimonSummary: daimonSummary{
			Name:             fm.Name,
			Description:      fm.Description,
			Version:          fm.Version,
			Provider:         fm.Provider,
			Model:            fm.Model,
			Mode:             fm.Mode,
			Interval:         fm.Interval,
			ModifiedAt:       st.ModTime().UTC().Format(time.RFC3339),
			SizeBytes:        st.Size(),
			Hash:             hash,
			PreviousVersion:  prevVersion,
			PreviousModified: prevModified,
		},
		Content: string(content),
	}, nil
}

// readPreviousMeta peeks at <name>.previous.md to surface rollback
// availability without requiring the operator to inspect the
// filesystem. Returns ("", "") when no previous slot exists.
func readPreviousMeta(dir, name string) (version, modifiedAt string) {
	path := filepath.Join(dir, name+".previous.md")
	st, err := os.Stat(path)
	if err != nil {
		return "", ""
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return "", st.ModTime().UTC().Format(time.RFC3339)
	}
	if y, _ := splitFrontmatter(body); y != "" {
		var fm frontmatter
		if yaml.Unmarshal([]byte(y), &fm) == nil {
			version = fm.Version
		}
	}
	modifiedAt = st.ModTime().UTC().Format(time.RFC3339)
	return
}

// computeContentHash returns hex(sha256(content)) — same convention used
// by the daemon and the mgmt-plane /config endpoint so all three agree.
func computeContentHash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// DaimonLibraryList handles GET /api/daimons/library.
//
// Returns one summary row per *.md file in the configured library dir.
// 503 if the dir isn't configured (deploy isn't possible without it).
func DaimonLibraryList(daimonFilesDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if daimonFilesDir == "" {
			http.Error(w, "library disabled — pass --daimon-files-dir to enable", http.StatusServiceUnavailable)
			return
		}
		entries, err := os.ReadDir(daimonFilesDir)
		if err != nil {
			http.Error(w, "read library: "+err.Error(), http.StatusInternalServerError)
			return
		}
		out := []daimonSummary{}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			name := strings.TrimSuffix(e.Name(), ".md")
			detail, err := readDaimonFile(daimonFilesDir, name)
			if err != nil {
				continue
			}
			out = append(out, detail.daimonSummary)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// DaimonLibraryGet handles GET /api/daimons/library/{name}.
func DaimonLibraryGet(daimonFilesDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if daimonFilesDir == "" {
			http.Error(w, "library disabled", http.StatusServiceUnavailable)
			return
		}
		name := chi.URLParam(r, "name")
		if !validDaimonName(name) {
			http.Error(w, "invalid name", http.StatusBadRequest)
			return
		}
		detail, err := readDaimonFile(daimonFilesDir, name)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(detail)
	}
}

// DaimonLibraryPut handles PUT /api/daimons/library/{name} — body
// `{"content": "...full markdown file..."}`. Creates or overwrites the
// file. Validates that frontmatter exists, parses, and that the parsed
// `name` matches the URL param. mode=daemon is required (or empty —
// some legacy daimon files omit it).
func DaimonLibraryPut(store *db.Store, daimonFilesDir string) http.HandlerFunc {
	type req struct {
		Content string `json:"content"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if daimonFilesDir == "" {
			http.Error(w, "library disabled", http.StatusServiceUnavailable)
			return
		}
		u := auth.UserFromContext(r.Context())
		if u == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		name := chi.URLParam(r, "name")
		if !validDaimonName(name) {
			http.Error(w, "invalid name", http.StatusBadRequest)
			return
		}
		var body req
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(body.Content) == "" {
			http.Error(w, "content required", http.StatusBadRequest)
			return
		}

		// Parse frontmatter and validate.
		yamlStr, _ := splitFrontmatter([]byte(body.Content))
		if yamlStr == "" {
			http.Error(w, "frontmatter required (--- yaml block at top of file)", http.StatusBadRequest)
			return
		}
		var fm frontmatter
		if err := yaml.Unmarshal([]byte(yamlStr), &fm); err != nil {
			http.Error(w, "frontmatter parse error: "+err.Error(), http.StatusBadRequest)
			return
		}
		if fm.Name != "" && fm.Name != name {
			http.Error(w, fmt.Sprintf("frontmatter name %q does not match url param %q", fm.Name, name), http.StatusBadRequest)
			return
		}
		// Daimons should be daemon-mode by definition. We only validate when
		// the user set the field — empty means "use the daemon default".
		if fm.Mode != "" && fm.Mode != "daemon" {
			http.Error(w, fmt.Sprintf("daimon files must use mode: daemon (got %q)", fm.Mode), http.StatusBadRequest)
			return
		}

		// Ensure target dir exists, then write atomically.
		if err := os.MkdirAll(daimonFilesDir, 0755); err != nil {
			http.Error(w, "mkdir: "+err.Error(), http.StatusInternalServerError)
			return
		}
		path := filepath.Join(daimonFilesDir, name+".md")
		previous := filepath.Join(daimonFilesDir, name+".previous.md")

		// Single-deep "previous" slot — same pattern as Phase 7c's
		// binary update. Before overwriting the live file, copy it to
		// <name>.previous.md so a one-click rollback is possible.
		// Skip when no live file exists yet (first save).
		//
		// While we're inspecting the previous content, also compute
		// restart-required warnings: interval and stateDir don't
		// hot-reload (Phase 7b only reloads model/prompt/tools/etc).
		previousVersion := ""
		var warnings []string
		if existing, err := os.ReadFile(path); err == nil {
			if y, _ := splitFrontmatter(existing); y != "" {
				var pfm frontmatter
				if yaml.Unmarshal([]byte(y), &pfm) == nil {
					previousVersion = pfm.Version
					if pfm.Interval != fm.Interval && (pfm.Interval != "" || fm.Interval != "") {
						warnings = append(warnings, fmt.Sprintf(
							"interval changed (%q → %q) — does NOT hot-reload; restart the daimons on each node for it to take effect.",
							pfm.Interval, fm.Interval,
						))
					}
					if pfm.StateDir != fm.StateDir && (pfm.StateDir != "" || fm.StateDir != "") {
						warnings = append(warnings, fmt.Sprintf(
							"stateDir changed (%q → %q) — does NOT hot-reload; restart the daimons on each node for it to take effect.",
							pfm.StateDir, fm.StateDir,
						))
					}
				}
			}
			_ = os.WriteFile(previous, existing, 0644)
		}

		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, []byte(body.Content), 0644); err != nil {
			http.Error(w, "write: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if err := os.Rename(tmp, path); err != nil {
			_ = os.Remove(tmp)
			http.Error(w, "rename: "+err.Error(), http.StatusInternalServerError)
			return
		}

		audit.Emit(r, store, db.AuditEntry{
			Action: "daimon.library_save",
			Target: "daimon:" + name,
			Metadata: map[string]any{
				"size_bytes":       len(body.Content),
				"provider":         fm.Provider,
				"model":            fm.Model,
				"new_version":      fm.Version,
				"previous_version": previousVersion,
			},
		})

		detail, err := readDaimonFile(daimonFilesDir, name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		detail.Warnings = warnings
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(detail)
	}
}

// DaimonLibraryRollback handles POST /api/daimons/library/{name}/rollback.
//
// Atomically swaps <name>.md and <name>.previous.md. The current file
// becomes the new "previous" so a second rollback brings back what was
// just rolled back from — symmetric, single-deep slot.
//
// Trips the same hot-reload path as a normal save: every registered
// daemon polls /config every ~60s, sees the new definition_hash, and
// fetches /definition. No daemon restart needed.
func DaimonLibraryRollback(store *db.Store, daimonFilesDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if daimonFilesDir == "" {
			http.Error(w, "library disabled", http.StatusServiceUnavailable)
			return
		}
		u := auth.UserFromContext(r.Context())
		if u == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		name := chi.URLParam(r, "name")
		if !validDaimonName(name) {
			http.Error(w, "invalid name", http.StatusBadRequest)
			return
		}
		current := filepath.Join(daimonFilesDir, name+".md")
		previous := filepath.Join(daimonFilesDir, name+".previous.md")
		if _, err := os.Stat(previous); err != nil {
			http.Error(w, "no previous version to roll back to", http.StatusBadRequest)
			return
		}
		// Symmetric swap via a stash file so we don't lose state on
		// partial failure. current → stash, previous → current,
		// stash → previous.
		stash := filepath.Join(daimonFilesDir, name+".swap.tmp")
		if err := os.Rename(current, stash); err != nil {
			http.Error(w, "stash current: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if err := os.Rename(previous, current); err != nil {
			// Try to restore the stash, then surface the failure.
			_ = os.Rename(stash, current)
			http.Error(w, "promote previous: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if err := os.Rename(stash, previous); err != nil {
			http.Error(w, "demote stash: "+err.Error(), http.StatusInternalServerError)
			return
		}

		audit.Emit(r, store, db.AuditEntry{
			Action: "daimon.library_rollback",
			Target: "daimon:" + name,
		})

		detail, err := readDaimonFile(daimonFilesDir, name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(detail)
	}
}

// DaimonLibraryDelete handles DELETE /api/daimons/library/{name}.
//
// Soft-blocks deletion when daimons of this name are still registered
// (the operator should uninstall those first, otherwise their
// re-registration will recreate the row in the agents table without
// the file present and the deploy flow will be inconsistent).
func DaimonLibraryDelete(store *db.Store, daimonFilesDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if daimonFilesDir == "" {
			http.Error(w, "library disabled", http.StatusServiceUnavailable)
			return
		}
		u := auth.UserFromContext(r.Context())
		if u == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		name := chi.URLParam(r, "name")
		if !validDaimonName(name) {
			http.Error(w, "invalid name", http.StatusBadRequest)
			return
		}

		// Block if any registered daimon (across hosts) still uses this name.
		// AgentByName falls back to most-recent across all hosts, so a single
		// hit means at least one is registered.
		if _, err := store.AgentByName(name); err == nil {
			http.Error(w, fmt.Sprintf("cannot delete %q — registered daimons still exist; uninstall first", name), http.StatusConflict)
			return
		}

		path := filepath.Join(daimonFilesDir, name+".md")
		if err := os.Remove(path); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "daimon.library_delete",
			Target: "daimon:" + name,
		})
		w.WriteHeader(http.StatusNoContent)
	}
}
