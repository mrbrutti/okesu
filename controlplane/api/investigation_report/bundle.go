// controlplane/api/investigation_report/bundle.go
//
// Bundle is the renderer's input: same fields the
// GetInvestigationHandler returns to the UI bundle, plus the audit
// log. Owned here so summary/narrative/tables/render all share one
// definition.
package investigation_report

import "github.com/section9labs/okesu/controlplane/db"

// Bundle is the renderer's input — investigation + linked entities.
// Audit log is passed alongside (separately) because it's loaded via
// a different store call.
type Bundle struct {
	Investigation  db.Investigation
	Findings       []db.InvestigationFindingItem
	Runs           []db.InvestigationRunItem
	IOCs           []db.InvestigationIOCItem
	Daimons        []db.InvestigationDaimonItem
	Orchestrations []db.InvestigationOrchestrationItem
	Notes          []db.InvestigationNote
}
