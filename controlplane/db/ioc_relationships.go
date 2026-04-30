package db

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

type IOCRelationship struct {
	ID         int64
	SubjectID  int64
	Predicate  string
	ObjectID   int64
	Source     string
	Confidence string
}

type IOCRelationshipInsert struct {
	SubjectID  int64
	Predicate  string
	ObjectID   int64
	Source     string
	Confidence string
}

// validPredicates is the fixed v1 vocabulary. New predicates require a
// migration with release notes calling out backward compatibility.
var validPredicates = map[string]bool{
	"resolves-to": true,
	"exploits":    true,
	"hosted-at":   true,
	"belongs-to":  true,
	"signed-with": true,
	"dropped-by":  true,
}

func (s *Store) AddIOCRelationship(in *IOCRelationshipInsert) error {
	if in.SubjectID == 0 || in.ObjectID == 0 || in.Predicate == "" {
		return fmt.Errorf("AddIOCRelationship: subject_id, object_id, and predicate are required")
	}
	if !validPredicates[in.Predicate] {
		return fmt.Errorf("AddIOCRelationship: unknown predicate %q (allowed: %s)",
			in.Predicate, predicateList())
	}
	source := in.Source
	if source == "" {
		source = "agent"
	}
	_, err := s.Exec(`
		INSERT INTO ioc_relationships (subject_id, predicate, object_id, source, confidence)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (subject_id, predicate, object_id) DO NOTHING`,
		in.SubjectID, in.Predicate, in.ObjectID, source, nullable(in.Confidence))
	return err
}

func (s *Store) ListIOCRelationships(iocID int64) ([]IOCRelationship, error) {
	rows, err := s.Query(`
		SELECT id, subject_id, predicate, object_id, source, COALESCE(confidence, '')
		FROM ioc_relationships
		WHERE subject_id = ? OR object_id = ?
		ORDER BY (subject_id = ?) DESC, id`, iocID, iocID, iocID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IOCRelationship
	for rows.Next() {
		var r IOCRelationship
		var confidence sql.NullString
		if err := rows.Scan(&r.ID, &r.SubjectID, &r.Predicate, &r.ObjectID, &r.Source, &confidence); err != nil {
			return nil, err
		}
		r.Confidence = confidence.String
		out = append(out, r)
	}
	return out, rows.Err()
}

func predicateList() string {
	preds := make([]string, 0, len(validPredicates))
	for p := range validPredicates {
		preds = append(preds, p)
	}
	sort.Strings(preds)
	return strings.Join(preds, ", ")
}
