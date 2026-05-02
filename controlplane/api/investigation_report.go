// HTTP layer for the investigation PDF report. Owns the handler,
// federation wrappers, and audit emission.
package api

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"

	"github.com/section9labs/okesu/controlplane/api/investigation_report"
	"github.com/section9labs/okesu/controlplane/audit"
	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/federation"
)

// GetInvestigationReportHandler returns a PDF rendering of the case.
// Loads the same bundle the detail endpoint returns + the case's
// audit log, hands them to the renderer, streams bytes back. Logs
// one audit_log entry per export (best-effort).
func GetInvestigationReportHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := investigationIDFromChi(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		inv, err := store.GetInvestigation(id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		findings, _ := store.ListFindingsForInvestigationEnriched(id)
		runs, _ := store.ListRunsForInvestigationEnriched(id)
		iocs, _ := store.ListIOCsForInvestigation(id)
		daimons, _ := store.ListDaimonsForInvestigation(id)
		orchs, _ := store.ListOrchestrationsForInvestigation(id)
		notes, _ := store.ListInvestigationNotes(id)
		auditEvents, _ := store.ListInvestigationAudit(id)

		bundle := &investigation_report.Bundle{
			Investigation:  *inv,
			Findings:       findings,
			Runs:           runs,
			IOCs:           iocs,
			Daimons:        daimons,
			Orchestrations: orchs,
			Notes:          notes,
		}

		var buf bytes.Buffer
		if err := investigation_report.Render(bundle, auditEvents, &buf); err != nil {
			http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
			return
		}

		filename := fmt.Sprintf("case-%d-%s.pdf", id, investigation_report.Slug(inv.Title))
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
		w.Header().Set("Content-Length", fmt.Sprintf("%d", buf.Len()))
		_, _ = w.Write(buf.Bytes())

		// Best-effort audit. If this fails, we still served the PDF.
		audit.Emit(r, store, db.AuditEntry{
			Action: "investigation.report_exported",
			Target: fmt.Sprintf("investigation:%d", id),
			Metadata: map[string]any{
				"format":     "pdf",
				"size_bytes": buf.Len(),
				"filename":   filename,
			},
		})
	}
}

// FederatedInvestigationReport — parent-side wrapper. Proxies to
// the owning child via ?cp=<instance_id>; falls through to the
// local handler otherwise.
func FederatedInvestigationReport(store *db.Store, agg *federation.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.Replace(r.URL.Path, "/api/investigations/", "/api/v1/federation/investigations/", 1)
		if handled, _ := proxyToCPByQuery(w, r, agg, path); handled {
			return
		}
		GetInvestigationReportHandler(store).ServeHTTP(w, r)
	}
}

// FederationInvestigationReport — child-side, token-authed sibling.
func FederationInvestigationReport(store *db.Store) http.HandlerFunc {
	return requireFederationToken(store, GetInvestigationReportHandler(store))
}
