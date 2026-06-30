# ADR 0010: PDF extraction uses pluggable engines; out-of-process pdftotext is the recommended one

- Status: Accepted (supersedes [ADR 0007](0007-pdf-extraction-watchdog-stopgap.md))
- Date: 2026-06-29

## Context

ADR 0007 shipped `extract/pdf` over the pure-Go `dslipak/pdf` parser with a
wall-clock watchdog, because that parser can *hang* (not just panic) on malformed
input — uninterruptible by `recover()` or context. The watchdog was explicitly a
stopgap: a timed-out parse leaks its goroutine, so sustained hostile input grows
memory. In practice the parser also produced poor/empty results on ordinary PDFs.

The realistic ways to do PDF text extraction well:

- **Pure-Go** (`dslipak/pdf`, `ledongthuc/pdf`) — convenient, no system
  dependency, but immature: hangs/poor extraction. `unipdf` is robust but
  commercially licensed.
- **CGO** (MuPDF via `go-fitz`, PDFium) — robust in-process, but CGO
  build/distribution cost *and* a shared crash domain (a crafted PDF can still
  segfault the process). MuPDF/`go-fitz` is **AGPL**, which is a licensing trap
  for an MIT module and its consumers.
- **Out-of-process** (`pdftotext`/Poppler, or a sandboxed subprocess) — robust
  *and* the only option that gives true **kill-on-timeout + crash isolation** for
  untrusted input: a hung or crashing parse dies in a child process with no
  leaked goroutine and no in-process blast radius. Poppler is **GPL**, so we must
  not link it; invoking the installed binary as a subprocess keeps this module
  MIT.

## Decision

- **Make the PDF engine pluggable.** `extract/pdf` defines an `Engine` interface
  (`Name`, `Pages`) and an `Extractor` that delegates to one; engines live in
  subpackages so `extract/pdf` itself stays dependency-free.
- **No default engine.** An unconfigured `Extractor` returns `ErrNoEngine`. We do
  not silently default to a parser known to hang — unsafe-by-default is worse than
  requiring one line of configuration. (A future `Auto` engine that prefers
  `pdftotext` when present, falling back otherwise, may be added as an app-level
  convenience.)
- **`extract/pdf/pdftotext` (out-of-process Poppler) is the recommended engine**
  for production and untrusted input. It runs `pdftotext -enc UTF-8 -q - -`,
  killed on timeout/cancellation; missing binary → `ErrEngineUnavailable`. Adds
  no Go dependency. `extract/preset` wires it as the bundle's PDF engine.
- **`extract/pdf/purego` (dslipak + watchdog) is kept as an explicit fallback**
  for small, *trusted* input only — clearly documented as unsafe for untrusted
  ingestion. The `dslipak/pdf` dependency moves out of `extract/pdf` into this
  subpackage.
- **MuPDF/`go-fitz` is kept out of the Go module entirely** (AGPL). If anyone
  wants MuPDF, it is run out-of-process like Poppler — never linked.
- **Output is one artifact per page** (page provenance matters for retrieval),
  tagged with page number and engine name in `Metadata`. OCR stays out of scope:
  image-only pages are dropped; a text-less PDF yields `ErrNoContent`.

### Licensing boundary (framing)

A subprocess is a strong **license and process boundary**, not a magic firewall:
keep GPL/AGPL code out of the Go module and interact with it only via CLI/service
adapters, so this MIT module and its importers stay clean. But if we *distribute*
a Poppler-based container or companion image, that artifact still carries
Poppler's GPL obligations — that is a packaging/compliance concern for the image,
handled where the image is built.

## Consequences

- `import extract/pdf` pulls no parser; consumers choose an engine explicitly.
- Untrusted PDF ingestion has a real, isolated solution (pdftotext out-of-process)
  — the goroutine-leak/hang risk ADR 0007 left open is closed for that path.
- The pure-Go path remains available, honestly labeled, for trusted/offline use
  with no system dependency.
- Consumers using `pdftotext` must provision the Poppler binary (a deployment
  step; a scratch image needs a base with `poppler-utils`).

## Alternatives considered

- **Keep dslipak as default** — rejected: unsafe-by-default.
- **CGO MuPDF as default** — rejected: AGPL licensing + CGO build cost + shared
  crash domain.
- **Bespoke companion extraction binary/service** — useful later for hardened,
  multi-engine ingestion, but `exec`-ing `pdftotext` already provides
  kill/timeout/isolation, so it is deferred.
