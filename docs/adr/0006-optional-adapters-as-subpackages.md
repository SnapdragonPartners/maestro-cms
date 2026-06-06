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
  `extract/html`, `extract/pdf`, `extract/docx`, `extract/markdown`.
- Root packages stay dependency-free: `store` is the byte interface only;
  `extract` holds the `Extractor` interface, a registry, and the genuinely
  stdlib-only `text/plain` extractor, while every other format is an opt-in
  subpackage. Markdown is kept out of core despite being stdlib-parseable:
  its whitespace is semantic, so `extract/markdown` preserves the body verbatim
  rather than applying the prose normalization the plain-text path uses (see
  ADR 0008). HTML belongs in a subpackage too: it needs
  `golang.org/x/net/html`, which is not the standard library. (DOCX and Markdown
  are stdlib-only — DOCX via `archive/zip` + `encoding/xml` — but stay
  subpackages anyway, so all formats are uniformly opt-in.)
- A consumer assembles what it needs (for example, registering `extract/pdf` into
  the registry). A convenience "default bundle" may come later.
- A golangci-lint depguard `core-no-adapters` rule forbids core packages
  (`content`, `chunk`, root `extract`, root `store`) from importing adapter
  subpackages (`extract/html|pdf|docx|markdown`, `store/gcs`, `index/*`). It
  landed with the first adapter (`extract/html`).

## Consequences

- `import store` / `import extract` pull only the standard library.
- Dependency growth is opt-in and auditable.
- Slightly more wiring for consumers: they register the formats/backends they
  use. This is the intended trade.
