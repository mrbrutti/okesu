// Generic labels store. Phase 22.9.
//
// Backs label/selector usage across every entity kind — nodes,
// daimons, findings, investigations, runs, orchestrations, peers,
// secrets, groups. Per-kind helpers (SetNodeLabel etc.) re-target
// these primitives at migration 051; new code should reach for the
// generic API directly via target kind constants.
//
// Identity model:
//   - Entities with a numeric primary key (most kinds) use target_id;
//     target_key stays "".
//   - Entities with a composite key (daimons = name@host, federation
//     peers identified by instance UUID) use target_key; target_id
//     stays 0.
//   - Selector evaluation is identical regardless of which form a
//     kind uses — selector.go's Matches walks the label map.

package db

import (
	"errors"
	"strings"
)

// Label-target kind constants. Matches the `target_kind` column.
// Adding a new kind requires three things: (1) the const here, (2)
// the per-entity store binding (e.g. labels-driven detail page), and
// (3) the API surface in api/labels.go listening for `?kind=X`. The
// migration is generic — no schema change per kind.
const (
	LabelKindNode          = "node"
	LabelKindDaimon        = "daimon" // identified by name@host (target_key)
	LabelKindFinding       = "finding"
	LabelKindInvestigation = "investigation"
	LabelKindRun           = "run"
	LabelKindOrchestration = "orchestration"
	LabelKindCP            = "cp" // federation peer; target_key = instance_id
	LabelKindSecret        = "secret"
	LabelKindGroup         = "group"
)

// Label is one row.
type Label struct {
	ID         int64  `json:"id"`
	TargetKind string `json:"target_kind"`
	TargetID   int64  `json:"target_id,omitempty"`
	TargetKey  string `json:"target_key,omitempty"`
	Key        string `json:"key"`
	Value      string `json:"value"`
	Source     string `json:"source"`
	CreatedAt  string `json:"created_at,omitempty"`
	UpdatedAt  string `json:"updated_at,omitempty"`
}

// LabelTarget is the lightweight identity tuple used by selector
// query results — a kind plus whichever id form the kind uses.
type LabelTarget struct {
	Kind string `json:"kind"`
	ID   int64  `json:"id,omitempty"`
	Key  string `json:"key,omitempty"`
}

// SetLabel upserts (target, key) → value. Validates key/value via the
// same character class the selector parser accepts so a label can
// always be selected against. source defaults to "manual" when empty.
func (s *Store) SetLabel(kind string, targetID int64, targetKey, key, value, source string) error {
	if kind == "" {
		return errors.New("label target_kind required")
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("label key required")
	}
	if !validKey(key) {
		return errors.New("invalid label key (allowed: a-z A-Z 0-9 . - _ /)")
	}
	if !validValue(value) {
		return errors.New("invalid label value (allowed: a-z A-Z 0-9 . - _ /)")
	}
	if source == "" {
		source = "manual"
	}
	_, err := s.Exec(`
		INSERT INTO labels (target_kind, target_id, target_key, key, value, source)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (target_kind, target_id, target_key, key) DO UPDATE SET
		  value      = excluded.value,
		  source     = excluded.source,
		  updated_at = CURRENT_TIMESTAMP
	`, kind, targetID, targetKey, key, value, source)
	return err
}

// DeleteLabel removes one (target, key) pair. Idempotent.
func (s *Store) DeleteLabel(kind string, targetID int64, targetKey, key string) error {
	_, err := s.Exec(`
		DELETE FROM labels
		 WHERE target_kind = ? AND target_id = ? AND target_key = ? AND key = ?`,
		kind, targetID, targetKey, key)
	return err
}

// DeleteLabelsForTarget removes every label on one entity. Used when
// the entity is deleted by a path that doesn't have CASCADE configured
// (most entity tables don't reference labels via foreign key — keeping
// the table independent so a kind misspelling can't take rows down).
func (s *Store) DeleteLabelsForTarget(kind string, targetID int64, targetKey string) error {
	_, err := s.Exec(`
		DELETE FROM labels
		 WHERE target_kind = ? AND target_id = ? AND target_key = ?`,
		kind, targetID, targetKey)
	return err
}

// ListLabels returns the label map for one target. Empty map (not
// nil) when nothing is set — JSON consumers see {} not null.
func (s *Store) ListLabels(kind string, targetID int64, targetKey string) (map[string]string, error) {
	rows, err := s.Query(`
		SELECT key, value
		  FROM labels
		 WHERE target_kind = ? AND target_id = ? AND target_key = ?`,
		kind, targetID, targetKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// ListLabelsBatchByID batches the (kind, target_id) lookup so list
// pages don't N+1. Empty map entries (entities with no labels) are
// absent from the outer map.
func (s *Store) ListLabelsBatchByID(kind string, ids []int64) (map[int64]map[string]string, error) {
	if len(ids) == 0 {
		return map[int64]map[string]string{}, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, 0, len(ids)+1)
	args = append(args, kind)
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}
	q := `SELECT target_id, key, value FROM labels
	       WHERE target_kind = ? AND target_id IN (` + strings.Join(placeholders, ",") + `)`
	rows, err := s.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]map[string]string{}
	for rows.Next() {
		var id int64
		var k, v string
		if err := rows.Scan(&id, &k, &v); err != nil {
			return nil, err
		}
		m, ok := out[id]
		if !ok {
			m = map[string]string{}
			out[id] = m
		}
		m[k] = v
	}
	return out, rows.Err()
}

// ListLabelsBatchByKey batches the (kind, target_key) lookup. Same
// shape as ListLabelsBatchByID but for kinds that key by string.
func (s *Store) ListLabelsBatchByKey(kind string, keys []string) (map[string]map[string]string, error) {
	if len(keys) == 0 {
		return map[string]map[string]string{}, nil
	}
	placeholders := make([]string, len(keys))
	args := make([]any, 0, len(keys)+1)
	args = append(args, kind)
	for i, k := range keys {
		placeholders[i] = "?"
		args = append(args, k)
	}
	q := `SELECT target_key, key, value FROM labels
	       WHERE target_kind = ? AND target_key IN (` + strings.Join(placeholders, ",") + `)`
	rows, err := s.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]string{}
	for rows.Next() {
		var tk string
		var k, v string
		if err := rows.Scan(&tk, &k, &v); err != nil {
			return nil, err
		}
		m, ok := out[tk]
		if !ok {
			m = map[string]string{}
			out[tk] = m
		}
		m[k] = v
	}
	return out, rows.Err()
}

// FindTargetsBySelector evaluates a parsed selector against every
// target of the given kind and returns the matching identity tuples.
// kind is required; sel is parsed by the caller (so a malformed
// selector surfaces a parse error before the DB hit).
//
// Implementation is conservative: pull every label row for the kind,
// group by target, evaluate in memory. Fine for the fleet sizes we
// support today; can switch to a CTE/recursive query if a deployment
// outgrows it.
func (s *Store) FindTargetsBySelector(kind string, sel Selector) ([]LabelTarget, error) {
	if kind == "" {
		return nil, errors.New("kind required")
	}
	rows, err := s.Query(`
		SELECT target_id, target_key, key, value
		  FROM labels
		 WHERE target_kind = ?`, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type tk struct {
		id  int64
		key string
	}
	byTarget := map[tk]map[string]string{}
	for rows.Next() {
		var id int64
		var keyTarget, k, v string
		if err := rows.Scan(&id, &keyTarget, &k, &v); err != nil {
			return nil, err
		}
		t := tk{id: id, key: keyTarget}
		m, ok := byTarget[t]
		if !ok {
			m = map[string]string{}
			byTarget[t] = m
		}
		m[k] = v
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]LabelTarget, 0, len(byTarget))
	for t, labels := range byTarget {
		if !sel.Matches(labels) {
			continue
		}
		out = append(out, LabelTarget{Kind: kind, ID: t.id, Key: t.key})
	}
	return out, nil
}

// DistinctLabelKeys returns the unique label keys present for a kind,
// sorted. Drives autocomplete in the SelectorInput component.
func (s *Store) DistinctLabelKeys(kind string) ([]string, error) {
	rows, err := s.Query(`
		SELECT DISTINCT key FROM labels
		 WHERE target_kind = ?
		 ORDER BY key`, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// DistinctLabelValues returns the unique values seen for one (kind,
// key) pair. Sorted; pairs with autocomplete in the SelectorInput
// when the user has typed a key and is filling in the value.
func (s *Store) DistinctLabelValues(kind, key string) ([]string, error) {
	rows, err := s.Query(`
		SELECT DISTINCT value FROM labels
		 WHERE target_kind = ? AND key = ?
		 ORDER BY value`, kind, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
