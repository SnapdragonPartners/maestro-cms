package pdftotext

import (
	"context"
	"fmt"
	"io"

	"github.com/SnapdragonPartners/maestro-cms/extract"
	"github.com/SnapdragonPartners/maestro-cms/extract/pdf"
	"github.com/SnapdragonPartners/maestro-cms/extract/pdf/internal/geometry"
	"github.com/SnapdragonPartners/maestro-cms/extract/pdf/internal/popplerexec"
)

// DefaultPdfinfoBinary is the pdfinfo executable Layout looks up on PATH by
// default. It ships in poppler-utils with pdftotext.
const DefaultPdfinfoBinary = geometry.DefaultBinary

// DefaultMaxLayoutBytes caps the pdftotext -tsv output Layout may read. TSV is
// about 13x the plain text, so it has its own cap rather than reusing
// DefaultMaxOutputBytes. The TSV is parsed as it streams and never held raw,
// the returned Layout takes more memory than the TSV it came from (about 1.4x,
// measured on a 342-page OCR'd scan), so this cap also bounds one call's heap:
// roughly 180 MiB at the default.
const DefaultMaxLayoutBytes int64 = 128 << 20

var _ pdf.LayoutEngine = (*Engine)(nil)

func (e *Engine) infoBin() string {
	if e.infoBinary != "" {
		return e.infoBinary
	}
	return DefaultPdfinfoBinary
}

func (e *Engine) maxLayoutOut() int64 {
	if e.maxLayout <= 0 {
		return DefaultMaxLayoutBytes
	}
	return e.maxLayout
}

// tsvArgs: words with boxes relative to the crop box, PDF on stdin, TSV on
// stdout. The mode set by WithLayout doesn't apply; TSV has one form.
func tsvArgs() []string { return []string{"-tsv", "-cropbox", "-enc", "UTF-8", "-q", "-", "-"} }

// Layout reports every page's size and its words with boxes. It implements
// pdf.LayoutEngine. It runs two subprocesses, each under the engine timeout and
// the caller's ctx:
//
//   - pdfinfo -box, the only source of page count, size and rotation (TSV's page
//     sizes are not rotated);
//   - pdftotext -tsv -cropbox, whose word boxes are already in displayed
//     coordinates, parsed as it streams under WithMaxLayoutBytes.
//
// Errors match Pages: ErrEngineUnavailable when either binary is missing (the
// message names it), ErrOutputTooLarge past the cap, a context error on
// cancellation, and extract.ErrMalformedSource for a rejected PDF, a crash
// (also pdf.ErrEngineCrashed), a timeout, or output that doesn't parse or
// disagrees on the page count.
func (e *Engine) Layout(ctx context.Context, data []byte) (*pdf.Layout, error) {
	info, err := geometry.Probe(ctx, e.spec(e.infoBin(), data), 1, geometry.LastPageAll)
	if err != nil {
		return nil, err //nolint:wrapcheck // classified sentinel/malformed/ctx errors
	}
	if info.Total >= geometry.LastPageAll {
		return nil, malformed(fmt.Errorf("document has %d pages; layout supports fewer than %d", info.Total, geometry.LastPageAll))
	}

	s := e.spec(e.bin(), data)
	s.Args = tsvArgs()
	s.MaxOutput = e.maxLayoutOut()
	var pages []tsvPage
	err = popplerexec.Stream(ctx, s, func(r io.Reader) error {
		var perr error
		pages, perr = parseTSV(r)
		if perr != nil {
			return malformed(perr)
		}
		return nil
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // classified sentinel/malformed/ctx errors
	}
	if len(pages) != info.Total {
		return nil, malformed(fmt.Errorf("pdftotext reported %d pages, pdfinfo %d", len(pages), info.Total))
	}

	out := &pdf.Layout{
		Version: pdf.LayoutVersion,
		Engine:  e.Name(),
		Box:     pdf.BoxCrop,
		Pages:   make([]pdf.PageLayout, len(pages)),
	}
	for i := range info.Pages {
		g := &info.Pages[i]
		out.Pages[i] = pdf.PageLayout{
			Number:   g.Number,
			Width:    g.Width,
			Height:   g.Height,
			Rotation: g.Rotation,
			Words:    pages[i].words,
		}
	}
	return out, nil
}

func malformed(err error) error {
	return &extract.MalformedSourceError{MediaType: pdf.MediaType, Err: err}
}
