package db

import (
	"database/sql"
	"fmt"
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
// last_seen are preserved.
func (s *Store) UpsertIOC(in *IOCUpsert) (id int64, created bool, err error) {
	if in.Kind == "" || in.NormalizedValue == "" {
		return 0, false, fmt.Errorf("UpsertIOC: kind and normalized_value are required")
	}
	source := in.Source
	if source == "" {
		source = "observed"
	}

	row := s.QueryRow(`SELECT id, source FROM iocs WHERE kind = ? AND normalized_value = ?`, in.Kind, in.NormalizedValue)
	var existingID int64
	var existingSource string
	switch err := row.Scan(&existingID, &existingSource); err {
	case nil:
		if source == "catalog" {
			_, err := s.Exec(`
				UPDATE iocs
				SET source = ?, definition_path = ?, confidence = ?, attribution = ?,
				    severity_floor = ?, classification = ?, notes = ?, updated_at = CURRENT_TIMESTAMP
				WHERE id = ?`,
				source, nullable(in.DefinitionPath), nullable(in.Confidence), nullable(in.Attribution),
				nullable(in.SeverityFloor), nullable(in.Classification), nullable(in.Notes), existingID)
			if err != nil {
				return 0, false, err
			}
		} else {
			_, err := s.Exec(`UPDATE iocs SET last_seen = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, existingID)
			if err != nil {
				return 0, false, err
			}
		}
		return existingID, false, nil
	case sql.ErrNoRows:
		res, err := s.Exec(`
			INSERT INTO iocs (kind, value, normalized_value, source, definition_path,
			                  confidence, attribution, severity_floor, classification, notes)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			in.Kind, in.Value, in.NormalizedValue, source,
			nullable(in.DefinitionPath), nullable(in.Confidence), nullable(in.Attribution),
			nullable(in.SeverityFloor), nullable(in.Classification), nullable(in.Notes))
		if err != nil {
			return 0, false, err
		}
		newID, err := res.LastInsertId()
		if err != nil {
			return 0, false, err
		}
		return newID, true, nil
	default:
		return 0, false, err
	}
}

func (s *Store) GetIOC(id int64) (*IOCRecord, error) {
	row := s.QueryRow(`
		SELECT id, kind, value, normalized_value, source,
		       COALESCE(definition_path,''), COALESCE(confidence,''),
		       COALESCE(attribution,''), COALESCE(severity_floor,''),
		       COALESCE(classification,''), COALESCE(notes,''), observation_count
		FROM iocs WHERE id = ?`, id)
	var r IOCRecord
	if err := row.Scan(&r.ID, &r.Kind, &r.Value, &r.NormalizedValue, &r.Source,
		&r.DefinitionPath, &r.Confidence, &r.Attribution, &r.SeverityFloor,
		&r.Classification, &r.Notes, &r.ObservationCount); err != nil {
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
		nullableObsInt(obs.FindingID),
		nullableObsInt(obs.OrchestrationRunID),
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

// nullableObsInt returns nil for v == 0 so observation rows can store
// NULL in the finding_id / orchestration_run_id columns when the
// observation isn't linked to one. The package's existing nullableInt64
// returns sql.NullInt64; this variant returns any so it slots
// directly into Exec's variadic args.
func nullableObsInt(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}
