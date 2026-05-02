// Page frame: A4 portrait, 20mm margins, Helvetica/Courier built-in
// fonts, 8pt grey header + footer on every page.
package investigation_report

import (
	"fmt"
	"time"

	"github.com/jung-kurt/gofpdf"
)

// PageWidthMM and PageHeightMM are A4 portrait minus 20mm margins on
// each side. Body height excludes the 8mm header + 8mm footer.
const (
	PageMarginMM   = 20.0
	HeaderHeightMM = 8.0
	FooterHeightMM = 8.0
	BodyTopMM      = PageMarginMM + HeaderHeightMM
	BodyBottomMM   = 297.0 - PageMarginMM - FooterHeightMM // A4 height
	BodyWidthMM    = 210.0 - 2*PageMarginMM                // A4 width
)

// NewDocument creates a fresh A4 portrait PDF with the standard
// margins set. caseID is recorded on the doc-level metadata for
// the footer renderer.
func NewDocument(caseID int64) *gofpdf.Fpdf {
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(PageMarginMM, PageMarginMM, PageMarginMM)
	pdf.SetAutoPageBreak(true, FooterHeightMM+PageMarginMM)
	pdf.SetTitle(fmt.Sprintf("Okesu Investigation Report — Case #%d", caseID), false)
	pdf.SetCreator("Okesu CP", false)
	return pdf
}

// WriteHeader draws the top-of-page header. Call once per page after
// AddPage(); the engine offers no auto-header callback that gets
// the section name right, so we drive header/footer explicitly.
func WriteHeader(pdf *gofpdf.Fpdf, caseID int64, section string, pageNum, pageCount int) {
	if pdf.Err() {
		return
	}
	pdf.SetFont("Helvetica", "", 8)
	pdf.SetTextColor(120, 120, 120)
	left := fmt.Sprintf("Case #%d — %s", caseID, section)
	right := fmt.Sprintf("Page %d / %d", pageNum, pageCount)
	pdf.SetXY(PageMarginMM, PageMarginMM/2)
	pdf.CellFormat(BodyWidthMM/2, 5, left, "", 0, "L", false, 0, "")
	pdf.CellFormat(BodyWidthMM/2, 5, right, "", 0, "R", false, 0, "")
	pdf.SetTextColor(0, 0, 0)
}

// WriteFooter draws the bottom-of-page footer. Call once per page
// before the page is finished.
func WriteFooter(pdf *gofpdf.Fpdf, caseID int64) {
	if pdf.Err() {
		return
	}
	pdf.SetFont("Helvetica", "", 8)
	pdf.SetTextColor(120, 120, 120)
	footer := fmt.Sprintf("Generated %s by Okesu CP — Investigation #%d",
		time.Now().UTC().Format("2006-01-02 15:04:05 UTC"), caseID)
	pdf.SetY(297.0 - PageMarginMM/2 - 4)
	pdf.CellFormat(BodyWidthMM, 5, footer, "", 0, "C", false, 0, "")
	pdf.SetTextColor(0, 0, 0)
}
