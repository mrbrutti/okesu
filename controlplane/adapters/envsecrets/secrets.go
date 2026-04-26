// Package envsecrets implements the Secrets port against environment
// variables (and optionally a /etc/okesu/secrets/ directory of one
// file per secret).
//
// Naming: secret "cp/webhook-secret" maps to env var
// OKESU_SECRET_CP_WEBHOOK_SECRET (uppercase, slashes/dashes → underscores).
//
// This adapter is read-only — fits the dev / single-host model. Real
// production deployments use the oci-vault adapter where rotate is
// supported.
package envsecrets

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/section9labs/okesu/controlplane/ports"
)

// Adapter satisfies ports.Secrets via env vars + an optional
// directory-of-files fallback for secrets too large to fit in env.
type Adapter struct {
	// Dir is an optional directory of secret files. When set, a Get
	// for "name" first tries env, then <Dir>/<name> (slashes preserved).
	Dir string
}

// New returns an env-only adapter.
func New() *Adapter { return &Adapter{} }

// NewWithDir returns an adapter that falls through to dir for secrets
// not found in the environment. Useful for systemd-loaded LoadCredential
// drops (/run/credentials/<unit>/<name>).
func NewWithDir(dir string) *Adapter { return &Adapter{Dir: dir} }

// nameToEnv converts a hierarchical name like "llm/anthropic" to an
// uppercase env var name with the OKESU_SECRET_ prefix.
func nameToEnv(name string) string {
	s := strings.ToUpper(name)
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "-", "_")
	return "OKESU_SECRET_" + s
}

func (a *Adapter) Get(_ context.Context, name string) ([]byte, error) {
	if name == "" {
		return nil, fmt.Errorf("secret name required")
	}
	if v := os.Getenv(nameToEnv(name)); v != "" {
		return []byte(v), nil
	}
	if a.Dir != "" {
		path := filepath.Join(a.Dir, filepath.Clean("/"+name))
		if data, err := os.ReadFile(path); err == nil {
			return data, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", ports.ErrNotFound, name)
}

// Compile-time assertion.
var _ ports.Secrets = (*Adapter)(nil)
