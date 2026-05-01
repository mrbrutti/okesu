// Cross-entity visibility filters. Phase 22.9.
//
// FilterVisibleNodes (in node_labels.go) handles the node case
// directly. Findings and runs scope through their host: derive the
// node from the finding/run's host string, evaluate the user's
// scoped grants against that node's labels.
//
// Same CP-wide grant short-circuit as FilterVisibleNodes — a user
// in default-admin (or any group with a CP-wide selector="" grant)
// short-circuits to "see everything," preserving today's behaviour
// for unscoped operators.

package db

import (
	"strings"
)

// FilterVisibleFindings returns the subset of finding IDs the user
// can see at the requested role. CP-wide grants short-circuit to
// "see everything"; otherwise we resolve each finding to its host's
// node row, pull labels, and evaluate the user's selectors.
//
// Findings whose host can't be resolved to a node fall through and
// are kept visible — better to over-show than to hide a finding the
// scope might cover. Operators who really need exclude-on-orphan
// semantics can configure a negative selector (`!host`) which would
// require labels that the host doesn't have.
func (s *Store) FilterVisibleFindings(userID int64, role string, findingIDs []int64) ([]int64, error) {
	if len(findingIDs) == 0 {
		return findingIDs, nil
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
		return findingIDs, nil
	}

	// Pull the user's scoped grants once.
	grants, err := loadUserGrants(s, userID)
	if err != nil {
		return nil, err
	}

	// Build (finding_id → host) and the host→node_id resolver.
	hostByFinding, err := s.findingHosts(findingIDs)
	if err != nil {
		return nil, err
	}
	hosts := make([]string, 0, len(hostByFinding))
	seen := map[string]bool{}
	for _, h := range hostByFinding {
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		hosts = append(hosts, h)
	}
	nodeByHost, err := s.nodeIDsByHost(hosts)
	if err != nil {
		return nil, err
	}
	nodeIDs := make([]int64, 0, len(nodeByHost))
	for _, id := range nodeByHost {
		nodeIDs = append(nodeIDs, id)
	}
	labelsByNode, err := s.ListLabelsForNodes(nodeIDs)
	if err != nil {
		return nil, err
	}

	out := make([]int64, 0, len(findingIDs))
	for _, fid := range findingIDs {
		host := hostByFinding[fid]
		if host == "" {
			out = append(out, fid) // host-less findings stay visible
			continue
		}
		nid, ok := nodeByHost[host]
		if !ok {
			out = append(out, fid) // unresolvable host stays visible
			continue
		}
		labels := labelsByNode[nid]
		for _, g := range grants {
			if !roleSatisfies(g.role, role) {
				continue
			}
			if g.sel.Matches(labels) {
				out = append(out, fid)
				break
			}
		}
	}
	return out, nil
}

// FilterVisibleRuns returns the subset of run IDs the user can see
// at the requested role. Same model as FilterVisibleFindings — runs
// scope through their NodeName.
func (s *Store) FilterVisibleRuns(userID int64, role string, runIDs []string) ([]string, error) {
	if len(runIDs) == 0 {
		return runIDs, nil
	}
	row := s.QueryRow(`
		SELECT 1 FROM user_groups ug
		  JOIN group_roles gr ON gr.group_id = ug.group_id
		 WHERE ug.user_id = ?
		   AND (gr.selector IS NULL OR gr.selector = '')
		 LIMIT 1`, userID)
	var present int
	if err := row.Scan(&present); err == nil && present == 1 {
		return runIDs, nil
	}

	grants, err := loadUserGrants(s, userID)
	if err != nil {
		return nil, err
	}

	hostByRun, err := s.runHosts(runIDs)
	if err != nil {
		return nil, err
	}
	hosts := make([]string, 0, len(hostByRun))
	seen := map[string]bool{}
	for _, h := range hostByRun {
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		hosts = append(hosts, h)
	}
	nodeByHost, err := s.nodeIDsByHost(hosts)
	if err != nil {
		return nil, err
	}
	nodeIDs := make([]int64, 0, len(nodeByHost))
	for _, id := range nodeByHost {
		nodeIDs = append(nodeIDs, id)
	}
	labelsByNode, err := s.ListLabelsForNodes(nodeIDs)
	if err != nil {
		return nil, err
	}

	out := make([]string, 0, len(runIDs))
	for _, rid := range runIDs {
		host := hostByRun[rid]
		if host == "" {
			out = append(out, rid)
			continue
		}
		nid, ok := nodeByHost[host]
		if !ok {
			out = append(out, rid)
			continue
		}
		labels := labelsByNode[nid]
		for _, g := range grants {
			if !roleSatisfies(g.role, role) {
				continue
			}
			if g.sel.Matches(labels) {
				out = append(out, rid)
				break
			}
		}
	}
	return out, nil
}

// userGrant is the parsed (role, selector) tuple for one row of the
// user's scoped grants.
type userGrant struct {
	role string
	sel  Selector
}

func loadUserGrants(s *Store, userID int64) ([]userGrant, error) {
	rows, err := s.Query(`
		SELECT gr.role, COALESCE(gr.selector, '')
		  FROM user_groups ug
		  JOIN group_roles gr ON gr.group_id = ug.group_id
		 WHERE ug.user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []userGrant
	for rows.Next() {
		var role, selectorStr string
		if err := rows.Scan(&role, &selectorStr); err != nil {
			return nil, err
		}
		sel, perr := ParseSelector(selectorStr)
		if perr != nil {
			continue
		}
		out = append(out, userGrant{role: role, sel: sel})
	}
	return out, rows.Err()
}

// findingHosts batches the (id → host) lookup. Returns empty string
// for findings with NULL host.
func (s *Store) findingHosts(ids []int64) (map[int64]string, error) {
	if len(ids) == 0 {
		return map[int64]string{}, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	q := `SELECT id, COALESCE(host, '') FROM findings WHERE id IN (` +
		strings.Join(placeholders, ",") + `)`
	rows, err := s.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]string, len(ids))
	for rows.Next() {
		var id int64
		var h string
		if err := rows.Scan(&id, &h); err != nil {
			return nil, err
		}
		out[id] = h
	}
	return out, rows.Err()
}

// runHosts batches the (id → node_name) lookup for runs.
func (s *Store) runHosts(ids []string) (map[string]string, error) {
	if len(ids) == 0 {
		return map[string]string{}, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	q := `SELECT id, COALESCE(node_name, '') FROM runs WHERE id IN (` +
		strings.Join(placeholders, ",") + `)`
	rows, err := s.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string, len(ids))
	for rows.Next() {
		var id string
		var h string
		if err := rows.Scan(&id, &h); err != nil {
			return nil, err
		}
		out[id] = h
	}
	return out, rows.Err()
}

// nodeIDsByHost resolves a batch of host strings to node IDs. Falls
// through three name forms — daemon_hostname, hostname, then name —
// in that order, picking the first match. Hosts with no matching
// node row are absent from the result map.
func (s *Store) nodeIDsByHost(hosts []string) (map[string]int64, error) {
	if len(hosts) == 0 {
		return map[string]int64{}, nil
	}
	placeholders := make([]string, len(hosts))
	for i := range hosts {
		placeholders[i] = "?"
	}
	in := strings.Join(placeholders, ",")
	args := make([]any, 0, len(hosts)*3)
	// We OR three column equality groups; each contributes len(hosts)
	// args. Three passes through the IN list keeps the query single-
	// statement and indexable.
	q := `SELECT id, daemon_hostname, hostname, name FROM nodes
	       WHERE daemon_hostname IN (` + in + `)
	          OR hostname         IN (` + in + `)
	          OR name             IN (` + in + `)`
	for i := 0; i < 3; i++ {
		for _, h := range hosts {
			args = append(args, h)
		}
	}
	rows, err := s.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hostSet := make(map[string]bool, len(hosts))
	for _, h := range hosts {
		hostSet[h] = true
	}
	out := map[string]int64{}
	for rows.Next() {
		var id int64
		var dh, hn, nm string
		var dhN, hnN, nmN any // sql.NullString-equivalent — scan into any handles NULL safely
		_ = dh
		_ = hn
		_ = nm
		if err := rows.Scan(&id, &dhN, &hnN, &nmN); err != nil {
			return nil, err
		}
		dh = scanString(dhN)
		hn = scanString(hnN)
		nm = scanString(nmN)
		// Pick the first column form that matches an input host.
		if dh != "" && hostSet[dh] {
			if _, exists := out[dh]; !exists {
				out[dh] = id
			}
			continue
		}
		if hn != "" && hostSet[hn] {
			if _, exists := out[hn]; !exists {
				out[hn] = id
			}
			continue
		}
		if nm != "" && hostSet[nm] {
			if _, exists := out[nm]; !exists {
				out[nm] = id
			}
		}
	}
	return out, rows.Err()
}

// scanString unwraps the any-typed value Scan handed us into a Go
// string. NULL → "".
func scanString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	case nil:
		return ""
	}
	return ""
}
