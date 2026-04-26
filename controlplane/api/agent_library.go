// Package api — agent library CRUD.
//
// "Agent" in our model is the short-form Claude Code / Codex format
// (small frontmatter — name, description, model, tools, maxTurns,
// effort). These are used for one-shot Runs, not deployed as daimons.
//
// Search-path semantics: each list/get walks the configured search dirs
// in order, first match wins. Writes go to a "primary" dir — by default
// the first dir in the list — but the wire shape includes a `dir` field
// so the UI can show which directory each file lives in.
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

// agentSummary is the per-file shape returned by the list endpoint.
type agentSummary struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Provider    string `json:"provider,omitempty"`
	Model       string `json:"model,omitempty"`
	MaxTurns    int    `json:"max_turns,omitempty"`
	Effort      string `json:"effort,omitempty"`
	Dir         string `json:"dir"`
	ModifiedAt  string `json:"modified_at"`
	SizeBytes   int64  `json:"size_bytes"`
}

type agentDetail struct {
	agentSummary
	Content string `json:"content"`
}

type agentFrontmatter struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Provider    string   `yaml:"provider"`
	Model       string   `yaml:"model"`
	MaxTurns    int      `yaml:"maxTurns"`
	Effort      string   `yaml:"effort"`
	Tools       []string `yaml:"tools"`
	Mode        string   `yaml:"mode"`
}

func validAgentName(name string) bool {
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

// findAgentFile walks the search dirs and returns the first matching
// path, or fs.ErrNotExist if none exists.
func findAgentFile(searchDirs []string, name string) (string, error) {
	for _, d := range searchDirs {
		if d == "" {
			continue
		}
		p := filepath.Join(d, name+".md")
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", fs.ErrNotExist
}

func parseAgentFile(path string) (agentDetail, error) {
	st, err := os.Stat(path)
	if err != nil {
		return agentDetail{}, err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return agentDetail{}, err
	}
	yamlStr, _ := splitFrontmatter(content)
	var fm agentFrontmatter
	if yamlStr != "" {
		_ = yaml.Unmarshal([]byte(yamlStr), &fm)
	}
	if fm.Name == "" {
		fm.Name = strings.TrimSuffix(filepath.Base(path), ".md")
	}
	return agentDetail{
		agentSummary: agentSummary{
			Name:        fm.Name,
			Description: fm.Description,
			Provider:    fm.Provider,
			Model:       fm.Model,
			MaxTurns:    fm.MaxTurns,
			Effort:      fm.Effort,
			Dir:         filepath.Dir(path),
			ModifiedAt:  st.ModTime().UTC().Format(time.RFC3339),
			SizeBytes:   st.Size(),
		},
		Content: string(content),
	}, nil
}

// AgentLibraryList handles GET /api/agent-library.
//
// Walks every configured search dir, collects *.md files, dedupes by
// name (first match wins so search-path ordering is the override).
func AgentLibraryList(searchDirs []string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		seen := make(map[string]bool)
		out := []agentSummary{}
		for _, d := range searchDirs {
			if d == "" {
				continue
			}
			entries, err := os.ReadDir(d)
			if err != nil {
				continue // missing dir is fine — operator might not use it
			}
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
					continue
				}
				name := strings.TrimSuffix(e.Name(), ".md")
				if seen[name] {
					continue
				}
				detail, err := parseAgentFile(filepath.Join(d, e.Name()))
				if err != nil {
					continue
				}
				seen[name] = true
				out = append(out, detail.agentSummary)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// AgentLibraryGet handles GET /api/agent-library/{name}.
func AgentLibraryGet(searchDirs []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		if !validAgentName(name) {
			http.Error(w, "invalid name", http.StatusBadRequest)
			return
		}
		path, err := findAgentFile(searchDirs, name)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		detail, err := parseAgentFile(path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(detail)
	}
}

// primaryWriteDir picks the first existing writable search dir, or the
// first listed dir (creating it if needed). New files go here.
func primaryWriteDir(searchDirs []string) (string, error) {
	for _, d := range searchDirs {
		if d == "" {
			continue
		}
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			return d, nil
		}
	}
	for _, d := range searchDirs {
		if d == "" {
			continue
		}
		if err := os.MkdirAll(d, 0755); err == nil {
			return d, nil
		}
	}
	return "", fmt.Errorf("no writable agent directory available — pass --agent-files-dir or ensure ~/.claude/agents exists")
}

// AgentLibraryPut handles PUT /api/agent-library/{name} — body
// `{"content": "..."}`. If the file already exists in any search dir,
// updates IN PLACE (preserves which directory the file lives in). Otherwise
// writes to the first existing search dir, falling back to creating
// the first listed dir.
func AgentLibraryPut(store *db.Store, searchDirs []string) http.HandlerFunc {
	type req struct {
		Content string `json:"content"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		u := auth.UserFromContext(r.Context())
		if u == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		name := chi.URLParam(r, "name")
		if !validAgentName(name) {
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

		yamlStr, _ := splitFrontmatter([]byte(body.Content))
		if yamlStr == "" {
			http.Error(w, "frontmatter required (--- yaml block at top of file)", http.StatusBadRequest)
			return
		}
		var fm agentFrontmatter
		if err := yaml.Unmarshal([]byte(yamlStr), &fm); err != nil {
			http.Error(w, "frontmatter parse error: "+err.Error(), http.StatusBadRequest)
			return
		}
		if fm.Name != "" && fm.Name != name {
			http.Error(w, fmt.Sprintf("frontmatter name %q does not match url param %q", fm.Name, name), http.StatusBadRequest)
			return
		}
		// Agents are NOT daimons — block mode: daemon to keep the libraries
		// distinct.  Operators trying to add a long-form file should put
		// it in the daimon library instead.
		if fm.Mode == "daemon" {
			http.Error(w, "files with mode: daemon belong in the Daimon Library, not the Agent Library", http.StatusBadRequest)
			return
		}

		// Pick the target path: keep the existing file's location if any,
		// otherwise write to the primary dir.
		target, err := findAgentFile(searchDirs, name)
		if err != nil {
			dir, derr := primaryWriteDir(searchDirs)
			if derr != nil {
				http.Error(w, derr.Error(), http.StatusInternalServerError)
				return
			}
			target = filepath.Join(dir, name+".md")
		}
		tmp := target + ".tmp"
		if err := os.WriteFile(tmp, []byte(body.Content), 0644); err != nil {
			http.Error(w, "write: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if err := os.Rename(tmp, target); err != nil {
			_ = os.Remove(tmp)
			http.Error(w, "rename: "+err.Error(), http.StatusInternalServerError)
			return
		}

		audit.Emit(r, store, db.AuditEntry{
			Action: "agent.library_save",
			Target: "agent:" + name,
			Metadata: map[string]any{
				"path":       target,
				"size_bytes": len(body.Content),
				"provider":   fm.Provider,
				"model":      fm.Model,
			},
		})

		detail, err := parseAgentFile(target)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(detail)
	}
}

// AgentLibraryDelete handles DELETE /api/agent-library/{name}.
//
// Deletes whichever copy was found first by the search-path walk. Other
// copies in lower-priority dirs (e.g. a default `~/.claude/agents`
// shipped by Claude Code) become visible after delete, which is the
// behavior operators intuitively expect.
func AgentLibraryDelete(store *db.Store, searchDirs []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := auth.UserFromContext(r.Context())
		if u == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		name := chi.URLParam(r, "name")
		if !validAgentName(name) {
			http.Error(w, "invalid name", http.StatusBadRequest)
			return
		}
		path, err := findAgentFile(searchDirs, name)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if err := os.Remove(path); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "agent.library_delete",
			Target: "agent:" + name,
			Metadata: map[string]any{"path": path},
		})
		w.WriteHeader(http.StatusNoContent)
	}
}
