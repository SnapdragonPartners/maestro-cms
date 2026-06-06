package docx_test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SnapdragonPartners/maestro-cms/extract"
	"github.com/SnapdragonPartners/maestro-cms/extract/docx"
)

var _ extract.Extractor = docx.Extractor{}

// makeDocx builds a minimal valid .docx archive whose word/document.xml contains
// the given paragraphs (each a <w:p> of <w:t> runs).
func makeDocx(t *testing.T, paragraphs ...string) []byte {
	t.Helper()
	var body strings.Builder
	body.WriteString(`<?xml version="1.0"?><w:document xmlns:w="x"><w:body>`)
	for _, p := range paragraphs {
		body.WriteString(`<w:p><w:r><w:t>` + p + `</w:t></w:r></w:p>`)
	}
	body.WriteString(`</w:body></w:document>`)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatalf("zip create: %v", err)
	}
	if _, err := w.Write([]byte(body.String())); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func TestExtractParagraphs(t *testing.T) {
	data := makeDocx(t, "First paragraph.", "Second paragraph.")
	arts, err := docx.New().Extract(context.Background(), bytes.NewReader(data), "src-1")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(arts) != 1 {
		t.Fatalf("got %d artifacts, want 1", len(arts))
	}
	a := arts[0]
	if a.MediaType != extract.MediaTypeText {
		t.Fatalf("MediaType = %q, want %q", a.MediaType, extract.MediaTypeText)
	}
	if a.DerivedFrom != "src-1" {
		t.Fatalf("DerivedFrom = %q, want src-1", a.DerivedFrom)
	}
	if a.ID != "" {
		t.Fatalf("ID = %q, want empty", a.ID)
	}
	// Paragraph structure must survive for the chunker.
	if !strings.Contains(a.Text, "First paragraph.\n\nSecond paragraph.") {
		t.Fatalf("paragraph break not preserved: %q", a.Text)
	}
}

func TestExtractEmptyBodyIsNoContent(t *testing.T) {
	data := makeDocx(t) // no paragraphs
	_, err := docx.New().Extract(context.Background(), bytes.NewReader(data), "s")
	if !errors.Is(err, extract.ErrNoContent) {
		t.Fatalf("Extract err = %v, want ErrNoContent", err)
	}
}

func TestExtractNotAZip(t *testing.T) {
	_, err := docx.New().Extract(context.Background(), strings.NewReader("this is not a zip"), "s")
	if !errors.Is(err, extract.ErrMalformedSource) {
		t.Fatalf("Extract err = %v, want ErrMalformedSource", err)
	}
}

func TestExtractZipWithoutDocumentPart(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("not-document.xml")
	_, _ = w.Write([]byte("<x/>"))
	_ = zw.Close()

	_, err := docx.New().Extract(context.Background(), bytes.NewReader(buf.Bytes()), "s")
	if !errors.Is(err, extract.ErrMalformedSource) {
		t.Fatalf("Extract err = %v, want ErrMalformedSource for missing document part", err)
	}
}

func TestExtractMalformedXML(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("word/document.xml")
	_, _ = w.Write([]byte("<w:document><w:body><w:p><w:t>unclosed")) // truncated
	_ = zw.Close()

	_, err := docx.New().Extract(context.Background(), bytes.NewReader(buf.Bytes()), "s")
	if !errors.Is(err, extract.ErrMalformedSource) {
		t.Fatalf("Extract err = %v, want ErrMalformedSource for bad XML", err)
	}
}

func TestExtractRejectsOversizedDecompressedPart(t *testing.T) {
	// A document.xml that decompresses past MaxDecompressedBytes must be rejected
	// as malformed (the zip-bomb guard) rather than streamed in full. The
	// Registry's compressed-byte cap does not catch this — a highly compressible
	// part can fit under it yet expand without bound.
	data := makeDocx(t, "this paragraph is comfortably larger than the tiny cap below")
	e := docx.Extractor{MaxDecompressedBytes: 8}
	_, err := e.Extract(context.Background(), bytes.NewReader(data), "s")
	if !errors.Is(err, extract.ErrMalformedSource) {
		t.Fatalf("Extract err = %v, want ErrMalformedSource for oversized decompressed part", err)
	}
}

func TestExtractHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	data := makeDocx(t, "hello")
	_, err := docx.New().Extract(ctx, bytes.NewReader(data), "s")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Extract err = %v, want context.Canceled", err)
	}
}

func TestExtractThroughRegistry(t *testing.T) {
	reg := extract.NewRegistry()
	reg.Register(docx.MediaType, docx.New())
	data := makeDocx(t, "registered content")
	arts, err := reg.Extract(context.Background(), docx.MediaType, bytes.NewReader(data), "src-1")
	if err != nil {
		t.Fatalf("registry Extract: %v", err)
	}
	if !strings.Contains(arts[0].Text, "registered content") {
		t.Fatalf("unexpected text: %q", arts[0].Text)
	}
}
