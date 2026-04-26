package api

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/audit"
	"github.com/section9labs/okesu/controlplane/auth"
	"github.com/section9labs/okesu/controlplane/db"
)

// ── daemon binaries (admin-only management) ────────────────────────────────

type binaryJSON struct {
	Name       string `json:"name"`
	OS         string `json:"os"`
	Arch       string `json:"arch"`
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
	Size       int64  `json:"size_bytes"`
	UploadedAt string `json:"uploaded_at"`
	UploadedBy string `json:"uploaded_by_email,omitempty"`
}

func toBinaryJSON(b *db.DaemonBinary) binaryJSON {
	return binaryJSON{
		Name:       b.Name,
		OS:         b.OS,
		Arch:       b.Arch,
		Path:       b.Path,
		SHA256:     b.SHA256,
		Size:       b.SizeBytes,
		UploadedAt: b.UploadedAt.UTC().Format(time.RFC3339),
		UploadedBy: b.UploadedByEmail.String,
	}
}

// BinariesList — GET /api/deploy/binaries (viewer+).
func BinariesList(store *db.Store, dirHint string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		bins, err := store.ListDaemonBinaries()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := struct {
			Dir      string       `json:"dir"`
			Binaries []binaryJSON `json:"binaries"`
		}{Dir: dirHint, Binaries: make([]binaryJSON, 0, len(bins))}
		for _, b := range bins {
			out.Binaries = append(out.Binaries, toBinaryJSON(b))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// BinaryUpload — POST /api/deploy/binaries (admin).
//
// multipart/form-data with fields:
//
//	os    text     "linux" / "darwin"
//	arch  text     "amd64" / "arm64" / ...
//	file  file     the binary
//
// Stored as `okesu-<os>-<arch>` under the configured DaemonBinariesDir.
func BinaryUpload(store *db.Store, dir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if dir == "" {
			http.Error(w, "--daemon-binaries-dir is not configured on this CP", http.StatusServiceUnavailable)
			return
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			http.Error(w, "mkdir: "+err.Error(), http.StatusInternalServerError)
			return
		}
		// 50 MiB cap on uploaded binary.
		if err := r.ParseMultipartForm(50 << 20); err != nil {
			http.Error(w, "parse multipart: "+err.Error(), http.StatusBadRequest)
			return
		}
		osName := r.FormValue("os")
		arch := r.FormValue("arch")
		if osName == "" || arch == "" {
			http.Error(w, "os and arch are required", http.StatusBadRequest)
			return
		}

		file, fh, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "file: "+err.Error(), http.StatusBadRequest)
			return
		}
		defer file.Close()

		name := fmt.Sprintf("okesu-%s-%s", strings.ToLower(osName), strings.ToLower(arch))
		dst := filepath.Join(dir, name)

		// Stream to a tempfile so a partial upload doesn't replace a good binary.
		tmp, err := os.CreateTemp(dir, name+".tmp.*")
		if err != nil {
			http.Error(w, "tempfile: "+err.Error(), http.StatusInternalServerError)
			return
		}
		hash := sha256.New()
		size, err := io.Copy(io.MultiWriter(tmp, hash), file)
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
			http.Error(w, "copy: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if err := tmp.Close(); err != nil {
			os.Remove(tmp.Name())
			http.Error(w, "close: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if err := os.Chmod(tmp.Name(), 0755); err != nil {
			os.Remove(tmp.Name())
			http.Error(w, "chmod: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if err := os.Rename(tmp.Name(), dst); err != nil {
			os.Remove(tmp.Name())
			http.Error(w, "rename: "+err.Error(), http.StatusInternalServerError)
			return
		}

		bin := &db.DaemonBinary{
			Name:      name,
			OS:        strings.ToLower(osName),
			Arch:      strings.ToLower(arch),
			Path:      dst,
			SHA256:    hex.EncodeToString(hash.Sum(nil)),
			SizeBytes: size,
		}
		uploader := ""
		if u := auth.UserFromContext(r.Context()); u != nil {
			uploader = u.Email
		}
		if err := store.UpsertDaemonBinary(bin, uploader); err != nil {
			http.Error(w, "store: "+err.Error(), http.StatusInternalServerError)
			return
		}

		audit.Emit(r, store, db.AuditEntry{
			Action: "binary.upload",
			Target: "binary:" + name,
			Metadata: map[string]any{
				"size":   size,
				"sha256": bin.SHA256,
				"file":   fh.Filename,
			},
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toBinaryJSON(bin))
	}
}

// BinaryDelete — DELETE /api/deploy/binaries/{name} (admin).
func BinaryDelete(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		bin, err := store.DaemonBinaryByName(name)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = os.Remove(bin.Path)
		if err := store.DeleteDaemonBinary(name); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "binary.delete",
			Target: "binary:" + name,
		})
		w.WriteHeader(http.StatusNoContent)
	}
}

// ── known hosts (per-node, admin manages clears) ──────────────────────────

type knownHostJSON struct {
	NodeID          int64  `json:"node_id"`
	NodeName        string `json:"node_name,omitempty"`
	Hostname        string `json:"hostname,omitempty"`
	SSHPort         int    `json:"ssh_port,omitempty"`
	KeyType         string `json:"key_type"`
	Fingerprint     string `json:"fingerprint"`
	AcceptedAt      string `json:"accepted_at"`
	AcceptedByEmail string `json:"accepted_by_email,omitempty"`
}

// KnownHostsList — GET /api/deploy/known-hosts (viewer+).
func KnownHostsList(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		list, err := store.ListKnownHosts()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]knownHostJSON, 0, len(list))
		for _, k := range list {
			out = append(out, knownHostJSON{
				NodeID:          k.NodeID,
				NodeName:        k.NodeName,
				Hostname:        k.Hostname,
				SSHPort:         k.SSHPort,
				KeyType:         k.KeyType,
				Fingerprint:     k.Fingerprint,
				AcceptedAt:      k.AcceptedAt.UTC().Format(time.RFC3339),
				AcceptedByEmail: k.AcceptedByEmail.String,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// NodeKnownHost — GET /api/nodes/{id}/known-host (viewer+).
func NodeKnownHost(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		kh, err := store.GetKnownHost(id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte("null"))
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := knownHostJSON{
			NodeID:          kh.NodeID,
			KeyType:         kh.KeyType,
			Fingerprint:     kh.Fingerprint,
			AcceptedAt:      kh.AcceptedAt.UTC().Format(time.RFC3339),
			AcceptedByEmail: kh.AcceptedByEmail.String,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// NodeKnownHostDelete — DELETE /api/nodes/{id}/known-host (admin).
// Clears the trusted host key so the next deploy re-pins via TOFU.
func NodeKnownHostDelete(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		if err := store.DeleteKnownHost(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		audit.Emit(r, store, db.AuditEntry{
			Action: "node.known_host_clear",
			Target: fmt.Sprintf("node:%d", id),
		})
		w.WriteHeader(http.StatusNoContent)
	}
}
