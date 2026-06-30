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

// DefaultMaxOutputBytes caps the extracted text pdftotext may produce. The
// Registry bounds the *compressed* input, but a small adversarial PDF can expand
// to far more text, so the output stream needs its own ceiling to bound the Go
// worker's heap. 64 MiB is generous for real documents while preventing OOM.
const DefaultMaxOutputBytes int64 = 64 << 20

// stderrCapBytes bounds captured diagnostics so a chatty/hostile process cannot
// grow the heap via stderr either; output past this is dropped.
const stderrCapBytes = 64 << 10

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
	binary    string
	timeout   time.Duration
	maxOutput int64
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

func (e *Engine) maxOut() int64 {
	if e.maxOutput <= 0 {
		return DefaultMaxOutputBytes
	}
	return e.maxOutput
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
	stdout := &capWriter{limit: e.maxOut(), cancel: cancel}
	stderr := &truncWriter{limit: stderrCapBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	runErr := cmd.Run()

	// Output cap is checked first: exceeding it kills the process (via cancel), so
	// the resulting run error / cctx cancellation would otherwise be misread below.
	if stdout.exceeded {
		return nil, fmt.Errorf("%w (%d bytes)", ErrOutputTooLarge, e.maxOut())
	}
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
		// A non-zero exit means pdftotext ran and rejected the input → the PDF is
		// malformed. Any other error means we could not start/execute the binary
		// (permissions, fork failure, …) → an operational error, not a bad PDF, so
		// it must not match ErrMalformedSource (callers fall back/diagnose on it).
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return nil, &extract.MalformedSourceError{
				MediaType: pdf.MediaType,
				Err:       fmt.Errorf("pdftotext exited %d: %s", exitErr.ExitCode(), strings.TrimSpace(stderr.String())),
			}
		}
		return nil, fmt.Errorf("pdftotext: exec failed: %w", runErr)
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

// capWriter buffers child stdout up to limit bytes; the first write that would
// exceed the cap kills the process (via cancel) and fails, so a PDF that expands
// to enormous text cannot grow the heap unbounded. It is written only by the
// exec copy goroutine, so it needs no locking.
type capWriter struct {
	buf      bytes.Buffer
	n        int64
	limit    int64
	cancel   context.CancelFunc
	exceeded bool
}

func (w *capWriter) Write(p []byte) (int, error) {
	if w.exceeded || w.n+int64(len(p)) > w.limit {
		w.exceeded = true
		w.cancel() // stop pdftotext from producing more output
		return 0, ErrOutputTooLarge
	}
	n, err := w.buf.Write(p)
	w.n += int64(n)
	return n, err //nolint:wrapcheck // bytes.Buffer.Write never errors; pass-through
}

func (w *capWriter) String() string { return w.buf.String() }

// truncWriter captures up to limit bytes and silently drops the rest, always
// reporting a full write so the process is never stalled by a bounded stderr.
type truncWriter struct {
	buf   bytes.Buffer
	limit int
}

func (w *truncWriter) Write(p []byte) (int, error) {
	if rem := w.limit - w.buf.Len(); rem > 0 {
		if len(p) <= rem {
			w.buf.Write(p)
		} else {
			w.buf.Write(p[:rem])
		}
	}
	return len(p), nil
}

func (w *truncWriter) String() string { return w.buf.String() }
