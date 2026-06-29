package pdftotext_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/SnapdragonPartners/maestro-cms/extract"
	"github.com/SnapdragonPartners/maestro-cms/extract/pdf"
	"github.com/SnapdragonPartners/maestro-cms/extract/pdf/pdftotext"
)

var _ pdf.Engine = (*pdftotext.Engine)(nil)

func TestEngineUnavailable(t *testing.T) {
	eng := pdftotext.New(pdftotext.WithBinary("maestro-cms-no-such-binary-xyz"))
	_, err := eng.Pages(context.Background(), []byte("%PDF-1.4"))
	if !errors.Is(err, pdftotext.ErrEngineUnavailable) {
		t.Fatalf("err = %v, want ErrEngineUnavailable", err)
	}
}

// makeTextPDF builds a minimal one-page PDF with a text content stream that
// pdftotext can extract. Offsets are computed so the xref is valid.
func makeTextPDF(text string) []byte {
	content := "BT /F1 24 Tf 72 720 Td (" + text + ") Tj ET\n"
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	var offsets []int
	obj := func(s string) { offsets = append(offsets, b.Len()); b.WriteString(s) }
	obj("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	obj("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")
	obj("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] " +
		"/Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>\nendobj\n")
	obj(fmt.Sprintf("4 0 obj\n<< /Length %d >>\nstream\n%sendstream\nendobj\n", len(content), content))
	obj("5 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>\nendobj\n")
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
