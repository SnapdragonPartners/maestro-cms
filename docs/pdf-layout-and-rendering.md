# Design: PDF layout text, word positions and page rendering

Status: **Proposed.** This is the plan for
[#25](https://github.com/SnapdragonPartners/maestro-cms/issues/25). It records
the discussion with maestro-agents on that issue and the measurements behind it.
Once reviewed, its decisions become ADR 0011, which extends (does not supersede)
[ADR 0010](adr/0010-pluggable-pdf-engines.md).

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
- `pdftotext.Engine` runs `pdftotext -enc UTF-8 -q - -` (PDF on stdin, text on
  stdout, pages split on form feeds), with:
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

## 4. Scope

In scope, in delivery order (§10):

1. A layout text mode on the existing engine (`-layout`).
2. A layout call returning every page's size and its words with boxes and
   structure, from `pdftotext -tsv`.
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
  optional interface (e.g. `interface{ Mode() string }`) that the Extractor
  checks; engines without it get no `pdf.mode` key.
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
	Number int     // 1-based
	Width  float64 // points, in the page box, after rotation
	Height float64
	Words  []Word  // in the engine's reading order
}

type Word struct {
	Text  string
	Box   Rect // points; origin top-left of the page box; Y grows down
	Block int  // engine structure ids, unique within the page
	Line  int  // unique within the page
	Conf  int  // OCR confidence if the text layer provides one, else -1
}

type Rect struct{ X0, Y0, X1, Y1 float64 }
```

- JSON tags on all exported fields, and the `Version` field, because consumers
  will persist layouts (Morris persists its page sidecar today). Changes to the
  serialized form bump `Version`.
- Every page is returned, with its size, even with no words. The `Extractor`
  drops empty pages, but a UI still renders them, and this gives the page count
  without a third binary (`pdfinfo`).
- Word ids: TSV's `block_num`/`line_num` are per-flow or per-block counters, so
  the parser renumbers them to be unique within the page.

### 6.2 Implementation: `pdftotext -tsv -cropbox`

- `-tsv`, not `-bbox-layout`: it avoids the crash in §3.2, it's half the size
  (§3.4), and it parses with the standard library (no XML).
- `-cropbox`, and the same flag for rendering (§7), so word boxes and images
  share one coordinate space. The crop box is what viewers show.
- Parser rules:
  - Identify row kinds by the `level` column, never by the `###PAGE###`,
    `###FLOW###`, `###LINE###` sentinel text, which a document could contain.
  - Split each row into at most 12 fields, so a tab inside word text stays in
    the text field.
  - Reject rows with fewer fields, unparseable numbers, or page numbers out of
    order as malformed output (an engine error, §8), not silently skip them.
  - Stream the output (line scanner over the capped stdout), don't buffer then
    parse.
- Output cap: TSV is ≈13x the plain text, so the layout call has its own cap,
  `WithMaxLayoutBytes`, default 256 MiB. Reusing the 64 MiB text cap would make
  documents that extract fine as text fail on layout.
- Rotation: TSV coordinates are reported after page rotation. A rotated-page
  fixture (`/Rotate 90`) verifies Width/Height and boxes match what `pdftoppm`
  renders.

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

func AssemblePage(p PageLayout, s Strategy) PageText
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
  blocks by a blank line. On the §3.1 sample this reproduces default-mode text
  exactly; a corpus comparison test checks it on more documents.
- **`Rows`:** a word joins a row when its vertical extent overlaps the row's
  extent by at least half of the shorter height. This tolerates superscripts
  and mixed font sizes better than "any overlap". Within a row, words are
  sorted by `X0`. Gaps wider than a threshold (relative to the row's median
  character width) become a run of spaces proportional to the gap, so columns
  read as separated; smaller gaps become one space. Rows are joined by `\n`,
  ordered by their top.
- **Known limits of `Rows`**, documented and tested rather than solved:
  skewed scans (a long line tilting across row boundaries), multi-column prose
  (interleaves), overlapping text. No deskewing in this version.
- **Whitespace:** the assembled text needs no normalization pass (which would
  shift offsets): no trailing spaces, no leading or trailing blank lines.

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
	Data     []byte
	Format   Format
	Width    int     // actual pixels, read from the image header
	Height   int
	ScaleX   float64 // pixels per point actually applied: Width / page width in points
	ScaleY   float64
}

func (r *Renderer) Render(ctx context.Context, data []byte, o RenderOptions) (*Image, error)
```

- **Sizing:** DPI is a target, `MaxPixels` is the hard limit. The renderer works
  out the requested scale from DPI, and if the longer side would exceed
  `MaxPixels`, uses `-scale-to MaxPixels` instead. maestro-agents' review UI
  asks for a DPI; images sent to a model need "fit within N px" for provider
  limits.
- **Report what was applied.** Width and height come from the PNG/JPEG header
  (`image.DecodeConfig`, stdlib), and the scale from those divided by the page
  size, so callers convert point coordinates to pixels without assuming the DPI
  was honored. Rendering needs the page size: the caller passes it (from
  `Layout`), or the renderer reads it from the image size and the DPI it asked
  for. (Open question 3.)
- **Format:** PNG default for born-digital pages (sharp text); JPEG for scanned
  pages, 4–10x faster and ~10x smaller (§3.4). The docs say which to pick.
- `-cropbox`, matching §6.2.
- Page out of range → a distinct `ErrPageOutOfRange`, not malformed.

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

Before adding the second and third commands, move the subprocess code out of
`pdftotext` into `extract/pdf/internal/popplerexec`:

- binary lookup, timeout and cancellation;
- the capped stdout writer (`capWriter`) and truncating stderr writer;
- classifying the result in today's priority order: output cap, caller
  cancellation, own timeout, exit status, start failure.

Then `pdftotext.Pages`, `pdftotext.Layout` and `pdftoppm.Render` share one
copy, so their safeguards can't drift apart. Each package keeps its exported
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

- `ReadingOrder` text reproduces default-mode text (tested on a corpus), so
  Morris can switch to layout-derived text without a retrieval-quality change.
- Byte offsets per word give page ranges directly (a page's range is its words'
  first and last spans plus the join separator).
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
| 2 | ADR 0011; move plumbing to `internal/popplerexec`; `ErrEngineCrashed`; `LayoutEngine`, TSV parser, `AssemblePage` | v0.8.0 |
| 3 | `extract/pdf/pdftoppm`; deployment notes | v0.9.0 |

Each step is additive (no breaking changes) and its own PR. maestro-agents'
verifier starts on step 1's text and moves to the layout call when step 2 lands,
so the UI's highlights come from the same text the checks ran on.

## 11. Testing

- **Unit tests without Poppler** (run in CI everywhere):
  - TSV parser on recorded TSV: level handling, the sentinel-text collision,
    tabs in word text, short rows, out-of-order pages, empty pages.
  - `AssemblePage`: the `Text[Start:End] == Word.Text` property for both
    strategies over generated layouts; golden outputs for a statement-shaped
    layout (`Rows` keeps label and numbers together) and a two-column prose
    layout (`ReadingOrder` keeps columns separate).
  - Error classification with a fake binary: exits non-zero, kills itself with
    SIGABRT (→ `ErrEngineCrashed`, also `ErrMalformedSource`), floods stdout
    (→ cap), sleeps (→ timeout), missing (→ unavailable).
- **Integration tests with Poppler** (skipped when the binary is absent, as
  today), on synthetic fixtures committed to the repo:
  - a statement-shaped table (default vs `-layout`; `Rows` vs `ReadingOrder`);
  - a rotated page (`/Rotate 90`): word boxes line up with the rendered image;
  - CropBox ≠ MediaBox: word boxes and rendering agree;
  - a blank page and an image-only page: present in `Layout`, renderable;
  - a non-embedded Helvetica: renders legibly (checks fonts are installed);
  - a reproduction of the §3.2 crash, if one can be made synthetically. We'll
    check Poppler's tracker and main branch first; if no synthetic repro, the
    fake binary covers classification.
- **Corpus comparison** (local only, not CI, documents not committed): run
  `ReadingOrder` against default-mode text and report differences, on the
  large test document and whatever samples are available.

## 12. Deployment notes (for consumers' images)

- Text and layout need only `pdftotext`. Rendering adds `pdftoppm`.
- `pdftoppm` needs runtime data that an `ldd`-derived library closure (Morris's
  distroless approach, ADR 0047) doesn't carry:
  - fonts and a fontconfig configuration, or PDFs that don't embed their fonts
    render with missing or blank text (maestro-agents has a sample using a
    non-embedded Helvetica);
  - `poppler-data`, for CJK documents.
- Recommend a CI smoke test in the consumer's image that renders a
  non-embedded-font fixture and checks the result, not just that the binary
  starts.
- Licensing is unchanged from ADR 0010: Poppler is GPL, invoked as a separate
  process; an image that bundles it carries its obligations.

## 13. Open questions

1. **Releases:** one minor release per step (as in §10), or steps 2 and 3
   together as v0.8.0?
2. **`pdf.mode` mechanism:** an optional `Mode()` interface on the engine (§5),
   or have the engine set metadata on `Page` (a new optional field)?
3. **Renderer page size:** should `Render` take the page size from the caller
   (from `Layout`), or report scale only from the requested DPI and the actual
   pixels? The first is exact; the second avoids coupling the two packages.
4. **`Rows` thresholds:** the overlap fraction and gap threshold are tuned on
   synthetic fixtures; maestro-agents can tune them on their confidential
   samples. Expose them as options, or keep them internal until a second
   consumer needs different values?
5. **Default layout cap:** 256 MiB is ≈4x the text cap; is that the right
   ceiling for a worker's heap?
