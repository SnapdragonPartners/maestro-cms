# ADR 0007: PDF extraction uses a timeout watchdog as a stopgap for a hanging parser

- Status: Superseded by [ADR 0010](0010-pluggable-pdf-engines.md) (pluggable PDF engines; out-of-process pdftotext)
- Date: 2026-06-02

## Context

`extract/pdf` uses `github.com/dslipak/pdf` (pure Go, no CGO), matching Morris's
choice. The code was integrated in Morris but **never exercised against real
PDFs**, so maestro-cms treated it as new code.

During testing we found a concrete failure: a structurally-valid PDF whose single
page has no `/Contents` stream parses fine (`NumPage()==1`, page not null) but then
**`GetPlainText` hangs** — it spins without returning. This is worse than the
panics Morris's design anticipated:

- `recover()` cannot catch a hang, so the panic-recovery boundary is useless
  against it.
- The parser ignores `context` cancellation (it has no ctx awareness), so a
  caller cannot interrupt it cooperatively.

For a library that accepts untrusted PDFs, an uninterruptible hang is a
denial-of-service vector: one crafted input ties up a worker indefinitely.

## Decision

Ship `extract/pdf` now with a **wall-clock timeout watchdog** as an explicit
stopgap, and plan a spike to fix or replace the parser.

- `Extractor.Timeout` (default `DefaultTimeout` = 30s) bounds one Extract call.
- Parsing runs in a goroutine; Extract `select`s on the result, `ctx.Done()`, and
  a timer. On timeout it returns `extract.ErrMalformedSource`.
- The panic-recovery boundary stays (for the inputs that *do* panic).

This is knowingly a band-aid, not a fix:

- A timed-out parse **leaks its goroutine** — the worker is still stuck in the
  uninterruptible parser, so repeated hostile input grows memory. The result
  channel is buffered so the leaked goroutine can finish and exit *if* it ever
  unblocks, but a true hang never does.
- The timeout is wall-clock, so it conflates "hostile hang" with "legitimately
  huge PDF". 30s is a generous default; callers tune `Timeout`.

The package doc states this loudly: do not point `extract/pdf` at high-volume
untrusted input without external process isolation until the spike resolves it.

## Consequences

- `extract/pdf` is safe to ship for trusted / low-volume input; a hang degrades to
  `ErrMalformedSource` instead of blocking forever.
- Memory can grow under sustained hostile input (leaked goroutines) — the residual
  risk this stopgap does not close.
- A spike is queued (see [docs/deferred-tooling.md](../deferred-tooling.md)) to
  either diagnose dslipak/pdf (is the no-`/Contents` hang fixable upstream / by us
  pre-validating page structure?), replace it with a maintained library, or move
  PDF parsing out-of-process. This ADR is superseded by whatever that spike lands.

## Alternatives considered

- **Defer PDF entirely** — cleanest (don't ship a known-imperfect dep), but DOCX
  is ready and PDF-with-a-bounded-failure is useful for trusted input now. We took
  the stopgap to unblock that, with the risk documented.
- **Out-of-process sandbox now** — the real fix, but disproportionate before we
  understand the failure; that's part of the spike's decision space.
