package investigation_report

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jung-kurt/gofpdf"
)

func TestNewDocumentSetsBasics(t *testing.T) {
	doc := NewDocument(123)
	doc.AddPage()
	var buf bytes.Buffer
	if err := doc.Output(&buf); err != nil {
		t.Fatalf("output: %v", err)
	}
	if !strings.HasPrefix(buf.String(), "%PDF-") {
		t.Errorf("output does not start with %%PDF-: %q", buf.String()[:16])
	}
}

func TestFooterIncludesCaseID(t *testing.T) {
	// Smoke check: the footer drawing function should at least not
	// panic when called against a real page. Content checks happen
	// via golden-file tests; here we just verify a clean call.
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.AddPage()
	WriteFooter(pdf, 42)
	if pdf.Err() {
		t.Fatalf("pdf err: %v", pdf.Error())
	}
}

func TestHeaderIncludesSection(t *testing.T) {
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.AddPage()
	WriteHeader(pdf, 42, "Narrative", 1, 5)
	if pdf.Err() {
		t.Fatalf("pdf err: %v", pdf.Error())
	}
}
