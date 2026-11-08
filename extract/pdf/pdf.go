// Package pdf extracts text from PDF sources through a pluggable engine.
//
// PDF parsing is hard to do safely — pure-Go parsers are immature (they can hang
// on adversarial input), and the robust C/C++ engines carry CGO and copyleft
// licensing. So this package defines an Engine interface and an Extractor that
// delegates to one; the engines live in subpackages so importing extract/pdf
// pulls no parser dependency:
//
//   - extract/pdf/pdftotext — out-of-process Poppler (pdftotext) — recommended
//     for production/untrusted input: a crafted PDF that hangs or crashes dies in
//     a child process the engine kills, with no in-process blast radius.
//   - extract/pdf/purego — pure-Go (dslipak/pdf) — a no-system-dependency
//     fallback for SMALL, TRUSTED input only; it can hang on malformed PDFs.
//
// There is deliberately **no default engine**: an unconfigured Extractor returns
// ErrNoEngine rather than silently choosing a parser known to hang. Wire one in:
//
//	reg.Register(pdf.MediaType, pdf.New(pdf.WithEngine(pdftotext.New())))
//
// Output is one text/plain artifact PER PAGE (page provenance matters for
// retrieval), each tagged with its page number and the engine name in Metadata,
// plus the text mode for engines that report one (ModeReporter).
// OCR is out of scope: an image-only page yields no text and is omitted; a PDF
// with no extractable text yields extract.ErrNoContent. See
// docs/adr/0010-pluggable-pdf-engines.md (which supersedes ADR 0007).
//
// Word positions: engines that can report them implement the optional
// LayoutEngine (pdftotext does), which returns every page's size and its words
// with boxes. AssemblePage then builds a page's text from those words, either
// in the engine's reading order or by geometric rows (which keeps a table
// row's label and values on one line), with each word's byte span in the text.
// See docs/adr/0011-pdf-word-positions-and-layout-assembly.md.
package pdf

import (
	"context"
	"errors"
	"io"
	"strconv"

	"github.com/SnapdragonPartners/maestro-cms/content"
	"github.com/SnapdragonPartners/maestro-cms/extract"
)

// MediaType is the media type this extractor handles.
const MediaType content.MediaType = "application/pdf"

// Metadata keys set on each emitted artifact.
const (
	// MetaPage is the 1-based page number the artifact's text came from.
	MetaPage = "pdf.page"
	// MetaEngine is the Name() of the engine that produced the text.
	MetaEngine = "pdf.engine"
	// MetaMode is the text mode the engine ran in, normally ModeLayout or
	// ModeRaw; an engine may report another value, so treat it as an open set.
	// It is set only when the engine implements ModeReporter and reports a
	// non-empty mode.
	MetaMode = "pdf.mode"
)

// Values of MetaMode.
const (
	// ModeLayout means the engine kept the page's physical layout, so a table
	// row's label and values stay on one line (pdftotext -layout).
	ModeLayout = "layout"
	// ModeRaw means the engine emitted text in its default flow order, which can
	// split a table's label column from its value column.
	ModeRaw = "raw"
)

// ErrNoEngine is returned by Extract when the Extractor has no engine configured.
// PDF extraction has no safe zero-config default, so a parser must be chosen
// explicitly via WithEngine.
var ErrNoEngine = errors.New("pdf: no engine configured (use pdf.WithEngine)")

// ErrEngineCrashed indicates an out-of-process engine died from a signal (for
// example SIGABRT or SIGSEGV) instead of exiting. The returned error also
// matches extract.ErrMalformedSource: a crash on bad input and a crash on an
// engine bug can't be told apart reliably, so it fails closed as malformed, and
// callers that only check ErrMalformedSource behave as before. Check this
// sentinel to tell operators it was a crash; the message names the signal.
var ErrEngineCrashed = errors.New("pdf: engine crashed")

// Page is one page's extracted text.
type Page struct {
	// Number is the 1-based page number in the source PDF.
	Number int
	// Text is the page's extracted text (may be empty for blank/image-only pages).
	Text string
}

// Engine extracts text per page from PDF bytes. Implementations live in
// subpackages so extract/pdf itself stays dependency-free.
//
// Pages must return an error matching extract.ErrMalformedSource for input it
// cannot parse, and a context error if ctx is canceled. Blank/image-only pages
// may be returned with empty Text or omitted; the Extractor drops empty pages.
type Engine interface {
	// Name identifies the engine; it is recorded on each artifact's metadata.
	Name() string
	// Pages extracts text per page from the (already fully buffered) PDF bytes.
	Pages(ctx context.Context, data []byte) ([]Page, error)
}

// ModeReporter is an optional interface for engines that can run in more than
// one text mode. When the configured engine implements it, the Extractor records
// Mode() under MetaMode on every artifact, so stored text says which mode
// produced it. Engines without it, or whose Mode() returns "", get no MetaMode
// key; any other value is stored as given. It is separate from Engine so
// existing engines need no change.
type ModeReporter interface {
	// Mode reports the engine's text mode, normally ModeLayout or ModeRaw.
	Mode() string
}

// Extractor extracts text from application/pdf sources via its configured Engine.
// The zero value has no engine and returns ErrNoEngine; use New(WithEngine(...)).
// It is safe for concurrent use if its engine is.
type Extractor struct {
	engine Engine
}

// Option configures an Extractor.
type Option func(*Extractor)

// WithEngine sets the PDF parsing engine. It is required — there is no default.
func WithEngine(e Engine) Option {
	return func(x *Extractor) { x.engine = e }
}

// New returns a PDF Extractor. Pass WithEngine to choose a parser; without one,
// Extract returns ErrNoEngine.
func New(opts ...Option) *Extractor {
	x := &Extractor{}
	for _, o := range opts {
		o(x)
	}
	return x
}

// Extract reads r as a PDF and returns one text/plain artifact per non-empty
// page, derived from parentID, each tagged with MetaPage and MetaEngine (and
// MetaMode when the engine implements ModeReporter). It
// honors ctx cancellation while reading. It returns:
//
//   - ErrNoEngine if no engine is configured;
//   - extract.ErrNoContent if the PDF parses but yields no text on any page (e.g.
//     an image-only/scanned PDF needing OCR);
//   - an error matching extract.ErrMalformedSource if the engine cannot parse it.
func (e Extractor) Extract(ctx context.Context, r io.Reader, parentID string) ([]content.Artifact, error) {
	if e.engine == nil {
		return nil, ErrNoEngine
	}
	data, err := extract.ReadAll(ctx, r)
	if err != nil {
		return nil, err
	}
	pages, err := e.engine.Pages(ctx, data)
	if err != nil {
		return nil, err //nolint:wrapcheck // engine returns package-appropriate errors (malformed/ctx)
	}

	name := e.engine.Name()
	mode := ""
	if mr, ok := e.engine.(ModeReporter); ok {
		mode = mr.Mode()
	}
	arts := make([]content.Artifact, 0, len(pages))
	for i := range pages {
		text := extract.NormalizeWhitespace(pages[i].Text)
		if text == "" {
			continue // drop blank / image-only pages rather than emit empty artifacts
		}
		meta := map[string]string{
			MetaPage:   strconv.Itoa(pages[i].Number),
			MetaEngine: name,
		}
		if mode != "" {
			meta[MetaMode] = mode
		}
		arts = append(arts, content.Artifact{
			MediaType:   extract.MediaTypeText,
			DerivedFrom: parentID,
			Text:        text,
			Metadata:    meta,
		})
	}
	if len(arts) == 0 {
		return nil, extract.ErrNoContent
	}
	return arts, nil
}
