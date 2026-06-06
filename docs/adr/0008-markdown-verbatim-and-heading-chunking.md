# ADR 0008: Markdown is extracted verbatim and chunked by heading

- Status: Accepted
- Date: 2026-06-06

## Context

Markdown was deferred in the first extraction slice (ADR 0006 kept it out of the
core text path because its whitespace is semantic). The intended primary
consumer is Maestro: technical content — code documentation and LLM prompts —
where almost nothing is non-technical prose. The output of handling such a
source is a prompt or a retrieval citation fed to an LLM, not a human-readable
rendering.

That use case settles the design questions that made Markdown look like a
deliberate-choice problem rather than a small slice:

- The consumer wants the Markdown *structure* (headings, fenced code, lists),
  because an LLM reads that structure as signal. De-formatting to plain prose
  would destroy exactly what is useful.
- The high-value piece is therefore not nice text extraction but **finding good
  chunk boundaries** — and Markdown's heading structure is precisely the kind of
  semantic boundary the `chunk` package's pluggable `Boundaries` seam exists for.
- Technical Markdown is dense with fenced code blocks, so fence-awareness is
  central, not an edge case: a `#` line inside a code fence is common and must
  not be read as a heading.

## Decision

Land two composing pieces, both stdlib-only.

1. **`extract/markdown` — verbatim extraction.** A subpackage (uniform with the
   other format adapters, ADR 0006) that emits the body verbatim as a
   `text/markdown` artifact. It strips only a leading UTF-8 BOM, coerces invalid
   UTF-8, and removes a leading front-matter block — and explicitly does *not*
   run the prose whitespace normalization the core `text/plain` extractor
   applies. Front matter is removed only when it both opens at the start of the
   document with a delimiter line that is exactly `---` (YAML) or `+++` (TOML)
   and has a matching closing delimiter; a `---` thematic break later in the
   document is never touched. The artifact's media type is `text/markdown`, not
   `text/plain`, because preserving Markdown structure is the whole point.

2. **`chunk.Headings` — heading-aware boundaries.** A `Boundaries` function that
   segments at ATX (`#`…`######`) and setext (`=`/`-` underline) headings,
   fence-aware so a `#` inside a fenced block is not a heading. Each unit runs
   from a heading line to the next heading, so a chunk carries its own section
   title for free (chunk text is exactly `source[Start:End]`). Content before the
   first heading is its own unit. A heading section is a *unit*, not a hard chunk
   boundary: `Split` still packs small sections together and hard-splits oversize
   ones — "one section per chunk" would be a later mode. A document with no
   headings degrades to `Paragraphs` so it still chunks at blank-line boundaries.

3. **Code-aware hard-split.** When `Split` must hard-split an oversize unit that
   contains a fenced code block, it splits on line boundaries instead of the
   prose sentence/word/rune cuts. The rule is deliberately narrow and honest:
   - prefer not to split inside a fenced block;
   - if a fenced block alone exceeds the budget, split it on line boundaries
     *inside* the fence (the only alternative to exceeding the budget);
   - never split mid-line, *except* a single line that itself exceeds the budget,
     which falls back to the prose splitter (word then rune boundaries) — the
     only honest options for one over-budget line are to cut it or to blow the
     budget, and cutting at a word/rune boundary is preferred over an unbounded
     chunk;
   - never split mid-rune.
   Fence-free spans keep the existing prose behavior unchanged.

### Heading ancestry is not modeled

A chunk carries its own (nearest) heading, but not the trail of ancestor
headings above it (e.g. `API › Auth › Tokens`). A flat breadcrumb string is the
wrong shape: ancestry is a parent/child relationship, which is a graph concern,
not a field on `Chunk`. v1 content already carries the stable IDs the future
graph primitive (ADR 0005) will index, so ancestry is left to that layer rather
than baked in here.

## Consequences

- Markdown sources keep their structure end to end, so LLM prompts and citations
  retain the signal an LLM uses.
- Heading-aware chunking gives section-shaped retrieval units; fenced code is not
  false-split into headings and is kept whole when it fits.
- The code-aware path touches `chunk`'s hard-split internals but leaves the prose
  path — and every existing chunk test — byte-for-byte unchanged.
- No heading-ancestry metadata exists yet; consumers that need it reconstruct it
  from byte offsets until the graph primitive lands.

## Alternatives considered

- **Strip Markdown to plain prose** (parse and de-format, dropping `#`, `*`,
  links→text). Rejected: it needs a parser dependency and discards the structure
  the LLM consumer wants, including the heading boundaries `chunk.Headings` uses.
- **Full CommonMark parsing (e.g. goldmark) now.** Not justified for "preserve
  content, segment by section": a stdlib line scanner with fenced-code tracking
  is enough. A parser earns its place only if we later extract structured heading
  metadata or strip formatting.
- **Breadcrumb field on `Chunk`.** Rejected in favor of deferring ancestry to the
  graph (see above).
