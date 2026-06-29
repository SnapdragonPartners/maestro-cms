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
package pdftotext

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/SnapdragonPartners/maestro-cms/extract"
	"github.com/SnapdragonPartners/maestro-cms/extract/pdf"
)

// DefaultBinary is the pdftotext executable looked up on PATH by default.
const DefaultBinary = "pdftotext"

// DefaultTimeout bounds a single pdftotext invocation. On timeout the process is
// killed and Pages returns ErrMalformedSource.
const DefaultTimeout = 30 * time.Second

// ErrEngineUnavailable indicates the pdftotext binary was not found, so the
// engine cannot run. Consumers can detect this to fall back or surface a clear
// "install poppler-utils" message.
var ErrEngineUnavailable = errors.New("pdftotext: binary not found on PATH")

// Engine extracts PDF text via the pdftotext CLI. It is safe for concurrent use.
type Engine struct {
	binary  string
	timeout time.Duration
}

// Option configures an Engine.
type Option func(*Engine)

// WithBinary overrides the pdftotext executable name/path (default "pdftotext").
func WithBinary(path string) Option { return func(e *Engine) { e.binary = path } }

// WithTimeout overrides the per-call timeout (default DefaultTimeout).
func WithTimeout(d time.Duration) Option { return func(e *Engine) { e.timeout = d } }

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

// Pages runs pdftotext over data and splits its output into per-page text. The
// child process is bounded by the engine timeout (and the caller's ctx); a
// timeout or non-zero exit becomes ErrMalformedSource, a canceled ctx becomes a
// context error, and a missing binary becomes ErrEngineUnavailable.
func (e *Engine) Pages(ctx context.Context, data []byte) ([]pdf.Page, error) {
	bin := e.bin()
	if _, err := exec.LookPath(bin); err != nil {
		return nil, fmt.Errorf("%w: %q", ErrEngineUnavailable, bin)
	}

	cctx, cancel := context.WithTimeout(ctx, e.to())
	defer cancel()

	// pdftotext -enc UTF-8 -q - -  : PDF from stdin, text to stdout, with a
	// form-feed (\f) separating pages (we keep page breaks rather than -nopgbrk).
	// bin is operator-configured (WithBinary), the args are constant literals, and
	// the PDF data is piped via stdin — there is no injection vector.
	cmd := exec.CommandContext(cctx, bin, "-enc", "UTF-8", "-q", "-", "-") //nolint:gosec // see comment: fixed args, operator-configured binary, stdin input
	cmd.Stdin = bytes.NewReader(data)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	// Caller cancellation/deadline takes priority and is not a malformed source.
	if ctx.Err() != nil {
		return nil, fmt.Errorf("pdftotext: canceled: %w", ctx.Err())
	}
	// Our own timeout fired (parent still alive): the parse was too slow / hung.
	if cctx.Err() != nil {
		return nil, &extract.MalformedSourceError{
			MediaType: pdf.MediaType,
			Err:       fmt.Errorf("pdftotext exceeded %s", e.to()),
		}
	}
	if runErr != nil {
		return nil, &extract.MalformedSourceError{
			MediaType: pdf.MediaType,
			Err:       fmt.Errorf("pdftotext failed: %w: %s", runErr, strings.TrimSpace(stderr.String())),
		}
	}
	return splitPages(stdout.String()), nil
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
