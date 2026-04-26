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
	Provider    string `json:"provider,omitempty"`
	Model       string `json:"model,omitempty"`
	Mode        string `json:"mode,omitempty"`
	Interval    string `json:"interval,omitempty"`
	ModifiedAt  string `json:"modified_at"`
	SizeBytes   int64  `json:"size_bytes"`
}

// daimonDetail extends daimonSummary with the raw file content. Saved
// edits go through PUT with the same shape.
type daimonDetail struct {
	daimonSummary
	Content string `json:"content"`
}

// frontmatter captures the keys we surface in the list summary. Unknown
// keys are tolerated — yaml.v3 ignores fields that don't map.
type frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Provider    string `yaml:"provider"`
	Model       string `yaml:"model"`
	Mode        string `yaml:"mode"`
	Interval    string `yaml:"interval"`
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
	return daimonDetail{
		daimonSummary: daimonSummary{
			Name:        fm.Name,
			Description: fm.Description,
			Provider:    fm.Provider,
			Model:       fm.Model,
			Mode:        fm.Mode,
			Interval:    fm.Interval,
			ModifiedAt:  st.ModTime().UTC().Format(time.RFC3339),
			SizeBytes:   st.Size(),
		},
		Content: string(content),
	}, nil
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
				"size_bytes": len(body.Content),
				"provider":   fm.Provider,
				"model":      fm.Model,
			},
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
