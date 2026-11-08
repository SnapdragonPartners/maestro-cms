package popplerexec

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SnapdragonPartners/maestro-cms/extract"
	"github.com/SnapdragonPartners/maestro-cms/extract/pdf"
)

var (
	errUnavail  = errors.New("test: unavailable")
	errTooLarge = errors.New("test: too large")
)

// fakeBin writes an executable shell script and returns its path. Scripts use
// exec for long-running commands so a kill reaches the process holding stdout.
func fakeBin(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake binaries are shell scripts")
	}
	p := filepath.Join(t.TempDir(), "fake")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil { //nolint:gosec // test helper must be executable
		t.Fatal(err)
	}
	return p
}

func spec(bin string) Spec {
	return Spec{
		Name:           "fake",
		Binary:         bin,
		Stdin:          []byte("%PDF-1.4"),
		Timeout:        5 * time.Second,
		MaxOutput:      1 << 20,
		ErrUnavailable: errUnavail,
		ErrTooLarge:    errTooLarge,
	}
}

func TestRunSuccessReadsStdin(t *testing.T) {
	var out bytes.Buffer
	if err := Run(context.Background(), spec(fakeBin(t, "cat")), &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.String() != "%PDF-1.4" {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestRunClassification(t *testing.T) {
	cases := []struct {
		name      string
		script    string
		timeout   time.Duration
		maxOut    int64
		want      error
		malformed bool
		crashed   bool
	}{
		{name: "non-zero exit", script: "echo bad >&2; exit 3", malformed: true},
		{name: "crash by signal", script: "kill -ABRT $$", malformed: true, crashed: true},
		{name: "stdout flood hits cap", script: "exec yes", maxOut: 4096, want: errTooLarge},
		{name: "timeout", script: "exec sleep 10", timeout: 100 * time.Millisecond, malformed: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := spec(fakeBin(t, tc.script))
			if tc.timeout > 0 {
				s.Timeout = tc.timeout
			}
			if tc.maxOut > 0 {
				s.MaxOutput = tc.maxOut
			}
			err := Run(context.Background(), s, io.Discard)
			if err == nil {
				t.Fatal("Run succeeded")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
			if got := errors.Is(err, extract.ErrMalformedSource); got != tc.malformed {
				t.Errorf("malformed = %v, want %v (err %v)", got, tc.malformed, err)
			}
			if got := errors.Is(err, pdf.ErrEngineCrashed); got != tc.crashed {
				t.Errorf("crashed = %v, want %v (err %v)", got, tc.crashed, err)
			}
		})
	}
}

func TestRunCrashNamesSignal(t *testing.T) {
	err := Run(context.Background(), spec(fakeBin(t, "kill -ABRT $$")), io.Discard)
	if !strings.Contains(err.Error(), "abort") {
		t.Fatalf("err %q doesn't name the signal", err)
	}
}

func TestRunCallerCancel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := Run(ctx, spec(fakeBin(t, "exec sleep 10")), io.Discard)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if errors.Is(err, extract.ErrMalformedSource) {
		t.Fatal("caller cancellation must not be malformed")
	}
}

func TestRunMissingBinary(t *testing.T) {
	err := Run(context.Background(), spec("maestro-cms-no-such-binary-xyz"), io.Discard)
	if !errors.Is(err, errUnavail) || !strings.Contains(err.Error(), "maestro-cms-no-such-binary-xyz") {
		t.Fatalf("err = %v, want unavailable naming the binary", err)
	}
}

func TestStreamConsumes(t *testing.T) {
	var got string
	err := Stream(context.Background(), spec(fakeBin(t, "printf 'a\\nb\\n'")), func(r io.Reader) error {
		b, err := io.ReadAll(r)
		got = string(b)
		return err
	})
	if err != nil || got != "a\nb\n" {
		t.Fatalf("Stream = %v, got %q", err, got)
	}
}

// A consumer that fails while the child is still writing kills the child, so
// it can't block on a full pipe, and Stream returns the consumer's error.
func TestStreamConsumerErrorStopsChild(t *testing.T) {
	errParse := errors.New("parse failed")
	s := spec(fakeBin(t, "exec yes"))
	s.MaxOutput = 1 << 40
	start := time.Now()
	err := Stream(context.Background(), s, func(r io.Reader) error {
		buf := make([]byte, 1024)
		_, _ = io.ReadFull(r, buf)
		return errParse
	})
	if !errors.Is(err, errParse) {
		t.Fatalf("err = %v, want the consumer's error", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("Stream took %s; child not stopped", d)
	}
}

// A cap hit mid-stream wins over whatever the consumer makes of the truncated
// output.
func TestStreamCapWins(t *testing.T) {
	s := spec(fakeBin(t, "exec yes"))
	s.MaxOutput = 64 << 10
	err := Stream(context.Background(), s, func(r io.Reader) error {
		_, _ = io.Copy(io.Discard, r)
		return errors.New("truncated")
	})
	if !errors.Is(err, errTooLarge) {
		t.Fatalf("err = %v, want too large", err)
	}
}

// A consumer that rejects complete output from a successful child is reported.
func TestStreamConsumerErrorAfterSuccess(t *testing.T) {
	errParse := errors.New("bad final line")
	err := Stream(context.Background(), spec(fakeBin(t, "echo hi")), func(r io.Reader) error {
		_, _ = io.Copy(io.Discard, r)
		return errParse
	})
	if !errors.Is(err, errParse) {
		t.Fatalf("err = %v, want consumer error", err)
	}
}

// A consumer that stops reading early without an error doesn't hang the child.
func TestStreamConsumerReturnsEarly(t *testing.T) {
	err := Stream(context.Background(), spec(fakeBin(t, "head -c 200000 /dev/zero")), func(io.Reader) error {
		return nil
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
}

// A child that exits non-zero is reported even if the consumer also failed on
// its partial output.
func TestStreamChildFailureWinsOverLateConsumerError(t *testing.T) {
	err := Stream(context.Background(), spec(fakeBin(t, "printf 'partial'; exit 2")), func(r io.Reader) error {
		_, _ = io.Copy(io.Discard, r)
		return errors.New("short row")
	})
	if !errors.Is(err, extract.ErrMalformedSource) || !strings.Contains(err.Error(), "exited 2") {
		t.Fatalf("err = %v, want the child's exit", err)
	}
}

// Sentinel errors carry the tool's own name, not the caller package's.
func TestErrorsNameTheTool(t *testing.T) {
	s := spec("maestro-cms-no-such-binary-xyz")
	s.Name = "pdfinfo"
	err := Run(context.Background(), s, io.Discard)
	if !errors.Is(err, errUnavail) || !strings.HasPrefix(err.Error(), "pdfinfo: ") {
		t.Fatalf("unavailable err = %q", err)
	}
	s = spec(fakeBin(t, "exec yes"))
	s.Name, s.MaxOutput = "pdfinfo", 4096
	err = Run(context.Background(), s, io.Discard)
	if !errors.Is(err, errTooLarge) || !strings.HasPrefix(err.Error(), "pdfinfo: ") || !strings.Contains(err.Error(), "4096") {
		t.Fatalf("too-large err = %q", err)
	}
}

// A consumer that fails on output a timed-out child left incomplete gets the
// timeout reported, not its own error.
func TestStreamTimeoutBeatsLateConsumerError(t *testing.T) {
	s := spec(fakeBin(t, "printf 'partial'; exec sleep 10"))
	s.Timeout = 200 * time.Millisecond
	err := Stream(context.Background(), s, func(r io.Reader) error {
		_, _ = io.Copy(io.Discard, r)
		return errors.New("short row")
	})
	if !errors.Is(err, extract.ErrMalformedSource) || !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("err = %v, want the timeout", err)
	}
}

// White-box: with the abort flag set and the run already past its deadline,
// run reports the timeout, not errAborted. Pins the check order in run.
func TestRunTimeoutOutranksAbort(t *testing.T) {
	aborted := &atomic.Bool{}
	aborted.Store(true)
	runCtx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	err := run(context.Background(), runCtx, spec(fakeBin(t, "cat")), io.Discard, aborted)
	if errors.Is(err, errAborted) || !errors.Is(err, extract.ErrMalformedSource) || !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("err = %v, want the timeout", err)
	}

	// And an abort without a deadline is reported as the abort.
	runCtx2, cancel2 := context.WithCancel(context.Background())
	cancel2()
	err = run(context.Background(), runCtx2, spec(fakeBin(t, "cat")), io.Discard, aborted)
	if !errors.Is(err, errAborted) {
		t.Fatalf("err = %v, want errAborted", err)
	}
}
