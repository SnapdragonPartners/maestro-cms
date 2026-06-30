package purego_test

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
	"github.com/SnapdragonPartners/maestro-cms/extract/pdf/purego"
)

var _ pdf.Engine = (*purego.Engine)(nil)

// minimalPDF builds a structurally-valid PDF with a single page that has no
// /Contents stream. dslipak/pdf parses it (NumPage==1) but GetPlainText hangs on
// it — the input that motivates the watchdog.
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

func extractWith(t *testing.T, eng *purego.Engine, data []byte) error {
	t.Helper()
	_, err := pdf.New(pdf.WithEngine(eng)).Extract(context.Background(), bytes.NewReader(data), "s")
	return err
}

func TestNotAPDFIsMalformed(t *testing.T) {
	if err := extractWith(t, purego.New(), []byte("this is plainly not a pdf")); !errors.Is(err, extract.ErrMalformedSource) {
		t.Fatalf("err = %v, want ErrMalformedSource", err)
	}
}

func TestEmptyInputIsMalformed(t *testing.T) {
	if err := extractWith(t, purego.New(), nil); !errors.Is(err, extract.ErrMalformedSource) {
		t.Fatalf("err = %v, want ErrMalformedSource", err)
	}
}

func TestTruncatedPDFDoesNotPanic(t *testing.T) {
	if err := extractWith(t, purego.New(), []byte("%PDF-1.4\n garbage stream without xref")); !errors.Is(err, extract.ErrMalformedSource) {
		t.Fatalf("err = %v, want ErrMalformedSource", err)
	}
}

func TestCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := pdf.New(pdf.WithEngine(purego.New())).Extract(ctx, strings.NewReader("%PDF-1.4"), "s")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// The watchdog must bound a hanging parse: a known-hang PDF with a short Timeout
// returns ErrMalformedSource quickly rather than blocking forever.
func TestHangIsBoundedByTimeout(t *testing.T) {
	eng := &purego.Engine{Timeout: 200 * time.Millisecond}
	done := make(chan error, 1)
	go func() { done <- extractWith(t, eng, minimalPDF()) }()
	select {
	case err := <-done:
		if !errors.Is(err, extract.ErrMalformedSource) {
			t.Fatalf("err = %v, want ErrMalformedSource (timeout)", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Extract did not return within 5s; watchdog failed to bound the hang")
	}
}

func TestMalformedErrorCarriesMediaType(t *testing.T) {
	err := extractWith(t, purego.New(), []byte("nope"))
	var mErr *extract.MalformedSourceError
	if !errors.As(err, &mErr) {
		t.Fatalf("err = %v, want *MalformedSourceError", err)
	}
	if mErr.MediaType != pdf.MediaType {
		t.Fatalf("MediaType = %q, want %q", mErr.MediaType, pdf.MediaType)
	}
}
