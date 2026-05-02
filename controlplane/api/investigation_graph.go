// HTTP layer for the investigation graph view. Handler builds the
// {nodes, edges} wire shape from db.GraphData and serialises as JSON.
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/federation"
)

// graphNode is one entry in the wire shape's nodes array.
type graphNode struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"` // finding | host | daimon | ioc
	Label        string `json:"label"`
	Severity     string `json:"severity,omitempty"`      // findings only
	Host         string `json:"host,omitempty"`          // findings only
	Agent        string `json:"agent,omitempty"`         // findings only
	FindingCount int64  `json:"finding_count,omitempty"` // daimons only
	IOCKind      string `json:"ioc_kind,omitempty"`      // iocs only
	IOCValue     string `json:"ioc_value,omitempty"`     // iocs only
	ObsCount     int64  `json:"obs_count,omitempty"`     // iocs only
	HostCount    int64  `json:"host_count,omitempty"`    // iocs only
}

// graphEdge is one entry in the wire shape's edges array.
type graphEdge struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Target string `json:"target"`
}

// graphResponse mirrors the spec's wire shape.
type graphResponse struct {
	TotalFindings int         `json:"total_findings"`
	LimitApplied  int         `json:"limit_applied"`
	Nodes         []graphNode `json:"nodes"`
	Edges         []graphEdge `json:"edges"`
}

// GetInvestigationGraphHandler serves the bipartite graph data.
func GetInvestigationGraphHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := investigationIDFromChi(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Existence check so we return 404 not an empty graph for
		// unknown IDs.
		if _, err := store.GetInvestigation(id); err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		limit := 20
		if s := r.URL.Query().Get("limit"); s != "" {
			if n, err := strconv.Atoi(s); err == nil {
				limit = n
			}
		}

		data, err := store.GetInvestigationGraphData(id, limit)
		if err != nil {
			http.Error(w, "graph: "+err.Error(), http.StatusInternalServerError)
			return
		}

		resp := buildGraphResponse(data)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// buildGraphResponse converts db.GraphData into the wire shape:
// derives host + daimon nodes from the findings; emits edges per
// finding.
func buildGraphResponse(d *db.GraphData) graphResponse {
	resp := graphResponse{
		TotalFindings: d.TotalFindings,
		LimitApplied:  d.LimitApplied,
		Nodes:         []graphNode{},
		Edges:         []graphEdge{},
	}

	// Track distinct hosts and daimons (with finding counts) keyed by
	// their string identity so we emit each as a single node.
	hostFindings := map[string]bool{} // existence — used for node creation
	daimonFindings := map[string]int64{}

	// Findings → finding nodes; collect host + daimon edges.
	for _, f := range d.Findings {
		fNode := graphNode{
			ID:       fmt.Sprintf("f:%d", f.ID),
			Kind:     "finding",
			Label:    f.Title,
			Severity: f.Severity,
			Host:     f.Host,
			Agent:    f.Agent,
		}
		resp.Nodes = append(resp.Nodes, fNode)

		if f.Host != "" {
			hostID := "h:" + f.Host
			hostFindings[f.Host] = true
			resp.Edges = append(resp.Edges, graphEdge{
				ID:     fNode.ID + "|" + hostID,
				Source: fNode.ID,
				Target: hostID,
			})
		}
		if f.Agent != "" {
			daimonID := "d:" + f.Agent
			daimonFindings[f.Agent]++
			resp.Edges = append(resp.Edges, graphEdge{
				ID:     fNode.ID + "|" + daimonID,
				Source: fNode.ID,
				Target: daimonID,
			})
		}
	}

	// Host nodes.
	for host := range hostFindings {
		resp.Nodes = append(resp.Nodes, graphNode{
			ID:    "h:" + host,
			Kind:  "host",
			Label: host,
		})
	}

	// Daimon nodes.
	for agent, count := range daimonFindings {
		resp.Nodes = append(resp.Nodes, graphNode{
			ID:           "d:" + agent,
			Kind:         "daimon",
			Label:        agent,
			FindingCount: count,
		})
	}

	// IOC nodes + edges.
	for _, i := range d.IOCs {
		iocID := fmt.Sprintf("i:%s:%s", i.Kind, i.Value)
		// Truncated label: kind:...last4 (matches SmartPayload IOC chip).
		label := i.Kind + ":" + lastN(i.Value, 4)
		resp.Nodes = append(resp.Nodes, graphNode{
			ID:        iocID,
			Kind:      "ioc",
			Label:     label,
			IOCKind:   i.Kind,
			IOCValue:  i.Value,
			ObsCount:  i.ObsCount,
			HostCount: i.HostCount,
		})
		for _, fid := range i.FindingIDs {
			fNodeID := fmt.Sprintf("f:%d", fid)
			resp.Edges = append(resp.Edges, graphEdge{
				ID:     fNodeID + "|" + iocID,
				Source: fNodeID,
				Target: iocID,
			})
		}
	}

	return resp
}

// lastN returns the last N characters of s, or all of s when shorter.
// Pure helper; unit-tested via the handler tests.
func lastN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// FederatedInvestigationGraph — parent-side wrapper. Proxies to the
// owning child via ?cp=<instance_id>; falls through to the local
// handler otherwise.
func FederatedInvestigationGraph(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.Replace(r.URL.Path, "/api/investigations/", "/api/v1/federation/investigations/", 1)
		if handled, _ := proxyToCPByQuery(w, r, agg, path); handled {
			return
		}
		GetInvestigationGraphHandler(store).ServeHTTP(w, r)
	}
}

// FederationInvestigationGraph — child-side, token-authed sibling.
func FederationInvestigationGraph(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, GetInvestigationGraphHandler(store))
}
