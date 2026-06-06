package pdf_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/SnapdragonPartners/maestro-cms/extract"
	"github.com/SnapdragonPartners/maestro-cms/extract/pdf"
)

var _ extract.Extractor = pdf.Extractor{}

// minimalPDF builds a structurally-valid PDF with a single page that has no
// /Contents stream. dslipak/pdf parses it (NumPage==1) but GetPlainText hangs on
// it — this is the input that motivated the watchdog (see the package doc).
func minimalPDF() []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	var offsets []int
	obj := func(s string) { offsets = append(offsets, b.Len()); b.WriteString(s) }
	obj("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	obj("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")
	obj("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >>\nendobj\n")
	xrefPos := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n", len(offsets)+1)
	b.WriteString("0000000000 65535 f \n")
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xrefPos)
	return b.Bytes()
}

// Synthesizing a valid text-bearing PDF in a test is impractical, and the
// dslipak/pdf parser is itself untested upstream — so these tests focus on the
// error and edge paths, which is where the risk of an untested parser actually
// lives (the happy path will be validated against a real corpus separately).

func TestExtractNotAPDF(t *testing.T) {
	_, err := pdf.New().Extract(context.Background(), strings.NewReader("this is plainly not a pdf"), "s")
	if !errors.Is(err, extract.ErrMalformedSource) {
		t.Fatalf("Extract err = %v, want ErrMalformedSource", err)
	}
}

func TestExtractEmptyInputIsMalformed(t *testing.T) {
	// An empty reader is not a valid PDF (no header/xref), so it is malformed
	// rather than ErrNoContent (which is reserved for a clean parse with no text).
	_, err := pdf.New().Extract(context.Background(), bytes.NewReader(nil), "s")
	if !errors.Is(err, extract.ErrMalformedSource) {
		t.Fatalf("Extract err = %v, want ErrMalformedSource for empty input", err)
	}
}

func TestExtractTruncatedPDFDoesNotPanic(t *testing.T) {
	// A truncated "%PDF" header exercises the parser on malformed input. Whatever
	// the parser does (error or panic), the recover boundary must turn it into a
	// MalformedSourceError rather than crash the caller.
	_, err := pdf.New().Extract(context.Background(), strings.NewReader("%PDF-1.4\n garbage stream without xref"), "s")
	if !errors.Is(err, extract.ErrMalformedSource) {
		t.Fatalf("Extract err = %v, want ErrMalformedSource for truncated PDF", err)
	}
}

func TestExtractHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := pdf.New().Extract(ctx, strings.NewReader("%PDF-1.4"), "s")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Extract err = %v, want context.Canceled", err)
	}
}

// The watchdog must bound a hanging parse: a known-hang PDF with a short Timeout
// returns ErrMalformedSource quickly rather than blocking forever. This is the
// core stopgap behavior (recover() cannot catch the hang).
func TestExtractHangIsBoundedByTimeout(t *testing.T) {
	e := pdf.Extractor{Timeout: 200 * time.Millisecond}
	done := make(chan error, 1)
	go func() {
		_, err := e.Extract(context.Background(), bytes.NewReader(minimalPDF()), "s")
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, extract.ErrMalformedSource) {
			t.Fatalf("Extract err = %v, want ErrMalformedSource (timeout)", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Extract did not return within 5s; watchdog failed to bound the hang")
	}
}

func TestMalformedErrorCarriesMediaType(t *testing.T) {
	_, err := pdf.New().Extract(context.Background(), strings.NewReader("nope"), "s")
	var mErr *extract.MalformedSourceError
	if !errors.As(err, &mErr) {
		t.Fatalf("Extract err = %v, want *MalformedSourceError", err)
	}
	if mErr.MediaType != pdf.MediaType {
		t.Fatalf("MalformedSourceError.MediaType = %q, want %q", mErr.MediaType, pdf.MediaType)
	}
}
