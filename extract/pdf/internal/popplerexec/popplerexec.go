// Package popplerexec runs one Poppler command-line tool as a child process
// with the safeguards every PDF subprocess in this module shares: binary lookup,
// a per-call timeout, kill on cancellation, a capped stdout and a truncated
// stderr, and one classification of the outcome.
//
// It is internal so the engines (pdftotext today, pdftoppm later) share one copy
// and their safeguards cannot drift apart. Each engine keeps its own exported
// sentinel errors by passing them in Spec.
package popplerexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/SnapdragonPartners/maestro-cms/extract"
	"github.com/SnapdragonPartners/maestro-cms/extract/pdf"
)

// StderrCapBytes bounds captured diagnostics so a chatty or hostile process
// cannot grow the heap via stderr; output past it is dropped.
const StderrCapBytes = 64 << 10

// Spec describes one invocation.
type Spec struct {
	// Name labels the tool in error messages (e.g. "pdftotext").
	Name string
	// Binary is the executable name or path, looked up on PATH.
	Binary string
	// Args are the tool's arguments. Callers pass fixed flags only; the PDF goes
	// to stdin.
	Args []string
	// Stdin is the PDF bytes piped to the child.
	Stdin []byte
	// Timeout bounds the child; it is killed when it fires.
	Timeout time.Duration
	// MaxOutput caps the bytes the child may write to stdout. Crossing it kills
	// the child and the call returns ErrTooLarge.
	MaxOutput int64
	// ErrUnavailable is matched (errors.Is) by the error returned when Binary
	// is not found; the message names Name and Binary.
	ErrUnavailable error
	// ErrTooLarge is matched by the error returned when stdout crosses
	// MaxOutput; the message names Name and the limit.
	ErrTooLarge error
}

// errAborted is returned by run when a Stream consumer failed and the child was
// killed because of it; Stream replaces it with the consumer's error.
var errAborted = errors.New("popplerexec: consumer aborted")

// Run runs the child and copies its stdout into sink, through the output cap.
// The result is classified in this order: output cap (ErrTooLarge), caller
// cancellation (a context error), own timeout (malformed source), (for Stream)
// a consumer error; then, for a run that failed, failure to start (an
// operational error), death by a signal (pdf.ErrEngineCrashed, which also
// matches extract.ErrMalformedSource) or a non-zero exit (malformed source),
// which are mutually exclusive.
// A missing binary returns ErrUnavailable before anything runs.
func Run(ctx context.Context, s Spec, sink io.Writer) error {
	return run(ctx, ctx, s, sink, nil)
}

// Stream runs the child and hands its stdout to consume as it arrives, so the
// raw output is never held in memory. consume runs in its own goroutine and
// must read until EOF or return an error. If it returns an error, the child is
// killed (so it cannot block on a full pipe) and Stream returns that error,
// unless the output cap, caller cancellation or the timeout came first; those
// keep Run's priority. Stream waits for both the child and consume to finish.
func Stream(ctx context.Context, s Spec, consume func(io.Reader) error) error {
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()

	pr, pw := io.Pipe()
	var (
		aborted    atomic.Bool
		consumeErr error
		done       = make(chan struct{})
	)
	go func() {
		defer close(done)
		if err := consume(pr); err != nil {
			consumeErr = err
			aborted.Store(true)
			pr.CloseWithError(err) // later writes fail instead of blocking
			cancel()               // kill the child
			return
		}
		// consume returned early without error: keep draining so the child is
		// never blocked on a full pipe.
		_, _ = io.Copy(io.Discard, pr)
	}()

	runErr := run(ctx, sctx, s, pw, &aborted)
	_ = pw.Close() // EOF for consume; nothing more will be written
	<-done

	switch {
	case errors.Is(runErr, errAborted):
		return consumeErr
	case runErr != nil:
		return runErr
	default:
		return consumeErr // a failure on the final, complete output
	}
}

// run executes the child under runCtx (which Stream can cancel on its own),
// classifying caller cancellation by callerCtx. aborted, when non-nil, reports
// that a Stream consumer killed the child.
func run(callerCtx, runCtx context.Context, s Spec, sink io.Writer, aborted *atomic.Bool) error {
	if _, err := exec.LookPath(s.Binary); err != nil {
		return &labeled{msg: fmt.Sprintf("%s: binary %q not found on PATH", s.Name, s.Binary), sentinel: s.ErrUnavailable}
	}

	cctx, cancel := context.WithTimeout(runCtx, s.Timeout)
	defer cancel()

	// Binary is operator-configured, Args are fixed flags chosen by the engine,
	// and the PDF is piped via stdin, so there is no injection vector.
	cmd := exec.CommandContext(cctx, s.Binary, s.Args...) //nolint:gosec // see comment: fixed args, operator-configured binary, stdin input
	cmd.Stdin = bytes.NewReader(s.Stdin)
	stdout := &capWriter{sink: sink, limit: s.MaxOutput, cancel: cancel, errTooLarge: s.ErrTooLarge}
	stderr := &truncWriter{limit: StderrCapBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	runErr := cmd.Run()

	// Exceeding the cap kills the child (via cancel), so the cap is checked first
	// or the resulting kill would be misread below.
	if stdout.exceeded {
		return &labeled{msg: fmt.Sprintf("%s: output exceeds size limit (%d bytes)", s.Name, s.MaxOutput), sentinel: s.ErrTooLarge}
	}
	// Caller cancellation/deadline takes priority and is not a malformed source.
	if callerCtx.Err() != nil {
		return fmt.Errorf("%s: canceled: %w", s.Name, callerCtx.Err())
	}
	// Our own timeout fired (caller still alive): the parse was too slow or hung.
	// It outranks a consumer abort when the two race: a hung or slow engine is
	// the more useful diagnosis. A consumer abort cancels runCtx, which leaves
	// cctx Canceled, not DeadlineExceeded, so the abort alone never looks like
	// a timeout.
	if errors.Is(cctx.Err(), context.DeadlineExceeded) {
		return &extract.MalformedSourceError{
			MediaType: pdf.MediaType,
			Err:       fmt.Errorf("%s exceeded %s", s.Name, s.Timeout),
		}
	}
	if aborted != nil && aborted.Load() {
		return errAborted
	}
	if runErr == nil {
		return nil
	}
	// A child that ran and failed means the input was rejected (or crashed the
	// tool) → malformed. Any other error means we could not start/execute the
	// binary (permissions, fork failure, …) → an operational error, which must
	// not match ErrMalformedSource (callers fall back/diagnose on it).
	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) {
		return fmt.Errorf("%s: exec failed: %w", s.Name, runErr)
	}
	diag := strings.TrimSpace(stderr.String())
	if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		// Crashes on bad input and on a Poppler bug can't be told apart, so this
		// fails closed as malformed, but names the signal for operators.
		return &extract.MalformedSourceError{
			MediaType: pdf.MediaType,
			Err:       fmt.Errorf("%w: %s killed by signal %v: %s", pdf.ErrEngineCrashed, s.Name, ws.Signal(), diag),
		}
	}
	return &extract.MalformedSourceError{
		MediaType: pdf.MediaType,
		Err:       fmt.Errorf("%s exited %d: %s", s.Name, exitErr.ExitCode(), diag),
	}
}

// labeled is an error with its own message (naming the tool that actually ran,
// e.g. pdfinfo) that still matches the caller's sentinel with errors.Is.
type labeled struct {
	msg      string
	sentinel error
}

func (e *labeled) Error() string { return e.msg }
func (e *labeled) Unwrap() error { return e.sentinel }

// capWriter forwards child stdout to sink, counting bytes; the first write that
// would cross limit kills the child (via cancel) and fails, so a PDF that
// expands enormously cannot grow the heap or the pipe unbounded. It only counts:
// the sink decides whether anything is stored. It is written only by the exec
// copy goroutine and read after Wait, so it needs no locking.
type capWriter struct {
	sink        io.Writer
	n           int64
	limit       int64
	cancel      context.CancelFunc
	errTooLarge error
	exceeded    bool
}

func (w *capWriter) Write(p []byte) (int, error) {
	if w.exceeded || w.n+int64(len(p)) > w.limit {
		w.exceeded = true
		w.cancel() // stop the child from producing more output
		return 0, w.errTooLarge
	}
	n, err := w.sink.Write(p)
	w.n += int64(n)
	return n, err //nolint:wrapcheck // pass-through of the sink's error (e.g. a closed pipe)
}

// truncWriter captures up to limit bytes and silently drops the rest, always
// reporting a full write so the child is never stalled by a bounded stderr.
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
