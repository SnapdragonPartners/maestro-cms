# ADR 0003: Content is a single-parent provenance tree of Source + Artifacts

- Status: Accepted
- Date: 2026-05-30

## Context

Morris modeled content as a 1:1 source→text relationship and dove straight into
textual documents. We want to avoid baking "content == text document" into the
foundation — but without over-modeling.

Real content is multi-modal and derivation is recursive: a PDF yields page images
yields OCR text yields chunks yields embeddings; an image yields metadata-as-text
plus OCR text. Go has no inheritance, so a single "superclass" god-struct is just
the opposite over-modeling failure.

## Decision

Model two minimal types:

- **Source** — the original thing: stable opaque ID, media type, content hash, and
  a `store` handle for raw bytes.
- **Artifact** — a derived thing: stable opaque ID, media type, a **single-parent**
  `DerivedFrom` link to a Source or another Artifact, and a payload (inline text or
  a `store` handle for binary).

Additional calls:

- Single-parent provenance ⇒ the derivation is a **tree**, not a DAG. Revisit only
  when a concrete multi-parent use case appears; none is obvious today.
- `extract` returns `[]Artifact`; today only a text artifact is populated. Media
  artifacts (OCR, transcripts, keyframes) are future variants the type already
  admits — additive, not a rewrite.
- A neutral `map[string]string` is the only label carrier. Grouping/tag *taxonomy
  semantics* (collections, bundles, seasons, classification) are app policy and
  stay out.
- No relationships/edges, no tags-as-a-feature, no persistence in the library.
  Forward-compatibility to the v2 graph
  ([ADR 0005](0005-defer-graph-engine-to-v2.md)) reduces to stable opaque IDs.

## Consequences

- Multi-modality is in the foundation from day one at near-zero cost; media work
  is additive.
- The library carries content as values; every app persists with its own schema.
- "Everything is Content/Artifact, text or not" — without inheritance or a
  god-struct.
