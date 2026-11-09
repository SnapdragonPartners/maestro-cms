# ADR 0011: PDF word positions come from pdftotext -tsv plus pdfinfo; page text is assembled by a pure function

- Status: Accepted (extends [ADR 0010](0010-pluggable-pdf-engines.md); does not supersede it)
- Date: 2026-11-08

## Context

[#25](https://github.com/SnapdragonPartners/maestro-cms/issues/25) asks for
word bounding boxes per PDF page and page rendering, out of process like the
existing `pdftotext` engine. maestro-agents' capital-statement verifier needs
text in which a table row's label and numbers share a line, plus the box of
the exact copy of a number it matched. Morris may later cite and highlight
below page level. The full analysis, measurements and review history are in
[`docs/pdf-layout-and-rendering.md`](../pdf-layout-and-rendering.md). This ADR
records the decisions. Step 1 (`WithLayout()`, `pdf.mode`) shipped in v0.7.0
under ADR 0010's notes. This ADR covers step 2 (v0.8.0) and fixes the
decisions step 3 (rendering, v0.9.0) builds on.

## Decision

### Word positions

- **An optional interface, not a wider `Engine`.** `pdf.LayoutEngine`
  (`Layout(ctx, data) (*pdf.Layout, error)`) is implemented by
  `pdftotext.Engine`. `purego` can't provide positions and doesn't need to.
- **`pdftotext -tsv -cropbox` for words, not `-bbox`/`-bbox-layout`.** Both bbox
  modes abort (SIGABRT) on a real Aspose-produced statement where `-tsv`
  succeeds. TSV is also half the size and parses with the standard library.
- **`pdfinfo -box` is the only source of page count, size and rotation.** TSV
  word boxes are in displayed (rotated) coordinates, but its page rows report
  the unrotated size and it carries no rotation. Page sizes are the crop box,
  swapped for 90/270, so they match what `pdftoppm -cropbox` renders. The TSV
  page count must equal pdfinfo's, or the result is malformed output.
- **The crop box everywhere.** Words, page sizes and (in step 3) images share
  one coordinate space, the region viewers show. `Layout.Box` records it
  (`"crop"`).
- **A versioned, JSON-tagged result.** `Layout.Version` (now 1) changes with
  the serialized form, because consumers persist layouts. Every page is
  returned, blank and scanned ones included, with a non-nil (possibly empty)
  word list.
- **Strict, streaming parsing.** Row kinds come from TSV's `level` column, never
  its sentinel text. Rows are split into at most 12 fields so tabs stay in word
  text. Short rows, unparseable or non-finite numbers, negative sizes, pages out
  of order, words outside their page, a changed header, or a line over 1 MiB are
  malformed output, never skipped. pdfinfo output is parsed just as strictly;
  document metadata that imitates its page lines shows up as a duplicate and
  fails closed. The TSV is parsed as it streams, so the raw output is never
  held in memory.
- **Word ids are renumbered** to be unique within a page (TSV's block and line
  numbers are counters within a flow).
- **`Conf` is passed through.** Poppler reports 100 for every text-layer word,
  not an OCR confidence. The design doc's "else -1" expectation didn't hold;
  -1 remains the value for "none reported".

### Limits

- **`WithMaxLayoutBytes`, default 128 MiB**, separate from the 64 MiB text cap
  because TSV is about 13x plain text. Measured on a 342-page OCR'd scan: 4.35
  MB of TSV became 6.0 MB of live `Layout` (about 1.4x), so the default bounds
  one call at roughly 180 MiB of heap. 256 MiB was proposed. 128 MiB was chosen
  because matching the text cap's coverage would need about 830 MiB of TSV
  anyway, so some documents will extract as text but exceed the layout cap
  whatever the default; callers can raise it.
- **pdfinfo output has a fixed 16 MiB cap** (about 40,000 pages).
- The timeout applies per subprocess. `Layout`'s two calls share the caller's
  context.

### Page-text assembly

- **A pure function in `extract/pdf`:** `AssemblePage(p, s, opts...)` returns
  the text and one byte span per word. `Text[Start:End] == Word.Text` always
  holds.
- **Two strategies.** `Rows` (geometric rows, left to right) keeps statement rows
  together. `ReadingOrder` (engine order) keeps multi-column prose apart.
  Neither suits both page types.
- **Gaps are bounded, never sized from raw coordinates.** A valid PDF can place
  words 10⁹ points apart. A gap becomes `clamp(round(gap / median char width),
  1, max)` spaces, so assembled text is at most the words' bytes plus
  `MaxGapSpacesLimit` (32) bytes per word plus one byte per row.
- **The `Rows` thresholds are options** (`WithRowOverlap`, default 0.5;
  `WithMaxGapSpaces`, default 8, hard ceiling 32). maestro-agents tunes them on
  confidential samples maintainers can't see, so internal constants would force
  a maestro-cms release per tuning round. Out-of-range values clamp, so
  `AssemblePage` never returns an error, and the ceiling keeps the size bound.
- **No dehyphenation.** It would break the span invariant. It is deferred until
  a consumer asks; the cost of deferring is slightly worse keyword and
  embedding retrieval on hyphenated line ends. **If it is built, it lives in
  maestro-cms**, not in each consumer, because remapping spans is the part
  consumers would get wrong. Its form (an option with a weaker span contract, or
  a separate index text with an offset map) is decided then.

### Shared subprocess plumbing and errors

- **`extract/pdf/internal/popplerexec`** holds binary lookup, timeout and kill,
  a counting stdout cap around any sink, the truncated stderr, and one
  classification order: output cap, caller cancellation, own timeout,
  consumer abort, then, for a failed run, start failure, signal, non-zero exit
  (mutually exclusive). Its `Stream` mode hands
  stdout to a parser goroutine through a pipe and kills the child if the parser
  fails, so the child can't block on a full pipe. Engines keep their exported
  sentinels (`ErrEngineUnavailable`, `ErrOutputTooLarge`) by passing them in.
- **`extract/pdf/internal/geometry`** runs and parses pdfinfo for both `Layout`
  and the step-3 renderer, so their page sizes agree by construction.
- **`pdf.ErrEngineCrashed`**: a child killed by a signal returns it, naming the
  signal. It **also matches `extract.ErrMalformedSource`**: a crash on bad input
  and one on a Poppler bug can't be told apart, so failing closed as malformed
  stays right, and existing callers (Morris maps malformed sources) behave as
  before.

### Releases and rendering

- **One minor release per step:** step 2 is v0.8.0, step 3 (`extract/pdf/pdftoppm`
  and deployment notes) is v0.9.0.
- Step 3's renderer reads page sizes through `internal/geometry` itself and
  takes none from the caller. As built (v0.9.0) it probes pages 1..N rather
  than N alone, because `pdfinfo -f N` past the last page exits as a usage
  error indistinguishable from a malformed PDF; 1..N gives the page count, so
  a page past the end returns `ErrPageOutOfRange`.
- Range rendering is deferred until a consumer needs bulk rendering (it would
  need a private temporary directory under a total output cap).

## Consequences

- `Layout` needs `pdfinfo` beside `pdftotext` (both in `poppler-utils`); a
  missing `pdfinfo` returns `ErrEngineUnavailable` naming it.
  `WithPdfinfoBinary` overrides its path.
- The text path (`Pages`) is unchanged in behavior. It now shares the plumbing,
  and crashes match `pdf.ErrEngineCrashed` as well as `ErrMalformedSource`.
- Morris's future cutover stays open: `ReadingOrder` text is close to (not equal
  to) default-mode text, page ranges come from assembled page texts, and the
  serialized `Layout` is versioned. Whether Morris calls `Layout` directly or
  the `Extractor` gains a layout artifact is decided when Morris plans it.
- Known `Rows` limits are documented and tested rather than solved: skewed
  scans, multi-column prose (interleaves), overlapping text.

## Alternatives considered

- **`-bbox-layout`**: rejected; it crashes on a real statement, and its XML is
  twice the size of TSV.
- **Page size from TSV page rows**: rejected; they are unrotated.
- **Widening `pdf.Engine`**: rejected; it breaks `purego` and every third-party
  engine.
- **Internal `Rows` constants**: rejected; see above.
- **Dehyphenation now, or leaving it to consumers**: both rejected; see above.
