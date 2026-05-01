// Per-label severity ceilings. Phase 22.10 PR γ.
//
// A ceiling is a (selector → max_severity) rule. At finding-ingest
// time the CP looks up the host's node labels, evaluates every
// ceiling whose selector matches, picks the most restrictive, and
// applies it as operator_severity if the agent's severity exceeds
// the cap.
//
// Most natural use:
//   selector="env=staging"  max_severity=MEDIUM
//   selector="env=dev"      max_severity=LOW
//   selector="env=lab"      max_severity=INFO
//
// Ceilings only LOWER. The agent's INFO finding never gets promoted
// to MEDIUM because some ceiling allows MEDIUM — that would make
// "noise reduction" silently amplify findings.

package db

import (
	"errors"
	"strings"
)

// SeverityCeiling is one row.
type SeverityCeiling struct {
	ID          int64  `json:"id"`
	Selector    string `json:"selector"`
	MaxSeverity string `json:"max_severity"`
	Reason      string `json:"reason,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
}

// severityRank lives in finding_clusters.go — same package, shared
// helper. Ceiling logic uses it for "is X more severe than Y?"
// comparison without re-implementing the table.

// CreateSeverityCeiling inserts a new ceiling. Returns an error when
// the selector is empty or max_severity is not a canonical value, or
// when the (UNIQUE) selector collides with an existing rule.
func (s *Store) CreateSeverityCeiling(c *SeverityCeiling) (int64, error) {
	c.Selector = strings.TrimSpace(c.Selector)
	if c.Selector == "" {
		return 0, errors.New("selector required")
	}
	if !IsValidSeverity(c.MaxSeverity) {
		return 0, errors.New("max_severity must be one of CRITICAL/HIGH/MEDIUM/LOW/INFO")
	}
	if _, err := ParseSelector(c.Selector); err != nil {
		return 0, err
	}
	res, err := s.Exec(`
		INSERT INTO severity_ceilings (selector, max_severity, reason)
		VALUES (?, ?, ?)`,
		c.Selector, strings.ToUpper(c.MaxSeverity), c.Reason)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateSeverityCeiling overwrites max_severity + reason for one rule.
func (s *Store) UpdateSeverityCeiling(id int64, maxSev, reason string) error {
	if !IsValidSeverity(maxSev) {
		return errors.New("max_severity must be one of CRITICAL/HIGH/MEDIUM/LOW/INFO")
	}
	_, err := s.Exec(`
		UPDATE severity_ceilings
		   SET max_severity = ?, reason = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ?`,
		strings.ToUpper(maxSev), reason, id)
	return err
}

// DeleteSeverityCeiling removes a rule. Idempotent.
func (s *Store) DeleteSeverityCeiling(id int64) error {
	_, err := s.Exec(`DELETE FROM severity_ceilings WHERE id = ?`, id)
	return err
}

// ListSeverityCeilings returns every ceiling, ordered by id.
func (s *Store) ListSeverityCeilings() ([]SeverityCeiling, error) {
	rows, err := s.Query(`
		SELECT id, selector, max_severity, reason,
		       CAST(created_at AS TEXT), CAST(updated_at AS TEXT)
		  FROM severity_ceilings
		 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SeverityCeiling{}
	for rows.Next() {
		var c SeverityCeiling
		if err := rows.Scan(&c.ID, &c.Selector, &c.MaxSeverity, &c.Reason, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SeverityCeilingForLabels returns the most restrictive max_severity
// (lowest rank) whose selector matches the given label map. Empty
// string when no ceiling applies. Used by the ingest path to cap
// agent-assigned severities for non-prod nodes.
func (s *Store) SeverityCeilingForLabels(labels map[string]string) (string, error) {
	all, err := s.ListSeverityCeilings()
	if err != nil {
		return "", err
	}
	if len(all) == 0 {
		return "", nil
	}
	best := ""
	bestRank := 99
	for _, c := range all {
		sel, perr := ParseSelector(c.Selector)
		if perr != nil {
			continue
		}
		if !sel.Matches(labels) {
			continue
		}
		r := severityRank(c.MaxSeverity)
		if r > 0 && r < bestRank {
			bestRank = r
			best = strings.ToUpper(c.MaxSeverity)
		}
	}
	return best, nil
}

// SeverityCeilingForHost is the ingest-time convenience: looks up
// the host's labels via existing helpers, evaluates ceilings,
// returns the cap. Empty when no rule applies.
func (s *Store) SeverityCeilingForHost(host string) (string, error) {
	if host == "" {
		return "", nil
	}
	labels, err := s.NodeLabelsForHost(host)
	if err != nil {
		return "", err
	}
	if len(labels) == 0 {
		return "", nil
	}
	return s.SeverityCeilingForLabels(labels)
}

// CapSeverity returns the lower of agentSev and ceiling. Used by
// callers that want "apply this cap if it's actually more
// restrictive." Returns "" when no override should be written.
func CapSeverity(agentSev, ceiling string) string {
	if ceiling == "" {
		return ""
	}
	if severityRank(agentSev) <= severityRank(ceiling) {
		return ""
	}
	return strings.ToUpper(ceiling)
}
