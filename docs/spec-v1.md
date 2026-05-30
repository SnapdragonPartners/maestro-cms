# Maestro CMS Spec v1

Status: Draft

## 1. Purpose

Maestro CMS is a shared Go library for document, media, and knowledge engines.

It should extract reusable content and knowledge primitives from Morris and Maestro, support Cooper as a new product application, and remain open-source in the same spirit as `github.com/SnapdragonPartners/maestro-llms`.

The library should make it easier to build applications that ingest content, derive text or other artifacts, chunk and embed material, index it, retrieve relevant context, and expose that context to chat, MCP tools, or application UI.

## 2. Initial Consumers

### Morris

Morris needs high-security document ingestion and retrieval for family offices.

Relevant current pieces:

- MIME-aware text extraction for PDF, DOCX, HTML, Markdown, and plain text.
- Text chunking for embeddings.
- Object storage abstraction over GCS.
- Embedding abstraction over `maestro-llms`.
- Planned pgvector retrieval over document chunks.

Morris-specific concerns that should not move into the shared core:

- Family-office group ACLs.
- Admin/system privilege separation.
- LLM handling classification.
- Admin confirmation before active revision swap.
- Audit requirements and citation retention policy.
- Per-client deployment assumptions.

### Cooper

Cooper is a lightweight multi-tenant CMS product with an integrated chatbot.

It needs:

- Tenant-scoped content ingestion.
- Public, free-account, member-only, and paid-subscriber access models.
- Content collections, bundles, teams, exhibits, or other tenant-specific grouping.
- Chat over whole tenant corpora, content bundles, or individual items.
- Postgres/pgvector or AlloyDB plus GCS-compatible object storage.
- Future media support for images, audio, and video.

Cooper-specific concerns that should not move into the shared core:

- Tenant hierarchy.
- Subscription and billing decisions.
- Custom-domain publishing.
- Public website and app UI.
- Product analytics and tenant administration.

### Maestro

Maestro currently has a project knowledge graph system.

Relevant current pieces:

- `.maestro/knowledge.dot` as a repo-backed knowledge graph.
- DOT parsing into nodes and edges.
- Schema validation for node and edge attributes.
- SQLite FTS5 indexing and retrieval.
- Story-specific knowledge packs.
- MCP tooling that can expose agent tools.

Maestro also creates pressure for future code-aware chunking and retrieval:

- Code-aware chunkers.
- AST or tree-sitter-backed parsing.
- Retrieval over repository files, architectural patterns, and prior decisions.
- MCP tools over knowledge retrieval.

Maestro-specific concerns that should not move into the shared core:

- Agent state machines.
- Story/session lifecycle.
- Workspace layout.
- Toolloop process effects.
- Claude Code MCP proxy behavior.

## 3. Core Design Principles

1. The core library should be product-neutral.
2. Applications own authorization, tenancy, subscriptions, and audit policy.
3. Storage adapters should be optional and replaceable.
4. Content sources and derived artifacts should be modeled separately.
5. "Document" should not be the top-level abstraction. Use "content" so media can fit naturally.
6. The library should interoperate with `maestro-llms` rather than duplicate provider work.
7. Interfaces should be small and useful to at least two expected consumers.
8. Extract code only when the boundary is clear enough to survive multiple clients.

## 4. Proposed Core Concepts

### Content Source

A content source is the original thing the application knows about.

Examples:

- Uploaded PDF.
- DOCX file.
- HTML page.
- Markdown file.
- External link.
- Image.
- Audio recording.
- Video.
- Code file.
- Knowledge graph file.

The shared library should avoid assuming every source is a document or every source has text.

### Content Version

A version represents one processing snapshot of a source.

Morris needs revisions for citation stability, classification safety, and rollback. Cooper may not need visible revision history for MVP, but a version concept is still valuable for reprocessing, rollback, and provenance. Maestro may use versions for repo-backed knowledge or indexed workspace state.

The shared library should define version/provenance primitives, but applications decide whether and how to persist them.

### Artifact

An artifact is derived from a source or version.

Examples:

- Extracted text.
- Chunks.
- Embeddings.
- OCR text.
- Audio transcript.
- Video transcript.
- Keyframes.
- Thumbnails.
- Knowledge graph subgraph.
- Search index records.

Artifacts should carry enough metadata for downstream indexing and provenance without encoding product-specific policy.

### Retrieval Handle

Retrieval results should expose opaque handles rather than raw storage paths by default.

Morris needs this for least-context and security reasons. Cooper and Maestro also benefit because a handle lets the application resolve UI links, permissions, and source details outside the LLM context.

## 5. Candidate Package Boundaries

### `extract`

Reusable extraction interfaces and implementations.

Initial sources from Morris:

- `internal/extract/extract.go`
- `internal/extract/text.go`
- `internal/extract/html.go`
- `internal/extract/pdf.go`
- `internal/extract/docx.go`

Likely API shape:

```go
type Extractor interface {
    Extract(ctx context.Context, r io.Reader) (Extracted, error)
}

type Extracted struct {
    Text string
    Hash string
    Bytes int
    Metadata map[string]string
}
```

Open design questions:

- Should extraction return multiple artifacts instead of one text field?
- Should media extraction live in this package or a parallel `media` package?
- Should MIME matching and normalization be part of the registry?

### `chunk`

Chunking interfaces and implementations.

Initial source from Morris:

- `internal/chunk/chunk.go`

Future Maestro-driven additions:

- Code-aware chunkers.
- Language-aware chunkers.
- AST or tree-sitter chunkers.
- Markdown section chunkers.

Likely API shape:

```go
type Chunker interface {
    Chunk(ctx context.Context, input ChunkInput) ([]Chunk, error)
}

type ChunkInput struct {
    Text string
    MediaType string
    SourceName string
    Metadata map[string]string
}
```

The Morris chunker can become the default prose chunker.

### `tokens`

Token counting and budget-aware text helpers.

Initial source from Maestro:

- `pkg/utils/tiktoken.go`

Initial source from Morris:

- Character-based token estimation embedded in `internal/chunk`.

This package should support both:

- Fast approximate counting.
- Optional tokenizer-backed counting.

### `embed`

Embedding contracts used by content workflows.

Initial source from Morris:

- `internal/embeddings/embeddings.go`

This should likely remain a small content-facing interface that can be backed by `maestro-llms`.

Open design question:

- Should this package define its own interface, or should `maestro-llms` expose the canonical embedding interface and Maestro CMS consume it?

### `content`

Core source/version/artifact/provenance types.

This should be new code rather than directly copied from Morris or Maestro.

It should avoid application-specific fields like `owner_user_id`, `classification_status`, `session_id`, or `story_id`.

### `retrieval`

Storage-neutral retrieval contracts.

Potential responsibilities:

- Search request/response types.
- Context-window retrieval.
- Related-content query contracts.
- Source handle and citation types.
- Hybrid vector/lexical result fields.

Morris has an early `internal/retrieval` scaffold, but it is too small and Morris-shaped to copy directly without redesign.

### `graph`

Structured knowledge graph primitives.

Initial source from Maestro:

- `pkg/knowledge/parser.go`
- `pkg/knowledge/validator.go`
- `pkg/knowledge/search.go`

Potential responsibilities:

- Graph representation.
- DOT parser and renderer.
- Validation.
- Subgraph extraction.
- Search term extraction.

Storage-backed FTS retrieval should probably be an adapter, not core graph logic.

### `store`

Object-store interfaces and optional adapters.

Initial source from Morris:

- `internal/storage/storage.go`
- optionally `internal/storage/gcs.go`

The core interface should not document Morris-specific object path conventions.

### `index`

Optional indexing adapters.

Likely subpackages:

- `index/postgrespgvector`
- `index/sqlitefts`
- `index/alloydb`

These should be optional. The core package should not require every consumer to pull in Postgres, SQLite, GCS, tree-sitter, and PDF dependencies unless needed.

## 6. What To Extract First

### Phase 1: Low-Risk Shared Primitives

Extract:

- Morris extraction package.
- Morris prose chunker.
- Maestro token counting helpers.
- Morris embedding contracts, after deciding relationship to `maestro-llms`.
- Morris object-store interface, cleaned of Morris-specific path docs.

Deliverables:

- New Go module `github.com/SnapdragonPartners/maestro-cms`.
- Basic lint/test workflow copied from established Snapdragon repos.
- Unit tests ported with the extracted packages.
- Morris updated to consume the new library for extraction/chunking if feasible.

### Phase 2: Content Model And Retrieval Contracts

Add:

- Source/version/artifact model.
- Retrieval handle and citation contracts.
- Context-window result types.
- Event sink interface.

Do not yet move Morris's worker or schema.

### Phase 3: Graph Knowledge

Evaluate moving Maestro graph primitives:

- DOT parser.
- Validator.
- Subgraph logic.
- Search term extraction.

Keep SQLite FTS as an adapter.

### Phase 4: Indexing Adapters

Add storage-backed retrieval adapters once at least one application is ready to consume them.

Candidate adapters:

- Postgres/pgvector for Morris and Cooper.
- SQLite FTS for Maestro.
- AlloyDB if Cooper or Morris needs it.

### Phase 5: Media

Add first media-derived artifact support.

Likely order:

1. OCR for image/PDF images.
2. Audio transcription.
3. Video transcription plus keyframes.
4. Image embeddings or multimodal indexing.

## 7. What Not To Extract Yet

Do not extract:

- Morris ingestion worker as-is.
- Morris document/revision schema as-is.
- Morris classification policy.
- Morris audit repository.
- Cooper tenant/subscription model.
- Maestro agent/session/story persistence.
- Maestro MCP server implementation.

These may inspire interfaces, but should not become core library code.

## 8. Event And Audit Strategy

The shared library should define an event sink, not an audit system.

Example:

```go
type EventSink interface {
    Emit(ctx context.Context, event Event) error
}
```

Applications can:

- Persist events.
- Log them.
- Drop them.
- Feature-flag them.
- Translate them into application audit records.

This lets Morris keep strict audit semantics while Cooper can start lightweight.

## 9. Revision And Version Strategy

The shared library should model versions, but not enforce Morris's active-revision policy.

Morris can require:

- Extracted.
- Classified.
- Embedded.
- Indexed.
- Admin-confirmed.
- Active pointer swapped.

Cooper can initially require:

- Extracted.
- Embedded.
- Indexed.
- Published.

Maestro can use versions differently for repo knowledge snapshots.

## 10. Open Questions

1. Should `maestro-cms` expose embedding interfaces directly, or should it depend on a canonical `maestro-llms` embedding interface?
2. Should GCS live in the core module or an optional adapter subpackage?
3. Should PDF/DOCX extractors live in core despite adding dependencies, or should each format be an optional subpackage?
4. What is the first Cooper content model: league/team/user, or generic tenant/collection/member?
5. Should graph knowledge use DOT as a core supported format or as a Maestro-specific adapter?
6. Which app should be the first consumer migrated to `maestro-cms`: Morris or Cooper?
7. How aggressively should v1 preserve Morris extraction APIs versus redesigning for multi-artifact media?

