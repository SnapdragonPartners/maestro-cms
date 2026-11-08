package pdftotext_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/SnapdragonPartners/maestro-cms/extract"
	"github.com/SnapdragonPartners/maestro-cms/extract/pdf"
	"github.com/SnapdragonPartners/maestro-cms/extract/pdf/pdftotext"
)

var (
	_ pdf.Engine       = (*pdftotext.Engine)(nil)
	_ pdf.ModeReporter = (*pdftotext.Engine)(nil)
)

func TestMode(t *testing.T) {
	if got := pdftotext.New().Mode(); got != pdf.ModeRaw {
		t.Fatalf("default Mode = %q, want %q", got, pdf.ModeRaw)
	}
	if got := pdftotext.New(pdftotext.WithLayout()).Mode(); got != pdf.ModeLayout {
		t.Fatalf("WithLayout Mode = %q, want %q", got, pdf.ModeLayout)
	}
}

func TestEngineUnavailable(t *testing.T) {
	eng := pdftotext.New(pdftotext.WithBinary("maestro-cms-no-such-binary-xyz"))
	_, err := eng.Pages(context.Background(), []byte("%PDF-1.4"))
	if !errors.Is(err, pdftotext.ErrEngineUnavailable) {
		t.Fatalf("err = %v, want ErrEngineUnavailable", err)
	}
}

// makeTextPDF builds a minimal one-page PDF with a text content stream that
// pdftotext can extract.
func makeTextPDF(text string) []byte {
	return makePDF("BT /F1 24 Tf 72 720 Td (" + text + ") Tj ET\n")
}

// statementRows is a capital-statement-shaped table: a label column at x=72 and
// a value column at x=400.
var statementRows = [][2]string{
	{"Beginning balance", "1,000.00"},
	{"Contributions", "250.00"},
	{"Distributions", "(100.00)"},
	{"Ending balance", "1,150.00"},
}

// makeStatementPDF builds a one-page PDF laying statementRows out as two
// columns. pdftotext's default mode emits the labels and the values as separate
// blocks for this layout; -layout keeps each row on one line.
func makeStatementPDF() []byte {
	var c strings.Builder
	c.WriteString("BT /F1 11 Tf\n")
	y := 700
	for _, r := range statementRows {
		fmt.Fprintf(&c, "1 0 0 1 72 %d Tm (%s) Tj\n1 0 0 1 400 %d Tm (%s) Tj\n", y, r[0], y, r[1])
		y -= 18
	}
	c.WriteString("ET\n")
	return makePDF(c.String())
}

// makePDF builds a minimal PDF with one page per content stream (Helvetica as
// /F1 on every page). Offsets are computed so the xref is valid.
func makePDF(pageContents ...string) []byte {
	// Objects: 1 catalog, 2 pages, 3 font, then a page and its content stream
	// for each page (4+2i and 5+2i).
	kids := make([]string, len(pageContents))
	for i := range pageContents {
		kids[i] = fmt.Sprintf("%d 0 R", 4+2*i)
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	var offsets []int
	obj := func(s string) { offsets = append(offsets, b.Len()); b.WriteString(s) }
	obj("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	obj(fmt.Sprintf("2 0 obj\n<< /Type /Pages /Kids [%s] /Count %d >>\nendobj\n",
		strings.Join(kids, " "), len(pageContents)))
	obj("3 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>\nendobj\n")
	for i, content := range pageContents {
		obj(fmt.Sprintf("%d 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] "+
			"/Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>\nendobj\n", 4+2*i, 5+2*i))
		obj(fmt.Sprintf("%d 0 obj\n<< /Length %d >>\nstream\n%sendstream\nendobj\n", 5+2*i, len(content), content))
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

// TestRealRoundTrip exercises the actual pdftotext binary; it skips when poppler
// is not installed (CI installs poppler-utils to run it).
func TestRealRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext not installed; skipping real round-trip")
	}
	data := makeTextPDF("Hello World")
	arts, err := pdf.New(pdf.WithEngine(pdftotext.New())).
		Extract(context.Background(), bytes.NewReader(data), "src-1")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(arts) != 1 {
		t.Fatalf("got %d artifacts, want 1", len(arts))
	}
	a := arts[0]
	if !strings.Contains(a.Text, "Hello World") {
		t.Fatalf("text = %q, want it to contain %q", a.Text, "Hello World")
	}
	if a.Metadata[pdf.MetaPage] != "1" {
		t.Fatalf("page metadata = %q, want 1", a.Metadata[pdf.MetaPage])
	}
	if a.Metadata[pdf.MetaEngine] != "pdftotext" {
		t.Fatalf("engine metadata = %q, want pdftotext", a.Metadata[pdf.MetaEngine])
	}
	if a.MediaType != extract.MediaTypeText {
		t.Fatalf("media type = %q, want %q", a.MediaType, extract.MediaTypeText)
	}
}

// TestOutputCapEnforced: a tiny output cap against a PDF that yields more text
// kills the process and returns ErrOutputTooLarge (not a malformed-source error).
func TestOutputCapEnforced(t *testing.T) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext not installed")
	}
	eng := pdftotext.New(pdftotext.WithMaxOutputBytes(4)) // "Hello World" exceeds 4 bytes
	_, err := eng.Pages(context.Background(), makeTextPDF("Hello World"))
	if !errors.Is(err, pdftotext.ErrOutputTooLarge) {
		t.Fatalf("err = %v, want ErrOutputTooLarge", err)
	}
	if errors.Is(err, extract.ErrMalformedSource) {
		t.Fatal("output-cap error should not also be ErrMalformedSource")
	}
}

// TestMalformedInput feeds non-PDF bytes; pdftotext should fail and the engine
// should report a malformed source (skips when the binary is absent).
func TestMalformedInput(t *testing.T) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext not installed")
	}
	_, err := pdftotext.New().Pages(context.Background(), []byte("definitely not a pdf"))
	if !errors.Is(err, extract.ErrMalformedSource) {
		t.Fatalf("err = %v, want ErrMalformedSource", err)
	}
}

// rowRE matches a statement row on one line: label, a gap of 2+ spaces, value.
func rowRE(r [2]string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(r[0]) + ` {2,}` + regexp.QuoteMeta(r[1]) + `$`)
}

// TestLayoutKeepsRowsTogether runs the real binary on a statement-shaped table:
// with WithLayout each label shares a line with its value and the artifact is
// tagged pdf.ModeLayout; the default mode splits them and is tagged pdf.ModeRaw.
func TestLayoutKeepsRowsTogether(t *testing.T) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext not installed")
	}
	data := makeStatementPDF()

	arts, err := pdf.New(pdf.WithEngine(pdftotext.New(pdftotext.WithLayout()))).
		Extract(context.Background(), bytes.NewReader(data), "src-1")
	if err != nil {
		t.Fatalf("Extract (layout): %v", err)
	}
	if len(arts) != 1 {
		t.Fatalf("layout: got %d artifacts, want 1", len(arts))
	}
	for _, r := range statementRows {
		if !rowRE(r).MatchString(arts[0].Text) {
			t.Errorf("layout text lacks row %q / %q on one line:\n%s", r[0], r[1], arts[0].Text)
		}
	}
	if got := arts[0].Metadata[pdf.MetaMode]; got != pdf.ModeLayout {
		t.Errorf("layout mode metadata = %q, want %q", got, pdf.ModeLayout)
	}
	if got := arts[0].Metadata[pdf.MetaEngine]; got != "pdftotext" {
		t.Errorf("engine metadata = %q, want pdftotext", got)
	}

	raw, err := pdf.New(pdf.WithEngine(pdftotext.New())).
		Extract(context.Background(), bytes.NewReader(data), "src-1")
	if err != nil {
		t.Fatalf("Extract (raw): %v", err)
	}
	if got := raw[0].Metadata[pdf.MetaMode]; got != pdf.ModeRaw {
		t.Errorf("raw mode metadata = %q, want %q", got, pdf.ModeRaw)
	}
	// Guards the fixture: if default mode ever kept these rows together, this
	// test would no longer show what -layout adds.
	if rowRE(statementRows[0]).MatchString(raw[0].Text) {
		t.Errorf("fixture no longer splits columns in default mode:\n%s", raw[0].Text)
	}
}

// TestLayoutMultiPage checks that -layout output still splits on form feeds
// into numbered pages, with a blank page keeping its slot.
func TestLayoutMultiPage(t *testing.T) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext not installed")
	}
	data := makePDF(
		"BT /F1 24 Tf 72 720 Td (First page) Tj ET\n",
		"",
		"BT /F1 24 Tf 72 720 Td (Third page) Tj ET\n",
	)
	pages, err := pdftotext.New(pdftotext.WithLayout()).Pages(context.Background(), data)
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	if len(pages) != 3 {
		t.Fatalf("got %d pages, want 3: %+v", len(pages), pages)
	}
	want := []string{"First page", "", "Third page"}
	for i, w := range want {
		if pages[i].Number != i+1 {
			t.Errorf("page %d Number = %d", i, pages[i].Number)
		}
		if got := strings.TrimSpace(pages[i].Text); got != w {
			t.Errorf("page %d text = %q, want %q", i+1, got, w)
		}
	}
}
