package db

import (
	"fmt"
	"strings"
	"time"
)

// IOCUpsert is the input to UpsertIOC. NormalizedValue is the dedup key
// (alongside Kind); Value is preserved as-supplied for display.
type IOCUpsert struct {
	Kind            string
	Value           string
	NormalizedValue string
	Source          string // "catalog" | "observed"; defaults to "observed" when empty
	DefinitionPath  string
	Confidence      string
	Attribution     string
	SeverityFloor   string
	Classification  string
	Notes           string
}

// IOCRecord is what the store reads back.
type IOCRecord struct {
	ID               int64
	Kind             string
	Value            string
	NormalizedValue  string
	Source           string
	DefinitionPath   string
	Confidence       string
	Attribution      string
	SeverityFloor    string
	Classification   string
	Notes            string
	ObservationCount int64
	FirstSeen        time.Time
	LastSeen         time.Time
}

// IOCObservation links an IOC to a finding and/or run.
type IOCObservation struct {
	IOCID              int64
	FindingID          int64 // 0 if not linked to a finding
	OrchestrationRunID int64 // 0 if not linked to a run
	Host               string
}

// UpsertIOC inserts or updates a row in `iocs`. Returns the row id and
// whether the row was newly created.
//
// Reconciliation: a catalog upsert overrides an existing observed row's
// metadata (source, definition_path, confidence, attribution,
// severity_floor, classification, notes); observation_count and
// last_seen are preserved. An observed upsert against a catalog row
// only refreshes last_seen — curated metadata is never clobbered.
//
// Race-safety: the dedup is done by the database via INSERT … ON
// CONFLICT DO NOTHING on the (kind, normalized_value) UNIQUE index, so
// two concurrent upserts of the same IOC can't both succeed and one
// can't see "no row" while the other is mid-insert. The follow-up
// UPDATE/SELECT runs unconditionally after the row is guaranteed to
// exist.
func (s *Store) UpsertIOC(in *IOCUpsert) (id int64, created bool, err error) {
	if in.Kind == "" || in.NormalizedValue == "" {
		return 0, false, fmt.Errorf("UpsertIOC: kind and normalized_value are required")
	}
	source := in.Source
	if source == "" {
		source = "observed"
	}

	// 1. Race-safe insert. If a row with the same (kind, normalized_value)
	//    already exists, this is a no-op.
	res, err := s.Exec(`
		INSERT INTO iocs (kind, value, normalized_value, source, definition_path,
		                  confidence, attribution, severity_floor, classification, notes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (kind, normalized_value) DO NOTHING`,
		in.Kind, in.Value, in.NormalizedValue, source,
		nullable(in.DefinitionPath), nullable(in.Confidence), nullable(in.Attribution),
		nullable(in.SeverityFloor), nullable(in.Classification), nullable(in.Notes))
	if err != nil {
		return 0, false, err
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return 0, false, err
	}
	created = rowsAffected == 1

	// 2. Apply metadata reconciliation on conflict. A catalog upsert
	//    overwrites observed metadata; an observed upsert only refreshes
	//    timestamps so curated catalog metadata is never clobbered.
	if !created {
		if source == "catalog" {
			if _, err := s.Exec(`
				UPDATE iocs
				SET source = ?, definition_path = ?, confidence = ?, attribution = ?,
				    severity_floor = ?, classification = ?, notes = ?, updated_at = CURRENT_TIMESTAMP
				WHERE kind = ? AND normalized_value = ?`,
				source, nullable(in.DefinitionPath), nullable(in.Confidence), nullable(in.Attribution),
				nullable(in.SeverityFloor), nullable(in.Classification), nullable(in.Notes),
				in.Kind, in.NormalizedValue); err != nil {
				return 0, false, err
			}
		} else {
			if _, err := s.Exec(`
				UPDATE iocs SET last_seen = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
				WHERE kind = ? AND normalized_value = ?`,
				in.Kind, in.NormalizedValue); err != nil {
				return 0, false, err
			}
		}
	}

	// 3. Fetch the row id. Safe regardless of whether we inserted or
	//    conflicted because the row exists either way after step 1.
	if err := s.QueryRow(
		`SELECT id FROM iocs WHERE kind = ? AND normalized_value = ?`,
		in.Kind, in.NormalizedValue,
	).Scan(&id); err != nil {
		return 0, false, err
	}
	return id, created, nil
}

func (s *Store) GetIOC(id int64) (*IOCRecord, error) {
	row := s.QueryRow(`
		SELECT id, kind, value, normalized_value, source,
		       COALESCE(definition_path,''), COALESCE(confidence,''),
		       COALESCE(attribution,''), COALESCE(severity_floor,''),
		       COALESCE(classification,''), COALESCE(notes,''), observation_count,
		       first_seen, last_seen
		FROM iocs WHERE id = ?`, id)
	var r IOCRecord
	if err := row.Scan(&r.ID, &r.Kind, &r.Value, &r.NormalizedValue, &r.Source,
		&r.DefinitionPath, &r.Confidence, &r.Attribution, &r.SeverityFloor,
		&r.Classification, &r.Notes, &r.ObservationCount,
		&r.FirstSeen, &r.LastSeen); err != nil {
		return nil, err
	}
	return &r, nil
}

// RecordIOCObservation appends an observation row and bumps
// observation_count + last_seen on the parent IOC.
func (s *Store) RecordIOCObservation(iocID int64, obs *IOCObservation) error {
	tx, err := s.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
		INSERT INTO ioc_observations (ioc_id, finding_id, orchestration_run_id, host)
		VALUES (?, ?, ?, ?)`,
		iocID,
		nullableInt64(obs.FindingID),
		nullableInt64(obs.OrchestrationRunID),
		nullable(obs.Host)); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE iocs SET observation_count = observation_count + 1, last_seen = CURRENT_TIMESTAMP WHERE id = ?`, iocID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListIOCObservations(iocID int64) ([]IOCObservation, error) {
	rows, err := s.Query(`
		SELECT ioc_id, COALESCE(finding_id,0), COALESCE(orchestration_run_id,0), COALESCE(host,'')
		FROM ioc_observations WHERE ioc_id = ? ORDER BY observed_at DESC`, iocID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IOCObservation
	for rows.Next() {
		var o IOCObservation
		if err := rows.Scan(&o.IOCID, &o.FindingID, &o.OrchestrationRunID, &o.Host); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// LookupIOC fetches by (kind, normalized_value). Returns sql.ErrNoRows if absent.
func (s *Store) LookupIOC(kind, normalizedValue string) (*IOCRecord, error) {
	row := s.QueryRow(`SELECT id FROM iocs WHERE kind = ? AND normalized_value = ?`, kind, normalizedValue)
	var id int64
	if err := row.Scan(&id); err != nil {
		return nil, err
	}
	return s.GetIOC(id)
}

// IOCListFilter narrows ListIOCs. Zero-valued fields disable that
// constraint. Limit is clamped to (0, 1000]; out-of-range values fall
// back to the default 100.
type IOCListFilter struct {
	Kind      string
	FindingID int64
	Limit     int
}

// ListIOCs returns rows from `iocs`, ordered by last_seen DESC, joined
// against ioc_observations only when FindingID is set. SELECT DISTINCT
// is applied so an IOC observed multiple times against the same
// finding doesn't appear multiple times in the result.
func (s *Store) ListIOCs(f IOCListFilter) ([]*IOCRecord, error) {
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 100
	}
	var (
		clauses []string
		args    []any
		joinObs bool
	)
	if f.Kind != "" {
		clauses = append(clauses, "iocs.kind = ?")
		args = append(args, f.Kind)
	}
	if f.FindingID > 0 {
		joinObs = true
		clauses = append(clauses, "ioc_observations.finding_id = ?")
		args = append(args, f.FindingID)
	}
	where := ""
	if len(clauses) > 0 {
		where = " WHERE " + strings.Join(clauses, " AND ")
	}
	join := ""
	if joinObs {
		join = " JOIN ioc_observations ON ioc_observations.ioc_id = iocs.id "
	}
	q := `SELECT DISTINCT iocs.id, iocs.kind, iocs.value, iocs.normalized_value, iocs.source,
	       COALESCE(iocs.definition_path,''), COALESCE(iocs.confidence,''),
	       COALESCE(iocs.attribution,''), COALESCE(iocs.severity_floor,''),
	       COALESCE(iocs.classification,''), COALESCE(iocs.notes,''), iocs.observation_count,
	       iocs.first_seen, iocs.last_seen
	FROM iocs ` + join + where + ` ORDER BY iocs.last_seen DESC LIMIT ?`
	args = append(args, f.Limit)

	rows, err := s.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*IOCRecord
	for rows.Next() {
		var r IOCRecord
		if err := rows.Scan(&r.ID, &r.Kind, &r.Value, &r.NormalizedValue, &r.Source,
			&r.DefinitionPath, &r.Confidence, &r.Attribution, &r.SeverityFloor,
			&r.Classification, &r.Notes, &r.ObservationCount,
			&r.FirstSeen, &r.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}
