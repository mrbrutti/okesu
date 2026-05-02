// Package db — investigation graph helper.
//
// GetInvestigationGraphData returns the data the API serialises into
// the bipartite graph response: top-N findings by severity desc + Ts
// desc, plus the linked IOCs (with finding-id linkage from
// ioc_observations). Hosts and daimons are derived in Go from the
// findings — no extra query.
package db

// GraphFinding is the minimal finding shape the graph endpoint needs.
// Hosts + daimons are derived from these in the API layer.
type GraphFinding struct {
	ID       int64
	Ts       int64
	Agent    string
	Host     string
	Severity string
	Title    string
}

// GraphIOC carries one IOC + the set of finding IDs linked to it
// within the kept-findings scope.
type GraphIOC struct {
	ID         int64
	Kind       string
	Value      string
	ObsCount   int64
	HostCount  int64
	FindingIDs []int64
}

// GraphData is the store-layer bundle GetInvestigationGraphData
// returns. The API serialises this into the {nodes, edges} wire
// shape.
type GraphData struct {
	TotalFindings int
	LimitApplied  int
	Findings      []GraphFinding
	IOCs          []GraphIOC
}

// graphLimitClamp normalises the operator-supplied limit to [1, 100],
// defaulting to 20 when zero.
func graphLimitClamp(limit int) int {
	if limit <= 0 {
		return 20
	}
	if limit > 100 {
		return 100
	}
	return limit
}

// graphSeverityRankSQL maps severity strings to sort weights. Mirrors
// the rank used by the investigation report renderer.
const graphSeverityRankSQL = `
CASE COALESCE(f.severity, '')
    WHEN 'CRITICAL' THEN 5
    WHEN 'HIGH'     THEN 4
    WHEN 'MEDIUM'   THEN 3
    WHEN 'LOW'      THEN 2
    WHEN 'INFO'     THEN 1
    ELSE 0
END`

// GetInvestigationGraphData returns the kept findings + linked IOCs
// scoped to those findings. Total finding count is reported alongside
// LimitApplied so the UI can render the "N of M shown" banner.
func (s *Store) GetInvestigationGraphData(invID int64, limit int) (*GraphData, error) {
	limit = graphLimitClamp(limit)
	out := &GraphData{LimitApplied: limit}

	// Total findings on the case (independent of limit).
	if err := s.QueryRow(`
		SELECT COUNT(*) FROM investigation_findings
		 WHERE investigation_id = ?`, invID).Scan(&out.TotalFindings); err != nil {
		return nil, err
	}
	if out.TotalFindings == 0 {
		return out, nil
	}

	// Kept findings: top-N by severity rank desc, then Ts desc.
	rows, err := s.Query(`
		SELECT f.id, f.ts, COALESCE(f.agent, ''), COALESCE(f.host, ''),
		       COALESCE(f.severity, ''), COALESCE(f.title, '')
		  FROM investigation_findings inv
		  JOIN findings f ON f.id = inv.finding_id
		 WHERE inv.investigation_id = ?
		 ORDER BY `+graphSeverityRankSQL+` DESC, f.ts DESC, f.id DESC
		 LIMIT ?`, invID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keptIDs := make([]int64, 0, limit)
	for rows.Next() {
		var f GraphFinding
		if err := rows.Scan(&f.ID, &f.Ts, &f.Agent, &f.Host, &f.Severity, &f.Title); err != nil {
			return nil, err
		}
		out.Findings = append(out.Findings, f)
		keptIDs = append(keptIDs, f.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(keptIDs) == 0 {
		return out, nil
	}

	// IOC linkage: one row per (ioc_id, finding_id).
	q, args := buildInClause(`
		SELECT obs.finding_id, i.id, i.kind, COALESCE(i.normalized_value, '')
		  FROM ioc_observations obs
		  JOIN iocs i ON i.id = obs.ioc_id
		 WHERE obs.finding_id IN `, keptIDs)
	linkRows, err := s.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer linkRows.Close()
	type iocAcc struct {
		Kind, Value string
		Findings    map[int64]bool
	}
	byIOC := map[int64]*iocAcc{}
	for linkRows.Next() {
		var fid, iid int64
		var kind, value string
		if err := linkRows.Scan(&fid, &iid, &kind, &value); err != nil {
			return nil, err
		}
		acc, ok := byIOC[iid]
		if !ok {
			acc = &iocAcc{Kind: kind, Value: value, Findings: map[int64]bool{}}
			byIOC[iid] = acc
		}
		acc.Findings[fid] = true
	}
	if err := linkRows.Err(); err != nil {
		return nil, err
	}

	// Aggregated obs/host counts per IOC (scoped to kept findings).
	q2, args2 := buildInClause(`
		SELECT obs.ioc_id,
		       COUNT(obs.id) AS obs_count,
		       COUNT(DISTINCT COALESCE(obs.host, '')) AS host_count
		  FROM ioc_observations obs
		 WHERE obs.finding_id IN `, keptIDs)
	q2 += ` GROUP BY obs.ioc_id`
	aggRows, err := s.Query(q2, args2...)
	if err != nil {
		return nil, err
	}
	defer aggRows.Close()
	type aggCounts struct {
		Obs, Hosts int64
	}
	aggByIOC := map[int64]aggCounts{}
	for aggRows.Next() {
		var iid int64
		var obs, hosts int64
		if err := aggRows.Scan(&iid, &obs, &hosts); err != nil {
			return nil, err
		}
		aggByIOC[iid] = aggCounts{Obs: obs, Hosts: hosts}
	}
	if err := aggRows.Err(); err != nil {
		return nil, err
	}

	out.IOCs = make([]GraphIOC, 0, len(byIOC))
	for iid, acc := range byIOC {
		fids := make([]int64, 0, len(acc.Findings))
		for fid := range acc.Findings {
			fids = append(fids, fid)
		}
		counts := aggByIOC[iid]
		out.IOCs = append(out.IOCs, GraphIOC{
			ID:         iid,
			Kind:       acc.Kind,
			Value:      acc.Value,
			ObsCount:   counts.Obs,
			HostCount:  counts.Hosts,
			FindingIDs: fids,
		})
	}
	return out, nil
}

// buildInClause expands a slice of int64 IDs into a parameterised
// `IN (?, ?, ...)` clause with the corresponding args slice. Used for
// the IOC linkage queries above. Empty input panics — callers must
// guard.
func buildInClause(prefix string, ids []int64) (string, []any) {
	if len(ids) == 0 {
		panic("buildInClause: empty ids")
	}
	q := prefix + "("
	args := make([]any, 0, len(ids))
	for i, id := range ids {
		if i > 0 {
			q += ", "
		}
		q += "?"
		args = append(args, id)
	}
	q += ")"
	return q, args
}
