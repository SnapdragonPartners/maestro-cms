// Package pdf extracts text from PDF sources.
//
// It is an opt-in subpackage so the core extract package stays standard-library
// only: importing this package pulls in github.com/dslipak/pdf (pure Go, no
// CGO) (see docs/adr/0006-optional-adapters-as-subpackages.md). Wire it into a
// registry with:
//
//	reg.Register("application/pdf", pdf.New())
//
// Extraction is text-stream based: it reads the embedded text of each page and
// joins pages with a blank line so the downstream boundary-aware chunker sees
// page boundaries. It does NOT perform OCR — an image-only (scanned) PDF yields
// no text and returns extract.ErrNoContent. The result is a single text/plain
// artifact in the same shape as the core text extractor.
//
// # Safety stopgap (read before depending on this)
//
// The dslipak/pdf parser can HANG (not just panic) on some inputs — e.g. a page
// with no content stream makes GetPlainText spin without returning. A hang
// cannot be caught by recover() and ignores context cancellation, which would be
// a denial-of-service vector for a library taking untrusted PDFs. As a stopgap,
// Extract runs the parse in a goroutine bounded by a wall-clock Timeout and
// returns extract.ErrMalformedSource if it elapses.
//
// This is a band-aid, not a fix: a timed-out parse LEAKS its goroutine (it is
// still stuck inside the uninterruptible parser), so repeated hostile input
// grows memory. A spike is planned to diagnose dslipak/pdf or replace it; see
// docs/adr/0007-pdf-extraction-watchdog-stopgap.md. Until then, do not point this
// at high-volume untrusted input without external process isolation.
//
// Output quality (column ordering, table flattening, font-encoding fidelity) is
// also unvalidated against a real corpus.
package pdf

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/dslipak/pdf"

	"github.com/SnapdragonPartners/maestro-cms/content"
	"github.com/SnapdragonPartners/maestro-cms/extract"
)

// MediaType is the media type this extractor handles.
const MediaType content.MediaType = "application/pdf"

// DefaultTimeout bounds a single Extract call's parsing time, guarding against
// the dslipak/pdf hang described in the package doc.
const DefaultTimeout = 30 * time.Second

// Extractor extracts text from application/pdf sources. It is safe for
// concurrent use.
type Extractor struct {
	// Timeout bounds the wall-clock time spent parsing one source before Extract
	// gives up with extract.ErrMalformedSource. Zero or negative means
	// DefaultTimeout (a negative timer would otherwise fire immediately and fail
	// every parse).
	Timeout time.Duration
}

// New returns a PDF Extractor using DefaultTimeout.
func New() *Extractor {
	return &Extractor{}
}

func (e Extractor) timeout() time.Duration {
	if e.Timeout <= 0 {
		return DefaultTimeout
	}
	return e.Timeout
}

// Extract reads r as a PDF and returns a single text/plain artifact derived from
// parentID, joining page text with blank lines. It honors ctx cancellation while
// reading. It returns:
//
//   - extract.ErrNoContent if the PDF parses cleanly but yields no text (e.g. an
//     image-only/scanned PDF that would need OCR);
//   - an error matching extract.ErrMalformedSource if the PDF cannot be parsed,
//     the parser panics, or parsing exceeds the Timeout (a hang).
func (e Extractor) Extract(ctx context.Context, r io.Reader, parentID string) ([]content.Artifact, error) {
	data, err := extract.ReadAll(ctx, r)
	if err != nil {
		return nil, err
	}

	text, err := e.runBounded(ctx, data)
	if err != nil {
		return nil, err
	}

	text = extract.NormalizeWhitespace(text)
	if text == "" {
		// Clean parse, no text. We treat this as ErrNoContent (the common
		// image-only/scanned-PDF case that wants OCR) rather than
		// ErrMalformedSource. A subtly-corrupt PDF that parses but recovers no
		// text is indistinguishable here and also reports ErrNoContent; only a
		// hard parse failure or panic is classified as malformed. Revisit if a
		// real corpus shows this conflation causes trouble.
		return nil, extract.ErrNoContent
	}
	return []content.Artifact{extract.TextArtifact(parentID, text)}, nil
}

// runBounded runs extractText in a goroutine and enforces the wall-clock
// Timeout, because the parser can hang in a way that recover() and context
// cancellation cannot interrupt (see the package doc). On timeout it returns
// ErrMalformedSource; the worker goroutine is abandoned (leaked) since the parser
// offers no way to stop it. The result channel is buffered so a leaked goroutine
// can still finish its send and exit if it ever unblocks.
func (e Extractor) runBounded(ctx context.Context, data []byte) (string, error) {
	// If the caller already canceled, don't even launch the parser: it is
	// uninterruptible, so starting it on a hang input would leak a goroutine for
	// a result no one is waiting for.
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("extract: pdf extraction canceled: %w", err)
	}

	type result struct {
		text string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		text, err := extractText(ctx, data)
		ch <- result{text: text, err: err}
	}()

	timer := time.NewTimer(e.timeout())
	defer timer.Stop()

	select {
	case res := <-ch:
		return res.text, res.err
	case <-ctx.Done():
		return "", fmt.Errorf("extract: pdf extraction canceled: %w", ctx.Err())
	case <-timer.C:
		return "", &extract.MalformedSourceError{
			MediaType: MediaType,
			Err:       fmt.Errorf("pdf extraction exceeded %s (parser hang)", e.timeout()),
		}
	}
}

// extractText pulls per-page plain text from the PDF bytes, joining pages with a
// blank line. dslipak/pdf has been observed to panic on certain malformed inputs
// (truncated content streams, broken xref tables); a recover boundary converts
// any panic into a MalformedSourceError. (Hangs are handled separately by
// runBounded, since recover cannot catch them.)
func extractText(ctx context.Context, data []byte) (text string, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = &extract.MalformedSourceError{
				MediaType: MediaType,
				Err:       fmt.Errorf("pdf parser panic: %v", rec),
			}
		}
	}()

	reader, rerr := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if rerr != nil {
		return "", &extract.MalformedSourceError{MediaType: MediaType, Err: rerr}
	}

	var b strings.Builder
	pages := reader.NumPage()
	for n := 1; n <= pages; n++ {
		if cerr := ctx.Err(); cerr != nil {
			return "", fmt.Errorf("extract: pdf page loop: %w", cerr)
		}
		page := reader.Page(n)
		if page.V.IsNull() {
			continue
		}
		// A single unreadable page should not fail the whole document; skip it.
		// Synthetic placeholders are deliberately not emitted (they would leak
		// into embeddings as if they were source content). If every page fails,
		// the empty result becomes ErrNoContent in Extract.
		pageText, perr := page.GetPlainText(nil)
		if perr != nil {
			continue
		}
		if pageText != "" {
			b.WriteString(pageText)
			b.WriteString("\n\n")
		}
	}
	return b.String(), nil
}
