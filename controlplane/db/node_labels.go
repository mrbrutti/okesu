// Node-specific label helpers. Phase 22.8 PR β / Phase 22.9.
//
// Migration 051 unified node labels into the generic `labels` table;
// these helpers remain as a thin shim so the dozens of existing call
// sites (HasEffectiveRoleOnNode, FilterVisibleNodes, the orchestrator
// selector resolver, the Node detail UI) stay intact. New code should
// reach for the generic Set/DeleteLabel via target_kind = "node".
//
// Backs the selector grammar (selector.go) and the per-node label
// editor on the Nodes page. Labels are admin-managed; node daemons
// don't author labels — that would invert the trust model (a
// compromised node could claim env=prod and inherit credentials).

package db

import (
	"strings"
)

// SetNodeLabel routes through the generic labels store.
func (s *Store) SetNodeLabel(nodeID int64, key, value string) error {
	return s.SetLabel(LabelKindNode, nodeID, "", key, value, "manual")
}

// DeleteNodeLabel routes through the generic labels store.
func (s *Store) DeleteNodeLabel(nodeID int64, key string) error {
	return s.DeleteLabel(LabelKindNode, nodeID, "", key)
}

// ListNodeLabels reads the label map for one node from the generic
// labels store. Empty map (not nil) when no labels are set.
func (s *Store) ListNodeLabels(nodeID int64) (map[string]string, error) {
	return s.ListLabels(LabelKindNode, nodeID, "")
}

// ListLabelsForNodes batches the per-node lookup. Identical shape to
// the pre-migration helper; callers (FilterVisibleNodes, the Nodes
// list page) keep working unchanged.
func (s *Store) ListLabelsForNodes(nodeIDs []int64) (map[int64]map[string]string, error) {
	return s.ListLabelsBatchByID(LabelKindNode, nodeIDs)
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

// MatchNodesBySelector returns node ids whose labels satisfy `sel`.
// Used by orchestration steps with a `nodes_selector:` field — the
// engine resolves the selector at run time, fans out to every match.
//
// Empty selector behaviour: matches every node. Callers that want
// "no match" semantics should validate before parsing.
func (s *Store) MatchNodesBySelector(sel Selector) ([]int64, error) {
	if sel.IsEmpty() {
		// Match-all: pull every node. Cap at 10k — fleets larger than
		// that need pagination plumbed through the call sites first.
		rows, err := s.Query(`SELECT id FROM nodes ORDER BY id LIMIT 10000`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return nil, err
			}
			out = append(out, id)
		}
		return out, rows.Err()
	}
	targets, err := s.FindTargetsBySelector(LabelKindNode, sel)
	if err != nil {
		return nil, err
	}
	out := make([]int64, 0, len(targets))
	for _, t := range targets {
		if t.ID > 0 {
			out = append(out, t.ID)
		}
	}
	return out, nil
}

// _ keeps the strings import alive when this file's body shrinks
// during refactors that move helpers into labels.go.
var _ = strings.Builder{}
