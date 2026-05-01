// Node labels store. Phase 22.8 PR β.
//
// Backs the selector grammar (selector.go) and the per-node label
// editor on the Nodes page. Labels are admin-managed; node daemons
// don't author labels — that would invert the trust model (a
// compromised node could claim env=prod and inherit credentials).

package db

import (
	"errors"
	"strings"
)

// SetNodeLabel upserts a label. Empty key is rejected; empty value
// is allowed (rare but legitimate — `env=` semantically matches
// only nodes that have the label at all).
func (s *Store) SetNodeLabel(nodeID int64, key, value string) error {
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
	_, err := s.Exec(`
		INSERT INTO node_labels (node_id, key, value)
		VALUES (?, ?, ?)
		ON CONFLICT (node_id, key) DO UPDATE SET
		  value = excluded.value,
		  updated_at = CURRENT_TIMESTAMP
	`, nodeID, key, value)
	return err
}

// DeleteNodeLabel removes one (node, key) pair. Idempotent — calling
// twice is fine.
func (s *Store) DeleteNodeLabel(nodeID int64, key string) error {
	_, err := s.Exec(`DELETE FROM node_labels WHERE node_id = ? AND key = ?`, nodeID, key)
	return err
}

// ListNodeLabels returns the label map for a node. Empty map (not
// nil) when the node has no labels — JSON consumers see {} not null.
func (s *Store) ListNodeLabels(nodeID int64) (map[string]string, error) {
	rows, err := s.Query(`SELECT key, value FROM node_labels WHERE node_id = ?`, nodeID)
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

// ListLabelsForNodes batches label lookups so the Nodes-list page
// doesn't have to N+1. Returns map of node_id → label map. Missing
// entries (nodes with no labels) are absent from the outer map.
func (s *Store) ListLabelsForNodes(nodeIDs []int64) (map[int64]map[string]string, error) {
	if len(nodeIDs) == 0 {
		return map[int64]map[string]string{}, nil
	}
	// Build the IN clause manually — sqlite doesn't take []int64 as a
	// single placeholder. We've capped fleet size in the lab at <100;
	// production caps are well under 10k where this approach is fine.
	placeholders := make([]string, len(nodeIDs))
	args := make([]any, len(nodeIDs))
	for i, id := range nodeIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	q := `SELECT node_id, key, value FROM node_labels WHERE node_id IN (` +
		strings.Join(placeholders, ",") + `)`
	rows, err := s.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]map[string]string{}
	for rows.Next() {
		var nid int64
		var k, v string
		if err := rows.Scan(&nid, &k, &v); err != nil {
			return nil, err
		}
		m, ok := out[nid]
		if !ok {
			m = map[string]string{}
			out[nid] = m
		}
		m[k] = v
	}
	return out, rows.Err()
}

// HasEffectiveRoleOnNode is the resource-scoped gate that PR β
// introduces. Returns true iff the user has any group_role with the
// requested role (or a stronger one in the role-implication ladder)
// AND a selector that matches the target node's labels.
//
// CP-wide grants (selector NULL/empty) match any node. So an admin
// in default-admin still passes for every node — pre-PR-β behaviour
// is preserved unchanged. Scoped grants only kick in when admins
// start authoring them.
//
// Synthetic users (federation / API token paths where userID == 0)
// short-circuit to false here — those callers should keep using the
// CP-wide HasEffectiveRole + their own resource gating, not this.
func (s *Store) HasEffectiveRoleOnNode(userID, nodeID int64, needed string) (bool, error) {
	if userID == 0 {
		return false, nil
	}
	// Pull the labels first — one cheap query that's reused if the
	// user has multiple group_roles.
	labels, err := s.ListNodeLabels(nodeID)
	if err != nil {
		return false, err
	}
	rows, err := s.Query(`
		SELECT gr.role, COALESCE(gr.selector, '')
		  FROM user_groups ug
		  JOIN group_roles gr ON gr.group_id = ug.group_id
		 WHERE ug.user_id = ?`, userID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var role, selectorStr string
		if err := rows.Scan(&role, &selectorStr); err != nil {
			return false, err
		}
		if !roleSatisfies(role, needed) {
			continue
		}
		sel, err := ParseSelector(selectorStr)
		if err != nil {
			// Malformed selectors don't grant access — we'd rather
			// fail closed than implicitly broaden to "match all".
			continue
		}
		if sel.Matches(labels) {
			return true, nil
		}
	}
	return false, rows.Err()
}

// FilterVisibleNodes returns the subset of `nodeIDs` the user can
// see at the requested role. Used by list endpoints to scope
// results without leaking metadata about nodes outside the user's
// scope. Users with at least one CP-wide grant for the role short-
// circuit to "see everything" — preserves the existing UX for
// today's admins/operators.
func (s *Store) FilterVisibleNodes(userID int64, role string, nodeIDs []int64) ([]int64, error) {
	if len(nodeIDs) == 0 {
		return nodeIDs, nil
	}
	// CP-wide check first.
	row := s.QueryRow(`
		SELECT 1 FROM user_groups ug
		  JOIN group_roles gr ON gr.group_id = ug.group_id
		 WHERE ug.user_id = ?
		   AND (gr.selector IS NULL OR gr.selector = '')
		 LIMIT 1`, userID)
	var present int
	if err := row.Scan(&present); err == nil && present == 1 {
		return nodeIDs, nil
	}

	// Need to evaluate per-node. Pull labels for every candidate +
	// the user's grants once, then test in memory.
	labelsByNode, err := s.ListLabelsForNodes(nodeIDs)
	if err != nil {
		return nil, err
	}
	rows, err := s.Query(`
		SELECT gr.role, COALESCE(gr.selector, '')
		  FROM user_groups ug
		  JOIN group_roles gr ON gr.group_id = ug.group_id
		 WHERE ug.user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	type grant struct {
		role string
		sel  Selector
	}
	var grants []grant
	for rows.Next() {
		var r, s string
		if err := rows.Scan(&r, &s); err != nil {
			rows.Close()
			return nil, err
		}
		sel, perr := ParseSelector(s)
		if perr != nil {
			continue
		}
		grants = append(grants, grant{role: r, sel: sel})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]int64, 0, len(nodeIDs))
	for _, nid := range nodeIDs {
		labels := labelsByNode[nid]
		for _, g := range grants {
			if !roleSatisfies(g.role, role) {
				continue
			}
			if g.sel.Matches(labels) {
				out = append(out, nid)
				break
			}
		}
	}
	return out, nil
}
