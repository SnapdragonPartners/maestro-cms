# Design: PDF layout text, word positions and page rendering

Status: **Accepted.** This is the plan for
[#25](https://github.com/SnapdragonPartners/maestro-cms/issues/25). It records
the discussion with maestro-agents on that issue and the measurements behind it.
Its decisions are recorded in
[ADR 0011](adr/0011-pdf-word-positions-and-layout-assembly.md), which extends
(does not supersede) [ADR 0010](adr/0010-pluggable-pdf-engines.md). Step 1
shipped in v0.7.0, step 2 in v0.8.0, and step 3 (rendering) is v0.9.0. Where
implementation refined the plan, ADR 0011 is authoritative (notably: `Conf` is
100 for text-layer words, the layout cap is 128 MiB, the `Rows` thresholds are
options, and the renderer probes pages 1..N, §7.1).

## 1. Why

maestro-agents' first agent, standalone capital-statement intake, reads PE/VC
capital-account statement PDFs through maestro-cms's existing out-of-process
Poppler engine (`extract/pdf/pdftotext`). Today that engine returns plain text
per page. The agent needs three more things:

1. **Text with each row's label and numbers on the same line.** Three of its
   verification checks depend on it: the ending balance agrees everywhere the
   statement repeats it (QTD, YTD, inception-to-date columns); the value comes
   from the limited-partner column, not the total-fund column; the period comes
   from a period label, not a print timestamp. The model's reading of the text
   needs it too, until the model receives the PDF visually.
2. **Words with bounding boxes, and their positions in a page text.** The review
   UI highlights the exact copy of a number a value came from. The same number
   often appears several times on a page (QTD and YTD are identical within one
   quarter), so searching the words isn't enough: the verifier matches on text,
   then needs the boxes of what it matched.
3. **Page images**, for the review UI and for models that take images but not
   PDFs.

Other consumers want the same things. Morris cites PDF hits by page today; word
positions would let it cite and highlight below page level, and rendering would
let it preview pages. That is a **future** Morris plan, not part of this work,
but nothing here may rule it out (§9).

## 2. Current state

- `pdf.Engine` (`Name`, `Pages`) returns `[]pdf.Page{Number, Text}`. The
  `pdf.Extractor` normalizes each page's whitespace (`extract.NormalizeWhitespace`:
  trims each line's ends, collapses blank lines), drops empty pages, and emits one
  `text/plain` artifact per page with `pdf.page` and `pdf.engine` metadata.
  (Step 1 adds `pdf.mode` for engines that report a mode; see §5.)
- `pdftotext.Engine` runs `pdftotext -enc UTF-8 -q - -` (PDF on stdin, text on
  stdout, pages split on form feeds; step 1 adds `-layout` under `WithLayout()`),
  with:
  - a per-call timeout (default 30 s), killed on timeout or caller cancellation;
  - a 64 MiB stdout cap (`ErrOutputTooLarge`) and a 64 KiB stderr cap;
  - `ErrEngineUnavailable` when the binary is missing;
  - a non-zero exit → `extract.MalformedSourceError` (matches
    `ErrMalformedSource`); failure to start → a plain operational error.
- `extract/pdf/purego` (dslipak) is the trusted-input-only fallback. It can
  provide none of the new features, and doesn't need to.

## 3. Findings that shaped the design

All measured with Poppler 26.04.0.

### 3.1 Default mode separates labels from numbers

pdftotext's default (reading-order) mode emits each text flow separately. On a
two-column statement, the labels come out as one run and the numbers as
another:

```
Beginning balance
Contributions
Ending balance

1,234,567.89
50,000.00
1,284,567.89
```

On maestro-agents' hardest sample, default mode detaches even the column
headers ("Limited Partner", "Total Fund", "QTD", "YTD"), and each row's six
numbers come out below its label. `-layout` keeps every row on one line under
its headers.

### 3.2 `-bbox` and `-bbox-layout` crash on a real statement; `-tsv` doesn't

On one of maestro-agents' five samples (produced by Aspose.PDF for .NET 24.12),
both `-bbox` and `-bbox-layout` abort with
`libc++abi: ... std::out_of_range: basic_string`, exit 134 (SIGABRT), no output.
On the same file `-tsv`, `-layout` and `pdftoppm` succeed. The file is
confidential and can't be shared.

`-tsv` carries the same structure as `-bbox-layout`. Each row has `level`,
`page_num`, `par_num`, `block_num`, `line_num`, `word_num`, `left`, `top`,
`width`, `height`, `conf`, `text`. Level 1 rows are pages (their box is the page
size), levels 3 and 4 are flows/blocks and lines, level 5 rows are words.

### 3.3 Reading-order words bring the label problem back

In `-tsv` output on the Aspose statement, every number cell is its own block or
line, apart from its row label. Page text assembled in TSV (reading) order
separates labels from numbers, exactly as default mode does (§3.1).

### 3.4 Sizes and timings on a large document

A 342-page OCR'd scan (~342 MB; ABBYY FineReader text layer, page size
767 × 614 pt). A 29 MB recompression of the same document gave identical text
numbers.

| | Raw text | `-layout` | `-tsv` | `-bbox-layout` |
|---|---|---|---|---|
| Output | 337 KB | 493 KB | 4.4 MB (≈13x) | 8.6 MB (≈25x) |
| Time (whole document) | 0.7 s | 0.7 s | 0.8 s | 0.8 s |

TSV parsed cleanly: one level-1 row per page (342), 12 fields on every row.

Rendering one page of the 342 MB file with `pdftoppm`:

| | 72 dpi PNG | 150 dpi PNG | 300 dpi PNG | 150 dpi JPEG |
|---|---|---|---|---|
| Time | 0.6 s | 1.1 s | 5.0 s | 0.3 s |
| Size | 0.4 MB | 1.6 MB | 5.6 MB | 0.14 MB |
| Pixels | 772 × 620 | 1608 × 1291 | 3215 × 2582 | 1608 × 1291 |

- PNG compression dominates on scanned pages: JPEG was 4–10x faster and about
  10x smaller.
- Passing the PDF on stdin cost the same as passing a file path.
- `pdftoppm` streams PDF on stdin to a single page's image on stdout when no
  output root is given.

### 3.5 Crop box and rotation: TSV page sizes are not rotated

Synthetic one-page fixtures (text at a known position; MediaBox 612 × 792;
variants with a 400 × 292 CropBox and/or `/Rotate 90`), checked against the
dark pixels of a 72 dpi `pdftoppm` render:

- **`-cropbox` behaves the same in both tools.** With it, TSV's page size and
  word coordinates are relative to the crop box, and `pdftoppm -cropbox`
  renders exactly the crop box.
- **TSV word boxes are rotated; TSV page sizes are not.** On the rotated page,
  TSV gives the word box in displayed (rotated) coordinates, and it matches the
  rendered ink: x 695–717, y 100–155 against ink at x 700–716, y 102–152 on a
  792 × 612 image. The same holds with a crop box. But TSV's page row still
  reports the unrotated size (612 × 792, or 400 × 292 with the crop box), and
  TSV carries no rotation field. So TSV alone can't produce a correct page size
  for a rotated page, blank pages included.
- **`pdfinfo -f 1 -l <large> -box -`** reports, per page, the crop-box size
  (unrotated) and the rotation (`Page 1 rot: 90`). It reads the PDF on stdin,
  clamps a last-page number beyond the document, and took 0.14 s on the 342 MB
  document (2,409 lines of output for 342 pages). It ships in `poppler-utils`
  with `pdftotext` and `pdftoppm`.

### 3.6 Default mode joins hyphenated line ends; TSV doesn't

A line ending in `hyphen-` followed by `ated` on the next line comes out as
`hyphenated` in default mode. TSV keeps both words as written, so text built
verbatim from TSV words reads `hyphen-`, newline, `ated`. Text assembled from
words therefore can't be identical to default-mode text. (Found in review with
a synthetic Poppler 26.04.0 fixture.)

## 4. Scope

In scope, in delivery order (§10):

1. A layout text mode on the existing engine (`-layout`).
2. A layout call returning every page's size and its words with boxes and
   structure, from `pdftotext -tsv` (words) and `pdfinfo` (page sizes and
   rotation).
3. Pure page-text assembly from those words, with two strategies, each giving
   every word's byte offsets in the text.
4. Rendering one page to PNG or JPEG via `pdftoppm`.
5. Shared subprocess plumbing, crash diagnosis, deployment notes, and ADR 0011.

Out of scope:

- OCR. Scanned pages without a text layer still have no words; they can still
  be rendered.
- Bulk/range rendering (§7.4).
- An MCP server over this package. maestro-agents calls it in-process; a thin
  wrapper can come when a non-Go consumer wants one.
- Morris's cutover to word-level citations (§9).
- Changing `pdf.Engine` or the `purego` engine.

## 5. Layout text mode

```go
// extract/pdf/pdftotext
func WithLayout() Option // runs pdftotext with -layout
```

- Pages split on form feeds as today. `Page.Text` keeps the column spacing; the
  `Extractor` still normalizes whitespace, which trims each line's leading and
  trailing spaces and collapses blank lines but leaves the gaps inside a line.
  So rows stay intact; leading indentation is lost. maestro-agents confirmed
  that's fine: they won't infer columns from character offsets in this text.
  Callers wanting the raw spacing call `Engine.Pages` directly.
- New metadata key `pdf.mode` (`MetaMode`) on each artifact: `"layout"` or
  `"raw"`, so stored text says which mode produced it. The pdftotext engine sets
  it. Since `pdf.Engine` has no way to report a mode, this needs a small
  optional interface, `pdf.ModeReporter` (`Mode() string`), that the
  Extractor checks; engines without it get no `pdf.mode` key.
- `Name()` stays `"pdftotext"`; the mode is a separate key.
- `extract/preset` keeps the default mode. Changing it would change Morris's
  indexed text; a preset option can be added if wanted.

## 6. Word positions

### 6.1 API

An optional interface in `extract/pdf`, so `pdf.Engine` is not widened (purego
can't implement it):

```go
// LayoutEngine is implemented by engines that can report word positions.
type LayoutEngine interface {
	Layout(ctx context.Context, data []byte) (*Layout, error)
}

type Layout struct {
	Version int          // format version of this struct's serialized form; starts at 1
	Engine  string       // engine Name()
	Box     string       // page box the coordinates are relative to: "crop"
	Pages   []PageLayout // every page, in order, including blank and scanned ones
}

type PageLayout struct {
	Number   int     // 1-based
	Width    float64 // points: the crop box as displayed, i.e. after rotation
	Height   float64
	Rotation int     // 0, 90, 180 or 270, as declared by the page (informational)
	Words    []Word  // in the engine's reading order
}

type Word struct {
	Text  string
	Box   Rect // points; origin top-left of the page box; Y grows down
	Block int  // engine structure ids, unique within the page
	Line  int  // unique within the page
	Conf  int  // engine confidence 0–100, or -1 if none (Poppler: 100 for every text-layer word)
}

type Rect struct{ X0, Y0, X1, Y1 float64 }
```

- JSON tags on all exported fields, and the `Version` field, because consumers
  will persist layouts (Morris persists its page sidecar today). Changes to the
  serialized form bump `Version`.
- Every page is returned, with its size, even with no words. The `Extractor`
  drops empty pages, but a UI still renders them. The page count and sizes come
  from `pdfinfo` (§6.2).
- Word ids: TSV's `block_num`/`line_num` are per-flow or per-block counters, so
  the parser renumbers them to be unique within the page.

### 6.2 Implementation: `pdfinfo` for geometry, `pdftotext -tsv -cropbox` for words

`Layout` runs two subprocesses through the shared plumbing (§8):

1. **Geometry: `pdfinfo -f 1 -l 1000000 -box -`.** Gives the page count and,
   per page, the crop-box size and rotation (§3.5). `Width`/`Height` are that
   size, swapped when the rotation is 90 or 270, so they describe the page as
   displayed and as `pdftoppm` renders it. This is the only source of page
   size and page count; TSV's page rows are used only to check page numbering.
   - Rotation is normalized to 0/90/180/270 (PDF allows any multiple of 90,
     including negative values); anything else is malformed output.
   - Its output has its own small cap (default 16 MiB; it needs about 400
     bytes per page, so that allows roughly 40,000 pages).
2. **Words: `pdftotext -tsv -cropbox -enc UTF-8 -q - -`.** Word boxes come out
   already in displayed coordinates (§3.5), so they are used as-is.

Why these flags and tools:

- `-tsv`, not `-bbox-layout`: it avoids the crash in §3.2, it's half the size
  (§3.4), and it parses with the standard library (no XML).
- `-cropbox` on every tool (`pdfinfo -box` reports it; `pdftoppm -cropbox`,
  §7), so word boxes, page sizes and images share one coordinate space. The
  crop box is what viewers show.
- Consistency check: the number of TSV page rows must equal `pdfinfo`'s page
  count, or the result is malformed output.
- Parser rules:
  - Identify row kinds by the `level` column, never by the `###PAGE###`,
    `###FLOW###`, `###LINE###` sentinel text, which a document could contain.
  - Split each row into at most 12 fields, so a tab inside word text stays in
    the text field.
  - Reject rows with fewer fields, unparseable numbers, non-finite numbers
    (NaN, ±Inf), negative widths or heights, or page numbers out of order as
    malformed output (an engine error, §8), not silently skip them.
  - Coordinates may still be huge or far off the page (a valid PDF can place
    text anywhere). The parser keeps them; consumers of coordinates, including
    `AssemblePage` (§6.3), must not size anything from them.
  - Stream the output: the parser reads TSV lines from the child's stdout as
    they arrive, through the capped sink described in §8, so raw TSV is never
    held in memory alongside the parsed `Layout`. A single line longer than a
    fixed limit (1 MiB) is malformed output, so one giant "word" can't grow
    the scanner's buffer without bound.
- Output cap: TSV is ≈13x the plain text, so the layout call has its own cap,
  `WithMaxLayoutBytes`, default 128 MiB (open question 5). Reusing the 64 MiB
  text cap would make many documents that extract fine as text fail on layout.
- Rotation: rotated-page fixtures (`/Rotate 90` and `270`, with and without a
  crop box) verify that Width/Height and word boxes match what `pdftoppm`
  renders (§11).

### 6.3 Page-text assembly

Assembling text from words is a separate, pure function in `extract/pdf`, with
no Poppler dependency:

```go
type Strategy int

const (
	ReadingOrder Strategy = iota // engine order: block, then line, then word
	Rows                         // geometric rows, left to right
)

type PageText struct {
	Text  string
	Spans []Span // one per word in PageLayout.Words, same index
}

type Span struct{ Start, End int } // byte offsets into Text; End exclusive

func AssemblePage(p PageLayout, s Strategy, opts ...AssembleOption) PageText

func WithRowOverlap(f float64) AssembleOption // Rows only; (0,1], default 0.5
func WithMaxGapSpaces(n int) AssembleOption   // Rows only; 1..32, default 8
```

- **Why two strategies.** `Rows` is right for statements (§3.3). On a
  two-column prose page it would interleave the columns line by line, and that
  page type is Morris's main case. `ReadingOrder` follows the engine's flows and
  suits prose. maestro-agents uses `Rows`; Morris can use `ReadingOrder` later.
- **Agreement by construction.** Each word's text is written verbatim into
  `Text` at its span, so `Text[Start:End] == Word.Text` always holds (tested as
  a property).
- **Byte offsets**, not rune offsets: Go strings and Morris's page spans are
  byte-indexed.
- **`ReadingOrder`:** words in a line joined by one space; lines by `\n`;
  blocks by a blank line. This is **close to, not identical to**, default-mode
  text: default mode joins hyphenated line ends (§3.6) and the verbatim-span
  invariant forbids that. On the §3.1 sample the two match exactly. A corpus
  comparison (§11) measures and documents the differences; it is not a
  guarantee of equality.
- **`Rows`:** a word joins a row when its vertical extent overlaps the row's
  extent by at least half of the shorter height. This tolerates superscripts
  and mixed font sizes better than "any overlap". Within a row, words are
  sorted by `X0`. Rows are joined by `\n`, ordered by their top.
- **Gaps between words in a row are bounded, never sized from raw
  coordinates.** A valid PDF can put two tiny words hundreds of millions of
  points apart, which a proportional rule would turn into billions of spaces
  inside the worker, after the subprocess output cap has done its job. So:
  - the unit is the row's median character width (word width ÷ rune count over
    words with positive width), floored at 1 point; a row with no
    positive-width word uses 1 point;
  - a gap becomes `clamp(round(gap / unit), 1, MaxGapSpaces)` spaces, with
    `MaxGapSpaces` = 8 by default (enough to read as a column break),
    configurable up to a hard ceiling of 32; overlapping or negative gaps
    become one space;
  - the result: assembled text is at most the words' bytes plus 32 bytes per
    word plus one byte per row, so its size is bounded by the TSV output that
    was already capped. A test asserts this bound on adversarial layouts
    (huge gaps, zero-width words, all words on one row, one word per row).
- **Known limits of `Rows`**, documented and tested rather than solved:
  skewed scans (a long line tilting across row boundaries), multi-column prose
  (interleaves), overlapping text. No deskewing in this version.
- **Whitespace:** the assembled text needs no normalization pass (which would
  shift offsets): no trailing spaces, no leading or trailing blank lines.
- **No dehyphenation in this version.** Joining `hyphen-` + `ated` would break
  `Text[Start:End] == Word.Text`. If a consumer needs it (likely Morris, for
  retrieval), it can come later as an additive option with a documented,
  weaker span contract (a span may cover a joined form). Open question 6.

## 7. Page rendering

### 7.1 API

A new subpackage, `extract/pdf/pdftoppm`, with its own binary and its own
`ErrEngineUnavailable` (ADR 0006):

```go
type Format int

const (
	PNG Format = iota
	JPEG
)

type RenderOptions struct {
	Page      int     // 1-based
	Format    Format
	DPI       float64 // target resolution; 0 → default 150
	MaxPixels int     // hard limit on the longer side; 0 → default 2048
	Quality   int     // JPEG only, 1–100; 0 → default 85
}

type Image struct {
	Data       []byte
	Format     Format
	Width      int     // actual pixels, read from the image header
	Height     int
	PageWidth  float64 // the page size used, in points (as displayed)
	PageHeight float64
	ScaleX     float64 // pixels per point actually applied: Width / PageWidth
	ScaleY     float64
}

func (r *Renderer) Render(ctx context.Context, data []byte, o RenderOptions) (*Image, error)
```

- **The renderer reads the page size itself, before rendering.** The image
  alone can't give it: after scaling down, differently sized pages with the
  same aspect ratio give identical images, and the renderer must know the size
  up front to decide whether the DPI would exceed `MaxPixels`. So every
  `Render` call first runs `pdfinfo` for that page through the shared geometry
  helper (§6.2, §8), the same source `Layout` uses, so their coordinates agree
  by construction. It asks for pages 1..N, not N alone: `pdfinfo -f N` with N
  past the last page is a usage error (exit 99) that can't be told apart from
  a malformed PDF, while `-l N` is clamped, so 1..N yields the page count and
  page N's size in one call. On the 342-page scan both take 0.16 s; the probe
  costs about 400 bytes of output per page, so pages past 40,000 are refused. The renderer deliberately takes **no page size from the
  caller**: the explicit pixel flags below force the output size, so a wrong
  caller-supplied size would produce a plausible image with a wrong scale that
  no check could catch. The cost is one extra subprocess (0.14 s on the 342 MB
  document, §3.5), small next to rendering itself.
- **Sizing:** scale = min(DPI / 72, MaxPixels / longer side in points), using
  the displayed (rotated) page size. The renderer passes the resulting pixel
  size explicitly as `-scale-to-x W -scale-to-y H
  -scale-dimension-before-rotation`, rather than a DPI, so the cap holds
  exactly. The last flag is required: without it, Poppler 26.04.0 applies the
  dimensions before rotating, so a 612 × 792-point page with `/Rotate 90`
  asked for 792 × 612 comes out 612 × 792 (verified; with the flag it comes
  out 792 × 612, and unrotated pages are unaffected). maestro-agents' review
  UI asks for a DPI; images sent to a model need "fit within N px" for
  provider limits.
- **Report what was applied.** Width and height come from the PNG/JPEG header
  (`image.DecodeConfig`, stdlib), and the scale from those divided by the page
  size `pdfinfo` reported, so callers convert point coordinates to pixels
  without assuming the DPI was honored. If the header's size differs from the
  requested size by more than a pixel of rounding, the render fails as
  malformed output. This checks that `pdftoppm` honored the requested pixels;
  it does not (and can't) validate the page geometry, which is why the
  geometry comes from `pdfinfo` and not the caller.
- **Format:** PNG default for born-digital pages (sharp text); JPEG for scanned
  pages, 4–10x faster and ~10x smaller (§3.4). The docs say which to pick.
- `-cropbox`, matching §6.2.
- Page out of range → a distinct `ErrPageOutOfRange`, not malformed.
- Options out of range (negative or non-finite DPI, `MaxPixels` above
  `MaxPixelsLimit` = 16384, `Quality` outside 1–100, an unknown format) →
  `ErrInvalidOptions`, before anything runs. The `MaxPixels` ceiling bounds
  `pdftoppm`'s own memory: it holds the uncompressed bitmap (about 800 MB in
  color at the ceiling), which the output cap can't see.
- `Format` has `String()` (`png`, `jpeg`) and `MediaType()` (`image/png`,
  `image/jpeg`) for callers storing or serving images.

### 7.2 Safeguards

Same as the text engine (shared plumbing, §8): timeout (default 30 s per page;
300 dpi PNG of a scanned page took 5 s), kill on cancellation, fail closed when
the binary is missing, an image output cap (default 64 MiB), stderr cap.
`MaxPixels` bounds memory independently of the page's declared size, so an
oversized page at a high DPI can't produce a huge image.

### 7.3 One page per call

`pdftoppm -f N -l N` with the PDF on stdin and the image on stdout. That fits
the existing capped-stdout plumbing with no temporary files. Each call re-reads
the whole PDF; stdin cost the same as a file path (§3.4), so on-demand rendering
(the review UI) is fine.

### 7.4 Range rendering: deferred

Rendering a whole document one call per page re-reads it every time (342 calls
× 342 MB for the large test document). Bulk rendering would want a range mode
that writes to a private temporary directory under a total output cap. Deferred
until a consumer needs bulk rendering; ADR 0011 records it.

## 8. Shared subprocess plumbing and errors

Before adding the new commands (`pdfinfo`, `pdftotext -tsv`, `pdftoppm`), move
the subprocess code out of `pdftotext` into `extract/pdf/internal/popplerexec`:

- binary lookup, timeout and cancellation;
- a **sink-based** stdout cap and the truncating stderr writer. Today's
  `capWriter` both counts and stores (it owns a `bytes.Buffer`), which would
  keep up to the layout cap (§6.2) of raw TSV in memory. The refactor splits the two: the
  cap is a counting wrapper around any `io.Writer` sink that cancels the child
  when the limit is crossed.
  - Text and image calls pass a `bytes.Buffer` as the sink, so they behave as
    today.
  - `Layout` passes the write end of an `io.Pipe`; the TSV parser reads the
    other end in its own goroutine. If parsing fails, the parser closes the
    pipe with the error and cancels the child, so the child can't block on a
    full pipe; the run waits for both the child and the parser before
    classifying the result. A cap hit, timeout or cancellation still wins
    over a parse error, in today's priority order;
- classifying the result in one priority order: output cap, caller
  cancellation, own timeout, (for streaming) a parser abort, then, for a run
  that failed, start failure, death by a signal, non-zero exit (these three are
  mutually exclusive).

Alongside it, `extract/pdf/internal/geometry` runs and parses `pdfinfo` (§6.2),
used by both `pdftotext.Layout` and `pdftoppm.Render`.

Then `pdftotext.Pages`, `pdftotext.Layout` and `pdftoppm.Render` share one
copy, so their safeguards can't drift apart. The timeout applies per
subprocess; `Layout`'s two calls share the caller's context. A missing
`pdfinfo` makes `Layout` return `ErrEngineUnavailable` naming that binary. Each package keeps its exported
sentinel errors (`ErrEngineUnavailable`, `ErrOutputTooLarge`) by wrapping the
internal ones, so existing `errors.Is` checks keep working.

**Crashes.** A child killed by a signal surfaces as an `exec.ExitError` with
exit code −1, so today it already classifies as `ErrMalformedSource`, but the
message says "exited -1" and drops the signal. Add `pdf.ErrEngineCrashed`:

- returned when the child dies from a signal, naming the signal;
- **also matches `ErrMalformedSource`**, so existing callers (Morris maps
  malformed sources to its own error category) behave as before;
- rationale: a crash on bad input and a crash on a Poppler bug (§3.2) can't be
  told apart reliably, so failing closed as malformed stays right, but
  operators can now see it was a crash.

**Malformed output.** TSV that doesn't parse (§6.2) is also returned as a
malformed source: it means the engine produced something we can't trust.

## 9. Keeping Morris's future cutover possible

Morris today (`internal/ingest/worker.go`, Morris ADR 0047 §5): Registry
extraction → per-page artifacts joined with `"\n\n"` → a `pages.json` file of
byte ranges per page → each chunk cited to the page of its first byte. It maps
`ErrOutputTooLarge`, `ErrEngineUnavailable`, `pdf.ErrNoEngine`, malformed
sources and `ErrNoContent` to its own error categories.

Morris has said re-indexing is acceptable, so word offsets don't need to match
today's `Page.Text`. What this design keeps open:

- `ReadingOrder` text is close to default-mode text but not identical (§3.6:
  no dehyphenation). Morris's cutover therefore includes a retrieval-quality
  check on its own fixtures, and possibly the dehyphenation option (open
  question 6). This design measures the gap (§11) rather than promising it is
  zero.
- Page ranges come from the assembled page texts, not from word spans. Word
  spans are offsets within one page's text, for highlighting; they are in
  engine-word order while `Rows` may reorder words in the text, and blank or
  scanned pages have none. So a consumer that joins pages into one document
  derives each page's range from its assembled text's byte length and its
  cumulative offset in the joined document, as Morris does today. The
  consumer's policy decides the edge cases; Morris's current one carries over
  directly:
  - empty pages contribute no text and no range;
  - each range is extended to the next range's start, so the join separator
    belongs to the page before it and every byte maps to exactly one page.

  A word's document offset is then its page's start plus its span.
- The serialized `Layout` has JSON tags and a version, so Morris can persist it
  beside its text.
- Error values are the same sentinels; `ErrEngineCrashed` still matches
  malformed.
- Rendering is a separate binary, so Morris's image needs `pdftoppm` only if it
  wants previews.

Not decided here: whether Morris calls `Layout` directly for PDFs (branching on
media type) or the `Extractor` gains an option to also emit a per-page layout
artifact (a JSON media type), keeping Morris on the Registry. The first is
simpler; the second adds an artifact type every consumer must handle. Decide
when Morris plans the cutover.

## 10. Delivery plan

| Step | Contents | Release |
|---|---|---|
| 1 | `WithLayout()`, `pdf.mode` metadata, tests | v0.7.0 |
| 2 | ADR 0011; move plumbing to `internal/popplerexec`; `ErrEngineCrashed`; `internal/geometry` (`pdfinfo`); `LayoutEngine`, TSV parser, `AssemblePage` | v0.8.0 |
| 3 | `extract/pdf/pdftoppm`; deployment notes | v0.9.0 |

Each step is additive (no breaking changes) and its own PR. maestro-agents'
verifier starts on step 1's text and moves to the layout call when step 2 lands,
so the UI's highlights come from the same text the checks ran on.

## 11. Testing

- **Unit tests without Poppler** (run in CI everywhere):
  - TSV parser on recorded TSV: level handling, the sentinel-text collision,
    tabs in word text, short rows, out-of-order pages, empty pages.
  - `pdfinfo` parser on recorded output: rotation normalization (negative and
    >360 values), page-count mismatch with TSV, missing pages.
  - `AssemblePage`: the `Text[Start:End] == Word.Text` property for both
    strategies over generated layouts; golden outputs for a statement-shaped
    layout (`Rows` keeps label and numbers together) and a two-column prose
    layout (`ReadingOrder` keeps columns separate); the size bound on
    adversarial layouts (two tiny words ~10⁹ points apart, zero-width words,
    NaN rejected earlier by the parser).
  - Renderer sizing: scale chosen from DPI vs `MaxPixels`, and the
    header-size check.
  - Streaming `Layout` with a fake binary: TSV that turns malformed halfway
    through a large output stops the child (no hang on a full pipe) and
    returns malformed output; a cap hit mid-stream returns
    `ErrOutputTooLarge`, not a parse error; an over-long line is rejected.
  - Page ranges: a `Rows`-assembled page whose words are reordered, plus
    blank pages, joined Morris-style; every byte maps to exactly one page and
    each word's document offset lands on its text.
  - Error classification with a fake binary: exits non-zero, kills itself with
    SIGABRT (→ `ErrEngineCrashed`, also `ErrMalformedSource`), floods stdout
    (→ cap), sleeps (→ timeout), missing (→ unavailable).
- **Integration tests with Poppler** (skipped when the binary is absent, as
  today), on synthetic fixtures committed to the repo:
  - a statement-shaped table (default vs `-layout`; `Rows` vs `ReadingOrder`);
  - rotated pages (`/Rotate 90`, `270`) on a non-square page, with and without
    a CropBox ≠ MediaBox, rendered through `Render` itself (so with the real
    `-scale-to-x/-scale-to-y -scale-dimension-before-rotation` flags), both
    at the DPI and capped by `MaxPixels`: the image's size matches
    `PageLayout` size × reported scale, and a known word's box, scaled,
    contains its rendered ink (the §3.5 check, automated);
  - a blank rotated page: correct size with no words;
  - a hyphenated line end: `ReadingOrder` keeps both halves (§3.6);
  - a blank page and an image-only page: present in `Layout`, renderable;
  - a non-embedded Helvetica: renders legibly (checks fonts are installed);
  - a reproduction of the §3.2 crash, if one can be made synthetically. We'll
    check Poppler's tracker and main branch first; if no synthetic repro, the
    fake binary covers classification.
- **Corpus comparison** (local only, not CI, documents not committed): run
  `ReadingOrder` against default-mode text and report differences, on the
  large test document and whatever samples are available.

## 12. Deployment notes (for consumers' images)

- Text needs only `pdftotext`. Layout adds `pdfinfo`. Rendering adds
  `pdftoppm` and `pdfinfo` (the renderer reads page sizes itself). All three
  are in `poppler-utils`.
- `pdftoppm` needs runtime data that an `ldd`-derived library closure (Morris's
  distroless approach, ADR 0047) doesn't carry:
  - fonts and a fontconfig configuration, or PDFs that don't embed their fonts
    render with missing or blank text (maestro-agents has a sample using a
    non-embedded Helvetica);
  - `poppler-data`, for CJK documents.
- Recommend a CI smoke test in the consumer's image that renders a
  non-embedded-font fixture and checks the result, not just that the binary
  starts. maestro-cms's own `TestRenderRealNonEmbeddedFont`
  (`extract/pdf/pdftoppm`) is that check: it renders Helvetica (not embedded)
  and requires its ink inside the word box `Layout` reports. Running the
  package's tests in the image, or copying that test, covers it.
- Sizing a deployment: through `Render` (v0.9.0, Poppler 26.04.0, Apple
  silicon) on the 342-page scan, page 1 at the default 150 dpi as PNG
  (1598 × 1279) took 1.1 s and 3.6 MB; page 100 at 300 dpi, capped to 2048 px,
  took 2.1 s as PNG (2.7 MB) and 0.7 s as JPEG at quality 85 (0.34 MB). Sizes
  vary from page to page with content: the page in §3.4's table was 1.6 MB as a
  150 dpi PNG. Each call re-reads the whole PDF, so rendering many pages of a
  large document is linear in its size (§7.4).
- Image size and the output cap: PNGs of scanned pages ran 1 to 1.5 bytes per
  pixel, so the default 64 MiB cap fits them up to about 8000 px on the longer
  side. For larger `MaxPixels`, use JPEG or raise `WithMaxOutputBytes`.
- Licensing is unchanged from ADR 0010: Poppler is GPL, invoked as a separate
  process; an image that bundles it carries its obligations.

## 13. Open questions

1. ~~Releases~~ — resolved: one minor release per step (§10).
2. ~~`pdf.mode` mechanism~~ — resolved in step 1: an optional
   `pdf.ModeReporter` interface (`Mode() string`) that the Extractor checks
   (§5). The mode is per engine, not per page, so a `Page` field would repeat
   the same value on every page and widen a struct purego also fills.
3. ~~Renderer page size~~ — resolved in review: inference from the image is
   unsound, and a caller-supplied size can't be validated, so the renderer
   always reads it with `pdfinfo` (§7.1).
4. ~~`Rows` thresholds~~ — resolved: exposed as `WithRowOverlap` and
   `WithMaxGapSpaces` (hard ceiling 32, out-of-range values clamp), because
   maestro-agents tunes them on samples maintainers can't see (§6.3).
5. ~~Default layout cap~~ — resolved: 128 MiB. The parsed `Layout` measured
   about 1.4x the TSV's size on the 342-page scan, so this bounds one call at
   roughly 180 MiB of heap; callers can raise it.
6. ~~Dehyphenation~~ — resolved: deferred until a consumer asks (the cost is
   slightly worse retrieval on hyphenated line ends). If built, it lives in
   maestro-cms, not in consumers.
