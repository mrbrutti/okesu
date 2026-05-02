package api

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/section9labs/okesu/controlplane/db"
)

func TestInvestigationReport_Returns404OnUnknown(t *testing.T) {
	store := newSeededTestStore(t)
	router := chi.NewRouter()
	router.Get("/api/investigations/{id}/report.pdf", GetInvestigationReportHandler(store))

	req := httptest.NewRequest("GET", "/api/investigations/99999/report.pdf", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestInvestigationReport_HappyPath(t *testing.T) {
	store := newSeededTestStore(t)
	id, err := store.CreateInvestigation(&db.InvestigationInsert{Title: "Test case"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	router := chi.NewRouter()
	router.Get("/api/investigations/{id}/report.pdf", GetInvestigationReportHandler(store))

	req := httptest.NewRequest("GET", "/api/investigations/"+strconv.FormatInt(id, 10)+"/report.pdf", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/pdf" {
		t.Errorf("Content-Type = %q", got)
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") || !strings.Contains(cd, "case-") || !strings.Contains(cd, ".pdf") {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if !strings.HasPrefix(rec.Body.String(), "%PDF-") {
		t.Errorf("body does not start with %%PDF-")
	}
}

func TestInvestigationReport_AuditLogged(t *testing.T) {
	store := newSeededTestStore(t)
	id, err := store.CreateInvestigation(&db.InvestigationInsert{Title: "Test case"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	router := chi.NewRouter()
	router.Get("/api/investigations/{id}/report.pdf", GetInvestigationReportHandler(store))

	req := httptest.NewRequest("GET", "/api/investigations/"+strconv.FormatInt(id, 10)+"/report.pdf", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	rows, err := store.ListAudit(db.AuditFilter{Action: "investigation.report_exported", Limit: 10})
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(rows))
	}
	if got := rows[0].Target.String; got != "investigation:"+strconv.FormatInt(id, 10) {
		t.Errorf("Target = %q", got)
	}
}
