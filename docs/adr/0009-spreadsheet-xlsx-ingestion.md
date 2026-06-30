# ADR 0009: Spreadsheet (XLSX) ingestion — faithful extraction in the library, semantics in the app (deferred)

- Status: Accepted (deferred — no consumer ask yet; ~90% expected)
- Date: 2026-06-29

## Context

Spreadsheets are semi-structured documents, not rows of prose. Two naive
approaches both fail: ingesting every cell as text produces garbage embeddings,
and discarding spreadsheets entirely loses genuinely high-signal content — sheet
names, table/column headers, row labels, named ranges, Excel tables, and
comments/notes.

Good retrieval practice for spreadsheets is well understood: capture workbook
structure; identify semantic regions (titles, headers, tables, comments);
preserve coordinates/context (workbook/sheet/range/headers/row-labels); summarize
tables rather than embedding all values; keep raw cell data addressable for
follow-up; and model spreadsheet chunks with their coordinates and region kind.

But much of that practice is **application and retrieval policy**, not extraction:

- Generating a *semantic* table summary ("Enterprise ARR assumptions by region")
  needs a model call. `extract` is deterministic and provider-neutral; it makes no
  model calls.
- Classifying a region as a "title block" or "section" is heuristic policy.
- A richly-typed spreadsheet-region chunk schema (region kind, headers[],
  row_labels[]) is domain schema. Apps own persistence and their own schema; the
  library carries values (spec §3 Core Design Principles 2–3).

This is the same line already drawn for Morris's `internal/classify`: clean,
reusable code that nonetheless encodes policy, so it stays in the app (spec §7).

No consumer has asked for XLSX yet. It is recorded now (while adjacent formats —
PPTX — were being added) so that if/when it is built, the boundary is already
decided and the neutral core is not eroded by spreadsheet *intelligence*.

## Decision

Defer building `extract/xlsx` until a consumer needs it. When built, hold this
boundary:

**In the library (`extract/xlsx`, an opt-in subpackage per ADR 0006):**

- Deterministic, faithful extraction only: workbook/sheet structure (including
  hidden sheets), Excel tables, named ranges, comments/notes.
- Per-sheet (or per-table) text artifacts: extract returns `[]content.Artifact`,
  and a source fans out to multiple derived artifacts (ADR 0003) — a workbook
  does so naturally.
- Faithful textual rendering of populated regions (headers + rows), skipping empty
  areas. No semantic guessing.
- Coordinates/context (`workbook`, `sheet`, `range`, `headers`) carried in
  `content.Artifact.Metadata`; raw bytes remain addressable via the `store` handle
  for linkback.
- Optionally a *structural* (not semantic) summary — e.g. "Sheet 'Revenue Model':
  table B4:G22, columns [Region, Segment, Current ARR, …], 18 rows" — which is
  mechanical.
- Optionally a spreadsheet-aware `chunk.Boundaries` (segment by table/sheet),
  analogous to `chunk.Headings`.

**In the app (Morris/Cooper):**

- LLM/semantic table summaries.
- Semantic-region classification (title/section/free-text heuristics).
- The embedding strategy (which of headers/labels/summary/raw to embed vs store).
- Any richly-typed spreadsheet-region chunk schema.

**Dependency:** robust XLSX parsing likely needs `github.com/xuri/excelize`
(heavy: shared-strings table, styles, cell typing, formulas). It would be confined
to the `extract/xlsx` subpackage (ADR 0006), like `dslipak/pdf` in `extract/pdf`.
A minimal stdlib parser is possible but fiddly; the dependency choice is made when
the extractor is built.

## Consequences

- No code or dependency is added speculatively; the in-scope/out-of-scope line is
  recorded so a future build does not drag provider calls or domain policy into
  core.
- The existing `extract` contract (`[]content.Artifact` + `Metadata` map) already
  accommodates the in-scope parts with no model change.

## Alternatives considered

- **Implement the full ingestion pipeline in the library** (semantic summaries +
  typed region chunks) — rejected: pulls model calls, heuristic policy, and domain
  schema into the neutral, provider-neutral core.
- **Never support XLSX** — rejected: discards real signal, and a consumer need is
  ~90% expected.
