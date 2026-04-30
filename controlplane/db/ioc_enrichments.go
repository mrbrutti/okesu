package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// IOCEnrichment is one cached vendor response for an IOC.
type IOCEnrichment struct {
	ID        int64
	IOCID     int64
	Adapter   string
	Verdict   string
	Score     int64
	RawJSON   string
	FetchedAt time.Time
	ExpiresAt time.Time
}

type IOCEnrichmentInsert struct {
	IOCID     int64
	Adapter   string
	Verdict   string
	Score     int64
	RawJSON   string
	ExpiresAt time.Time
}

func (s *Store) UpsertIOCEnrichment(in *IOCEnrichmentInsert) (int64, error) {
	if in.IOCID == 0 || in.Adapter == "" {
		return 0, fmt.Errorf("UpsertIOCEnrichment: ioc_id and adapter are required")
	}
	if in.ExpiresAt.IsZero() {
		return 0, fmt.Errorf("UpsertIOCEnrichment: expires_at is required")
	}
	res, err := s.Exec(`
		INSERT INTO ioc_enrichments (ioc_id, adapter, verdict, score, raw_json, expires_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (ioc_id, adapter) DO UPDATE SET
		    verdict = excluded.verdict,
		    score = excluded.score,
		    raw_json = excluded.raw_json,
		    fetched_at = CURRENT_TIMESTAMP,
		    expires_at = excluded.expires_at`,
		in.IOCID, in.Adapter,
		nullable(in.Verdict),
		nullableInt64(in.Score),
		in.RawJSON,
		in.ExpiresAt.UTC())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetIOCEnrichment(iocID int64, adapter string) (*IOCEnrichment, error) {
	row := s.QueryRow(`
		SELECT id, ioc_id, adapter, COALESCE(verdict, ''), COALESCE(score, 0),
		       raw_json, fetched_at, expires_at
		FROM ioc_enrichments
		WHERE ioc_id = ? AND adapter = ?`, iocID, adapter)
	var e IOCEnrichment
	var fetchedAt, expiresAt sql.NullString
	if err := row.Scan(&e.ID, &e.IOCID, &e.Adapter, &e.Verdict, &e.Score,
		&e.RawJSON, &fetchedAt, &expiresAt); err != nil {
		return nil, err
	}
	e.FetchedAt = ParseTimestamp(fetchedAt.String)
	e.ExpiresAt = ParseTimestamp(expiresAt.String)
	return &e, nil
}

func (s *Store) GetFreshIOCEnrichment(iocID int64, adapter string) (*IOCEnrichment, error) {
	got, err := s.GetIOCEnrichment(iocID, adapter)
	if err != nil {
		return nil, err
	}
	if !got.ExpiresAt.IsZero() && time.Now().After(got.ExpiresAt) {
		return nil, errors.New("ioc enrichment: cache expired")
	}
	return got, nil
}

func (s *Store) ListIOCEnrichments(iocID int64) ([]IOCEnrichment, error) {
	rows, err := s.Query(`
		SELECT id, ioc_id, adapter, COALESCE(verdict, ''), COALESCE(score, 0),
		       raw_json, fetched_at, expires_at
		FROM ioc_enrichments
		WHERE ioc_id = ?
		ORDER BY adapter`, iocID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IOCEnrichment
	for rows.Next() {
		var e IOCEnrichment
		var fetchedAt, expiresAt sql.NullString
		if err := rows.Scan(&e.ID, &e.IOCID, &e.Adapter, &e.Verdict, &e.Score,
			&e.RawJSON, &fetchedAt, &expiresAt); err != nil {
			return nil, err
		}
		e.FetchedAt = ParseTimestamp(fetchedAt.String)
		e.ExpiresAt = ParseTimestamp(expiresAt.String)
		out = append(out, e)
	}
	return out, rows.Err()
}
