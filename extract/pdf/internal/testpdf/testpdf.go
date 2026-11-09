// Package testpdf builds minimal synthetic PDFs for tests of the PDF engines.
// The fixtures are generated, not committed, so their contents are plain to see.
package testpdf

import (
	"bytes"
	"fmt"
	"strings"
)

// Page is one page of a synthetic PDF: its content stream and extra page
// dictionary entries (e.g. "/Rotate 90 /CropBox [...]").
type Page struct {
	Content string
	Extra   string
}

// Build returns a minimal PDF with the given pages on a 612 x 792 MediaBox,
// with Helvetica (not embedded) as /F1 on every page. Offsets are computed so
// the xref is valid.
func Build(pages ...Page) []byte {
	// Objects: 1 catalog, 2 pages, 3 font, then a page and its content stream
	// for each page (4+2i and 5+2i).
	kids := make([]string, len(pages))
	for i := range pages {
		kids[i] = fmt.Sprintf("%d 0 R", 4+2*i)
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	var offsets []int
	obj := func(s string) { offsets = append(offsets, b.Len()); b.WriteString(s) }
	obj("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	obj(fmt.Sprintf("2 0 obj\n<< /Type /Pages /Kids [%s] /Count %d >>\nendobj\n",
		strings.Join(kids, " "), len(pages)))
	obj("3 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>\nendobj\n")
	for i := range pages {
		pg := &pages[i]
		obj(fmt.Sprintf("%d 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] %s "+
			"/Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>\nendobj\n", 4+2*i, pg.Extra, 5+2*i))
		obj(fmt.Sprintf("%d 0 obj\n<< /Length %d >>\nstream\n%sendstream\nendobj\n", 5+2*i, len(pg.Content), pg.Content))
	}
	xrefPos := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n", len(offsets)+1)
	b.WriteString("0000000000 65535 f \n")
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xrefPos)
	return b.Bytes()
}
