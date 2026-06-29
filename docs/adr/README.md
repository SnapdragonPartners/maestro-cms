# Architecture Decision Records

Load-bearing decisions for `maestro-cms`. Do not "fix" a deliberate limitation
described in an ADR without superseding it.

- [0001](0001-consume-maestro-llms-embedding-interface.md) — Consume the
  maestro-llms embedding interface directly; do not define our own.
- [0002](0002-token-estimation-belongs-in-maestro-llms.md) — Token estimation
  lives in maestro-llms; no `tokens` package here.
- [0003](0003-content-as-single-parent-provenance-tree.md) — Content is a
  single-parent provenance tree of Source + Artifacts.
- [0004](0004-embed-is-a-runner-over-a-pure-chunker.md) — `embed` is an
  orchestration runner over a pure `chunk`.
- [0005](0005-defer-graph-engine-to-v2.md) — Defer the graph engine to v2; v1
  content needs only stable IDs.
- [0006](0006-optional-adapters-as-subpackages.md) — Optional adapters live in
  subpackages; core stays dependency-free.
- [0007](0007-pdf-extraction-watchdog-stopgap.md) — PDF extraction uses a timeout
  watchdog as a stopgap for a hanging parser (supersede after the spike).
- [0008](0008-markdown-verbatim-and-heading-chunking.md) — Markdown is extracted
  verbatim and chunked by heading (ancestry deferred to the graph).
- [0009](0009-spreadsheet-xlsx-ingestion.md) — Spreadsheet (XLSX) ingestion:
  faithful extraction in the library, semantics in the app (deferred).
