// Package purego is a pure-Go PDF engine (github.com/dslipak/pdf) for SMALL,
// TRUSTED input only.
//
// It adds no system dependency, which makes it convenient for tests and trusted
// documents — but it is NOT safe for untrusted/high-volume ingestion. The
// dslipak/pdf parser can HANG (not just panic) on some inputs (e.g. a page with
// no content stream), and a hang cannot be caught by recover() or interrupted by
// context. As a stopgap, Pages runs the parse in a goroutine bounded by a
// wall-clock Timeout and returns extract.ErrMalformedSource if it elapses — but a
// timed-out parse LEAKS its goroutine, so repeated hostile input grows memory.
//
// For production or untrusted PDFs use extract/pdf/pdftotext (out-of-process),
// which kills a hung/crashed parse with no leak. See
// docs/adr/0010-pluggable-pdf-engines.md.
package purego

import (
	"bytes"
	"context"
	"fmt"
	"time"

	dslipak "github.com/dslipak/pdf"

	"github.com/SnapdragonPartners/maestro-cms/extract"
	"github.com/SnapdragonPartners/maestro-cms/extract/pdf"
)

// DefaultTimeout bounds a single Pages call's parsing time, guarding against the
// dslipak/pdf hang.
const DefaultTimeout = 30 * time.Second

// Engine is the pure-Go PDF engine. It is safe for concurrent use.
type Engine struct {
	// Timeout bounds wall-clock parsing time before Pages gives up with
	// extract.ErrMalformedSource. Zero or negative means DefaultTimeout.
	Timeout time.Duration
}

// New returns a pure-Go Engine using DefaultTimeout.
func New() *Engine { return &Engine{} }

// Name reports the engine identifier recorded on artifacts.
func (e *Engine) Name() string { return "purego" }

func (e *Engine) timeout() time.Duration {
	if e.Timeout <= 0 {
		return DefaultTimeout
	}
	return e.Timeout
}

// Pages extracts per-page text. It runs the (uninterruptible) parser in a
// goroutine bounded by the timeout; on timeout it returns ErrMalformedSource and
// abandons (leaks) the worker, which the parser offers no way to stop.
func (e *Engine) Pages(ctx context.Context, data []byte) ([]pdf.Page, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("purego: canceled: %w", err)
	}

	type result struct {
		pages []pdf.Page
		err   error
	}
	ch := make(chan result, 1) // buffered so a leaked goroutine can still exit
	go func() {
		pages, err := extractPages(ctx, data)
		ch <- result{pages: pages, err: err}
	}()

	timer := time.NewTimer(e.timeout())
	defer timer.Stop()

	select {
	case res := <-ch:
		return res.pages, res.err
	case <-ctx.Done():
		return nil, fmt.Errorf("purego: canceled: %w", ctx.Err())
	case <-timer.C:
		return nil, &extract.MalformedSourceError{
			MediaType: pdf.MediaType,
			Err:       fmt.Errorf("purego pdf extraction exceeded %s (parser hang)", e.timeout()),
		}
	}
}

// extractPages pulls per-page plain text. A recover boundary converts a parser
// panic into ErrMalformedSource (hangs are handled by Pages's watchdog, since
// recover cannot catch them). An unreadable page is skipped rather than failing
// the whole document.
func extractPages(ctx context.Context, data []byte) (pages []pdf.Page, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			pages = nil
			err = &extract.MalformedSourceError{
				MediaType: pdf.MediaType,
				Err:       fmt.Errorf("pdf parser panic: %v", rec),
			}
		}
	}()

	reader, rerr := dslipak.NewReader(bytes.NewReader(data), int64(len(data)))
	if rerr != nil {
		return nil, &extract.MalformedSourceError{MediaType: pdf.MediaType, Err: rerr}
	}

	n := reader.NumPage()
	for i := 1; i <= n; i++ {
		if cerr := ctx.Err(); cerr != nil {
			return nil, fmt.Errorf("purego: page loop canceled: %w", cerr)
		}
		page := reader.Page(i)
		if page.V.IsNull() {
			continue
		}
		text, perr := page.GetPlainText(nil)
		if perr != nil {
			continue
		}
		if text != "" {
			pages = append(pages, pdf.Page{Number: i, Text: text})
		}
	}
	return pages, nil
}
