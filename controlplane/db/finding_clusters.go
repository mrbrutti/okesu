package db

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// FindClusterIDForIOCs returns the most recent cluster_id assigned to
// any finding observed referencing one of the given IOCs within the
// supplied window. Empty string means "no existing cluster" — the
// caller should mint a new cluster_id (typically the new finding's own
// id, after the InsertFinding succeeds).
//
// IOC IDs are passed as a slice so a finding referencing N indicators
// joins whichever cluster matches first; first-match-wins is fine in
// v1 because it's a stable choice given the same input.
func (s *Store) FindClusterIDForIOCs(iocIDs []int64, window time.Duration) (string, error) {
	if len(iocIDs) == 0 {
		return "", nil
	}
	placeholders := make([]string, len(iocIDs))
	args := make([]any, 0, len(iocIDs)+1)
	for i, id := range iocIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	cutoff := time.Now().Add(-window).UnixMilli()
	args = append(args, cutoff)

	q := `SELECT findings.cluster_id
	      FROM findings
	      JOIN ioc_observations ON ioc_observations.finding_id = findings.id
	      WHERE ioc_observations.ioc_id IN (` + strings.Join(placeholders, ",") + `)
	        AND findings.cluster_id IS NOT NULL
	        AND findings.cluster_id != ''
	        AND findings.ts >= ?
	      ORDER BY findings.ts DESC
	      LIMIT 1`

	row := s.QueryRow(q, args...)
	var cid string
	switch err := row.Scan(&cid); {
	case errors.Is(err, sql.ErrNoRows):
		return "", nil
	case err != nil:
		// Real DB error — surface it so the caller sees ingest-time DB
		// trouble instead of silently minting fresh clusters.
		return "", err
	}
	return cid, nil
}

// MaxSeverityFloor returns the highest severity_floor across the given
// IOCs. Severity ordering: CRITICAL > HIGH > MEDIUM > LOW > INFO > "".
// Empty string means none of the IOCs carry a severity_floor (typical
// for observed-source IOCs).
func (s *Store) MaxSeverityFloor(iocIDs []int64) (string, error) {
	if len(iocIDs) == 0 {
		return "", nil
	}
	placeholders := make([]string, len(iocIDs))
	args := make([]any, len(iocIDs))
	for i, id := range iocIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	q := `SELECT COALESCE(severity_floor,'') FROM iocs WHERE id IN (` + strings.Join(placeholders, ",") + `)`
	rows, err := s.Query(q, args...)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	best := ""
	for rows.Next() {
		var sf string
		if err := rows.Scan(&sf); err != nil {
			return "", err
		}
		if severityRank(sf) > severityRank(best) {
			best = sf
		}
	}
	return best, rows.Err()
}

// PropagatedMetadata is the slice of catalog metadata that ingest
// stamps on the FindingInsert. First-match-wins for attribution and
// classification — multi-IOC findings take the metadata of the first
// catalog-source IOC that has it. Severity floor is the highest across
// the set.
type PropagatedMetadata struct {
	SeverityFloor  string
	Confidence     string
	Attribution    string
	Classification string
}

// PropagatedMetadataForIOCs reads (severity_floor, confidence,
// attribution, classification) for every supplied IOC ID and returns
// the propagation values to apply to the inserting finding. Only
// catalog-source IOCs contribute (observed-source IOCs don't carry
// curated metadata to propagate).
//
// Ordering: ORDER BY id makes "first non-empty wins" deterministic —
// the earliest-registered catalog entry for a finding's IOC set wins
// metadata-propagation ties, which is stable across runs and matches
// the operator mental model "the first curated entry I added is the
// authoritative one for matching findings."
func (s *Store) PropagatedMetadataForIOCs(iocIDs []int64) (PropagatedMetadata, error) {
	var out PropagatedMetadata
	if len(iocIDs) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(iocIDs))
	args := make([]any, len(iocIDs))
	for i, id := range iocIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	q := `SELECT
	        COALESCE(severity_floor,''),
	        COALESCE(confidence,''),
	        COALESCE(attribution,''),
	        COALESCE(classification,'')
	      FROM iocs WHERE id IN (` + strings.Join(placeholders, ",") + `) AND source = 'catalog'
	      ORDER BY id`
	rows, err := s.Query(q, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var sf, conf, attr, class string
		if err := rows.Scan(&sf, &conf, &attr, &class); err != nil {
			return out, err
		}
		if severityRank(sf) > severityRank(out.SeverityFloor) {
			out.SeverityFloor = sf
		}
		if out.Confidence == "" {
			out.Confidence = conf
		}
		if out.Attribution == "" {
			out.Attribution = attr
		}
		if out.Classification == "" {
			out.Classification = class
		}
	}
	return out, rows.Err()
}

// severityRank is a private ordering helper for severity comparisons.
// Mirrors the scale used in api/ingest.go's severity validation.
func severityRank(s string) int {
	switch strings.ToUpper(s) {
	case "CRITICAL":
		return 5
	case "HIGH":
		return 4
	case "MEDIUM":
		return 3
	case "LOW":
		return 2
	case "INFO":
		return 1
	default:
		return 0
	}
}
