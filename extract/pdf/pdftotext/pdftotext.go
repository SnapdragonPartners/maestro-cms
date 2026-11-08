// Package pdftotext is a PDF engine that shells out to Poppler's pdftotext.
//
// It runs pdftotext as a child process (PDF on stdin, text on stdout), which is
// the recommended engine for production and untrusted input: a crafted PDF that
// hangs or crashes dies in a process the engine kills on timeout/cancellation —
// no leaked goroutine, no in-process crash. It adds no Go dependency.
//
// Licensing/operational note: Poppler is GPL. Invoking the installed pdftotext
// binary as a subprocess is a process boundary (we do not link or vendor it), so
// this Go module stays MIT — but a distributed image that BUNDLES Poppler still
// carries Poppler's GPL obligations for that artifact. The pdftotext binary must
// be present at runtime; when it is absent, Pages returns ErrEngineUnavailable.
//
// The engine also implements pdf.LayoutEngine: Layout returns each page's size
// and its words with boxes, from pdftotext -tsv and Poppler's pdfinfo (which
// ships with pdftotext in poppler-utils and must also be present).
package pdftotext

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"time"

	"github.com/SnapdragonPartners/maestro-cms/extract/pdf"
	"github.com/SnapdragonPartners/maestro-cms/extract/pdf/internal/popplerexec"
)

// DefaultBinary is the pdftotext executable looked up on PATH by default.
const DefaultBinary = "pdftotext"

// DefaultTimeout bounds a single pdftotext invocation. On timeout the process is
// killed and Pages returns ErrMalformedSource.
const DefaultTimeout = 30 * time.Second

// DefaultMaxOutputBytes caps the extracted text pdftotext may produce. The
// Registry bounds the *compressed* input, but a small adversarial PDF can expand
// to far more text, so the output stream needs its own ceiling to bound the Go
// worker's heap. 64 MiB is generous for real documents while preventing OOM.
const DefaultMaxOutputBytes int64 = 64 << 20

// ErrEngineUnavailable indicates the pdftotext binary was not found, so the
// engine cannot run. Consumers can detect this to fall back or surface a clear
// "install poppler-utils" message.
var ErrEngineUnavailable = errors.New("pdftotext: binary not found on PATH")

// ErrOutputTooLarge indicates pdftotext produced more extracted text than the
// configured output cap; the process is killed and this is returned. It is a
// resource limit, distinct from a malformed PDF.
var ErrOutputTooLarge = errors.New("pdftotext: output exceeds size limit")

// Engine extracts PDF text via the pdftotext CLI. It is safe for concurrent use.
type Engine struct {
	binary     string
	infoBinary string
	timeout    time.Duration
	maxOutput  int64
	maxLayout  int64
	layout     bool
}

// Option configures an Engine.
type Option func(*Engine)

// WithBinary overrides the pdftotext executable name/path (default "pdftotext").
func WithBinary(path string) Option { return func(e *Engine) { e.binary = path } }

// WithTimeout overrides the per-call timeout (default DefaultTimeout).
func WithTimeout(d time.Duration) Option { return func(e *Engine) { e.timeout = d } }

// WithMaxOutputBytes overrides the extracted-text output cap (default
// DefaultMaxOutputBytes). Non-positive means the default.
func WithMaxOutputBytes(n int64) Option { return func(e *Engine) { e.maxOutput = n } }

// WithPdfinfoBinary overrides the pdfinfo executable name/path used by Layout
// (default DefaultPdfinfoBinary).
func WithPdfinfoBinary(path string) Option { return func(e *Engine) { e.infoBinary = path } }

// WithMaxLayoutBytes overrides the cap on pdftotext -tsv output in Layout
// (default DefaultMaxLayoutBytes). Non-positive means the default.
func WithMaxLayoutBytes(n int64) Option { return func(e *Engine) { e.maxLayout = n } }

// WithLayout runs pdftotext with -layout, which keeps each page's physical
// layout: a table row's label and its values stay on one line, separated by
// spaces, where the default mode can emit the label column and the value column
// as separate blocks. Page splitting, timeout and output cap are unchanged. The
// engine then reports pdf.ModeLayout (recorded as pdf.MetaMode); without this
// option it reports pdf.ModeRaw.
//
// Column spacing is in Page.Text. pdf.Extractor normalizes whitespace, which
// trims each line's leading and trailing spaces but keeps the gaps inside a
// line; call Pages directly for the raw spacing. Character offsets in layout
// text are not a reliable way to infer columns.
func WithLayout() Option { return func(e *Engine) { e.layout = true } }

// New returns a pdftotext Engine.
func New(opts ...Option) *Engine {
	e := &Engine{}
	for _, o := range opts {
		o(e)
	}
	return e
}

// Name reports the engine identifier recorded on artifacts.
func (e *Engine) Name() string { return "pdftotext" }

// Mode reports the text mode (pdf.ModeLayout with WithLayout, else pdf.ModeRaw).
// It implements pdf.ModeReporter.
func (e *Engine) Mode() string {
	if e.layout {
		return pdf.ModeLayout
	}
	return pdf.ModeRaw
}

// args returns pdftotext's arguments: PDF from stdin, text to stdout, with a
// form feed (\f) separating pages (page breaks are kept, not -nopgbrk).
func (e *Engine) args() []string {
	if e.layout {
		return []string{"-layout", "-enc", "UTF-8", "-q", "-", "-"}
	}
	return []string{"-enc", "UTF-8", "-q", "-", "-"}
}

func (e *Engine) bin() string {
	if e.binary != "" {
		return e.binary
	}
	return DefaultBinary
}

func (e *Engine) to() time.Duration {
	if e.timeout <= 0 {
		return DefaultTimeout
	}
	return e.timeout
}

func (e *Engine) maxOut() int64 {
	if e.maxOutput <= 0 {
		return DefaultMaxOutputBytes
	}
	return e.maxOutput
}

// spec returns the shared subprocess settings for one call on data.
func (e *Engine) spec(binary string, data []byte) popplerexec.Spec {
	return popplerexec.Spec{
		Name:           "pdftotext",
		Binary:         binary,
		Stdin:          data,
		Timeout:        e.to(),
		ErrUnavailable: ErrEngineUnavailable,
		ErrTooLarge:    ErrOutputTooLarge,
	}
}

// Pages runs pdftotext over data and splits its output into per-page text. The
// child process is bounded by the engine timeout (and the caller's ctx); a
// timeout or non-zero exit becomes ErrMalformedSource (a crash also matches
// pdf.ErrEngineCrashed), a canceled ctx becomes a context error, and a missing
// binary becomes ErrEngineUnavailable.
func (e *Engine) Pages(ctx context.Context, data []byte) ([]pdf.Page, error) {
	s := e.spec(e.bin(), data)
	s.Args = e.args()
	s.MaxOutput = e.maxOut()
	var out bytes.Buffer
	if err := popplerexec.Run(ctx, s, &out); err != nil {
		return nil, err //nolint:wrapcheck // popplerexec returns this package's sentinels and malformed/ctx errors
	}
	return splitPages(out.String()), nil
}

// splitPages turns pdftotext's form-feed-separated output into per-page text.
// pdftotext writes a form feed after each page, so a trailing empty block from
// the final separator is dropped. Page numbers are 1-based and positional, so a
// blank page keeps its slot (its empty Text is dropped downstream).
func splitPages(out string) []pdf.Page {
	if out == "" {
		return nil
	}
	blocks := strings.Split(out, "\f")
	if n := len(blocks); n > 0 && blocks[n-1] == "" {
		blocks = blocks[:n-1]
	}
	pages := make([]pdf.Page, 0, len(blocks))
	for i, blk := range blocks {
		pages = append(pages, pdf.Page{Number: i + 1, Text: blk})
	}
	return pages
}
