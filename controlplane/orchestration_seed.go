// Orchestration seed-from-disk. Phase 22.10 PR γ.
//
// On CP boot, scan an operator-supplied directory for orchestration
// *.md files and install any that aren't already present (matched by
// `name` from the YAML frontmatter). This lets a fresh deployment
// pick up the canonical set of orchestrations without hand-curling
// each one through /api/orchestrations.
//
// Behavior:
//   - "Install if missing" — never overwrites an existing entry. The
//     operator can edit installed orchestrations through the UI
//     without the next CP boot clobbering their changes.
//   - Best-effort — a malformed file logs and skips. We don't fail
//     CP boot over a bad seed.
//   - Empty / unset OrchestrationSeedDir disables the path entirely.

package controlplane

import (
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/section9labs/okesu/controlplane/orchestrator"
)

// seedOrchestrations walks the seed directory and installs any
// orchestration *.md files whose names aren't already in the DB.
// Logs progress; never returns an error to the caller — boot must
// proceed even when the seed is broken.
func (s *Server) seedOrchestrations() {
	dir := s.cfg.OrchestrationSeedDir
	if dir == "" {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		log.Printf("orchestration-seed: read %q: %v (skipping)", dir, err)
		return
	}
	// Cache the existing names so we don't N+1.
	existing, err := s.store.ListOrchestrations()
	if err != nil {
		log.Printf("orchestration-seed: list installed: %v (skipping)", err)
		return
	}
	have := make(map[string]bool, len(existing))
	for _, o := range existing {
		have[o.Name] = true
	}
	installed := 0
	skipped := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		path := filepath.Join(dir, name)
		body, rerr := os.ReadFile(path)
		if rerr != nil {
			log.Printf("orchestration-seed: read %s: %v (skipping)", path, rerr)
			continue
		}
		spec, perr := orchestrator.Parse(string(body))
		if perr != nil {
			log.Printf("orchestration-seed: parse %s: %v (skipping)", path, perr)
			continue
		}
		if have[spec.Name] {
			skipped++
			continue
		}
		_, cerr := s.store.CreateOrchestration(
			spec.Name, spec.Description, string(body),
			spec.Trigger.On, spec.Trigger.Filter, spec.Trigger.Cron,
			0, // createdBy=0 — system seed; the audit log will show "" actor
		)
		if cerr != nil {
			log.Printf("orchestration-seed: install %s: %v (skipping)", spec.Name, cerr)
			continue
		}
		installed++
		log.Printf("orchestration-seed: installed %q from %s", spec.Name, path)
	}
	if installed > 0 || skipped > 0 {
		log.Printf("orchestration-seed: %d installed, %d already present", installed, skipped)
	}
}
