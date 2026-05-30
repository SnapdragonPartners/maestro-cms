# ADR 0006: Optional adapters live in subpackages; core stays dependency-free

- Status: Accepted
- Date: 2026-05-30

## Context

Core packages must stay provider- and storage-neutral (spec §3, §5). Some
capabilities need heavy third-party dependencies: GCS (a cloud SDK), PDF
(`dslipak/pdf`), DOCX (format-specific parsing), pgvector (a DB driver), and so
on. We do not want `import store` or `import extract` to drag every cloud or
format dependency into every consumer — that violates the module's dependency
discipline.

## Decision

- Adapters live in **subpackages of the same module**, not separate modules:
  `store/gcs`, `index/pgvector`, `index/sqlitefts`, and per-format extractors
  `extract/pdf`, `extract/docx`.
- Root packages stay dependency-free: `store` is the byte interface only;
  `extract` (when it lands) holds the `Extractor` interface, a registry, and the
  stdlib-only text/HTML extractors, while heavy formats are opt-in subpackages.
- A consumer assembles what it needs (for example, registering `extract/pdf` into
  the registry). A convenience "default bundle" may come later.
- When the first adapter lands, add the golangci-lint depguard rule that forbids
  core packages from importing adapter subpackages (tracked in
  [docs/deferred-tooling.md](../deferred-tooling.md)).

## Consequences

- `import store` / `import extract` pull only the standard library.
- Dependency growth is opt-in and auditable.
- Slightly more wiring for consumers: they register the formats/backends they
  use. This is the intended trade.
