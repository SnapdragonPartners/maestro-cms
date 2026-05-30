# Maestro CMS Spec v1

Status: Draft (revised 2026-05-30 after grounding the original draft against the
`maestro`, `maestro-llms`, `morris`, and `cooper` repositories)

The load-bearing decisions in this spec are recorded as ADRs under
[`docs/adr/`](adr/README.md) and are referenced inline. Read those for the full
rationale.

## 1. Purpose

Maestro CMS is a shared Go library for document, media, and knowledge engines.

It should extract reusable content primitives from Morris and Maestro, support
Cooper as a new product application, and remain open-source in the same spirit as
`github.com/SnapdragonPartners/maestro-llms`.

The goal is explicitly to **get ahead of duplication**. Embedding contracts have
already been re-implemented independently in Morris; tokenization exists in two
places; content modeling is about to be re-derived per app. This library defines
the boundaries proactively so the apps can be refactored onto it, rather than
codifying the apps as they happen to be today.

## 2. Initial Consumers

A note on maturity, because it shapes how much weight each repo's current code
should carry:

- **`maestro-llms`** is the only stable sibling — freshly extracted and tested. We
  build on it and extend it. (See §8 for a work request we are sending its way.)
- **`maestro`** is a battle-tested app, but its `knowledge`/graph component is its
  least mature and optional part. It is a source of ideas, not proven content code.
- **`morris`** is early in development. Its extraction/chunking/storage code is
  clean and reusable, but its chunker is not yet wired into production, and its
  embedding interface is duplicative (see ADR 0001).
- **`cooper`** is a concept: a README, no code. It cannot yet validate any boundary;
  it informs requirements, not interfaces.

### Morris

High-security document ingestion and retrieval for family offices.

Reusable pieces (verified):

- MIME-aware text extraction (`internal/extract`) — clean, zero Morris coupling.
- A prose chunker (`internal/chunk`) — clean, but **not yet used in production**;
  treat its API as unvalidated.
- An object-store interface over GCS (`internal/storage`) — clean; path
  conventions live in the ingest worker, not the interface.
- An embedding abstraction over `maestro-llms` (`internal/embeddings`) — a
  near-clone of the `maestro-llms` contract plus an adapter. This is the
  duplication we are removing (ADR 0001).

Morris concerns that stay in Morris: family-office group ACLs, admin/system
privilege separation, handling classification, admin confirmation before active
revision swap, audit/citation retention, per-client deployment, the ingestion
worker, and the document/revision schema.

### Cooper

A lightweight multi-tenant CMS with an integrated chatbot. Currently a concept.

The one strong signal in its README: the first content model is a **sports
league/team** shape, generalizing later — *not* a generic tenant/collection
abstraction. So v1 must not bake generic-tenant assumptions into the core.

Cooper-shaped requirements it will eventually impose (all app-owned): tenant
hierarchy, subscription/billing, access models (public/free/member/paid),
content grouping (collections, bundles, teams, seasons), custom-domain
publishing, and product analytics.

### Maestro

Has a project knowledge-graph system: DOT parsing, schema validation, SQLite FTS5
indexing/retrieval, and story-scoped knowledge packs (`pkg/knowledge`, zero
coupling to Maestro internals; `.maestro/knowledge.dot` is optional). It also has
a tiktoken-based token counter (`pkg/utils/tiktoken.go`).

Maestro's graph informs the v2 graph primitive (ADR 0005), not v1. Its token
counter is superseded by the estimator that already exists in `maestro-llms`
(`middleware.TokenEstimator`; ADR 0002), not a `maestro-cms` package. Maestro concerns that stay in Maestro: agent state machines,
story/session lifecycle, workspace layout, toolloop effects, the MCP server, and
the architecture-knowledge ontology itself.

## 3. Core Design Principles

1. The core library is product-neutral and storage-neutral.
2. Applications own authorization, tenancy, subscriptions, audit policy, grouping
   taxonomy, and persistence/schema. The library carries values; apps persist them.
3. **No domain persistence in the library.** No DB schemas, and no tenant,
   content, revision, or job state — each app owns those (Morris on Postgres/sqlc,
   Maestro on SQLite, Cooper TBD). The library returns values; apps persist them.
   The `store` object-store adapters are *not* an exception to this: they are byte
   primitives that read/write opaque bytes (raw sources, derived blobs) behind a
   narrow `Get/Put/Delete/Exists` interface, with no schema or domain knowledge.
4. Model only what we need now, but as a **subset that later work extends, not a
   rewrite**. Multi-modality and the future graph must be reachable additively.
5. "Document" is not the top-level abstraction. "Content" is, so non-text media
   fits naturally (ADR 0003).
6. Interoperate with `maestro-llms` rather than duplicate provider work; consume
   its contracts directly (ADR 0001, ADR 0002).
7. Prefer small interfaces, options, and adapters over widening central types.
8. Fakes are first-class: shared behavior ships with deterministic fakes (e.g. a
   fake embedder) so consumers test offline.

## 4. Core Concepts

### Source and Artifact (the content model)

See ADR 0003. The content model is a **single-parent provenance tree**:

- **Source** — the original: stable opaque ID, media type, content hash, a `store`
  handle for raw bytes.
- **Artifact** — a derived thing: stable opaque ID, media type, a single-parent
  `DerivedFrom` link to a Source or another Artifact, and a payload (inline text
  or a `store` handle).

A PDF Source fans out to a text Artifact today and may fan out to page-image and
OCR-text Artifacts later — same type, no remodel. A neutral `map[string]string`
is the only label carrier; grouping/tag *semantics* are app policy.

### Media type

A first-class field on Source and Artifact from day one. This is the hinge that
keeps the model from collapsing back into "content == text".

### Retrieval handle (deferred to v1.x)

Retrieval results should expose opaque handles rather than raw storage paths, so
apps resolve UI links, permissions, and source details outside the model context.
There is no proven implementation yet (Morris's retrieval is a 37-line stub that
bakes in group ACLs), so the handle/citation contracts are deferred — see §6.

### Provisional type sketches

Illustrative, **not binding** — they exist to keep independent implementers from
diverging on shape. Field names, exact types, and packaging are decided in Phase 1
(see open questions §10). Go-ish pseudocode:

```go
// content
type MediaType string // IANA type, e.g. "application/pdf", "text/plain", "image/png"

// StoreHandle is an opaque reference to bytes in a store. The library never
// interprets Key; only the matching store adapter resolves it.
type StoreHandle struct {
    Backend string // adapter id, e.g. "gcs", "fs"
    Key     string // adapter-defined locator
}

// Source is the original content an application knows about.
type Source struct {
    ID        string            // stable, opaque, app-assigned
    MediaType MediaType
    Hash      string            // content hash of the raw bytes
    Raw       StoreHandle       // where the raw bytes live
    Metadata  map[string]string // neutral carrier; no grouping semantics
}

// Artifact is something derived from a Source or another Artifact (single parent).
type Artifact struct {
    ID          string
    MediaType   MediaType
    DerivedFrom string            // ID of the single parent Source or Artifact
    Text        string            // set for textual artifacts
    Blob        *StoreHandle      // set instead of Text for binary artifacts
    Metadata    map[string]string
}
```

```go
// chunk — pure; estimator injected, defaults to char/4 (ADR 0002)
type Chunk struct {
    Text       string
    Index      int // 0-based position within the source artifact
    StartByte  int // offset into the artifact text (inclusive)
    EndByte    int // exclusive
    TokenCount int // from the injected estimator
}

type Config struct {
    MaxTokens     int
    OverlapTokens int
}

type Estimate func(string) int // default: local char/N; later, a maestro-llms text helper

func Split(text string, cfg Config, est Estimate) []Chunk
```

```go
// embed — runner over llms.EmbeddingClient (ADR 0001, ADR 0004)
// Record is one chunk plus its vector, ready for the app to persist.
type Record struct {
    SourceID   string
    ArtifactID string
    ChunkIndex int
    Text       string
    Vector     []float32
    Model      string // from llms.ModelRef
    TokenCount int
}

// RunResult preserves successful records and reports failures per batch.
type RunResult struct {
    Records  []Record
    Failures []BatchFailure
}

type BatchFailure struct {
    ChunkIndexes []int
    Err          error
    Retryable    bool
}
```

## 5. Package Boundaries

| Package    | In v1? | Shape |
|------------|:------:|-------|
| `extract`  | ✅ | MIME-aware extraction. Returns `[]Artifact` (multi-modal; text-only populated today). Lifted from Morris. |
| `chunk`    | ✅ | **Pure**: `text → []Chunk`. Injected `func(string) int` estimator, char/4 default. Lifted from Morris; API unvalidated, expect revision. |
| `content`  | ✅ | `Source` + `Artifact` + media type + single-parent provenance + stable IDs + optional neutral metadata map. New, minimal code. |
| `embed`    | ✅ | **Runner**, not a contract: batches `[]Chunk`, preserves successful batches and reports per-batch failure diagnostics, with ID-order matching / retry / budget-aware batch sizing over `llms.EmbeddingClient`; returns persist-ready records. Optional `Pipeline` chains extract→chunk→embed. (Failure semantics: ADR 0004.) |
| `store`    | ✅ | `Get/Put/Delete/Exists(key)` object-store interface — opaque, adapter-defined keys, **no path conventions** — plus optional GCS adapter. Clean lift from Morris. A `content.StoreHandle{Backend, Key}` names which adapter resolves a given key. |
| `testcms`  | ✅ | Deterministic fakes, including a fake embedder. |
| `retrieval`| v1.x | Search request/response, context-window, source-handle, citation contracts. Deferred until a consumer is ready (§6). |
| `graph`    | v2 | Generic directed-graph primitive with caller-defined schema (ADR 0005). |
| `index/*`  | later | Optional adapters (`pgvector`, `sqlitefts`, `alloydb`) once a consumer adopts. |

Removed from the original draft:

- **`tokens`** — moved to `maestro-llms` (ADR 0002).
- **`embed` as a contract package** — redefined as a runner; the contract is
  `llms.EmbeddingClient` (ADR 0001, ADR 0004).

Core packages stay provider- and storage-neutral; only optional adapters
(`store/gcs`, `index/*`) may import cloud SDKs or DB drivers.

## 6. Sequencing (ordered by confidence, not just risk)

### Phase 1 — High-confidence content pipeline

The pieces that are clean, real, and have a consumer (Morris) ready to adopt:

- Repo scaffolding mirrored from `maestro-llms`: `go.mod`
  (`github.com/SnapdragonPartners/maestro-cms`, matching Go version), Makefile
  (`build`/`test`/`lint`/`fix`/`tidy`), the strict `.golangci.yaml` (including the
  depguard core-cannot-import-adapters rule), CI (build-lint-test + weekly
  `govulncheck`), and pre-push hooks.
- `extract`, `chunk` (pure), `content` (minimal), `store`, `embed` runner,
  `testcms` fakes.
- Refactor Morris to consume these back, and to drop its duplicate embedding
  interface (pending the team-Morris review in §8).

### Phase 2 — Retrieval contracts

`retrieval` handle, citation, search request/response, and context-window types,
designed once a Phase-1 consumer is persisting embedded records and a second
consumer's needs are concrete. Do not copy Morris's group-ACL-shaped stub.

### Phase 3 (v2) — Graph primitive

Generic directed graph with caller-defined node/edge schema (ADR 0005). v1
content already carries the stable IDs it will index.

### Phase 4 — Index adapters

`index/pgvector` and `index/sqlitefts` once at least one app is ready to consume
them. Cooper signals pgvector + GCS as the likely first targets.

### Phase 5 — Media artifacts

OCR, audio/video transcription, keyframes — populated as new `Artifact` variants
the `extract` return type already admits. No content remodel required.

## 7. What Not To Extract

Stays in the apps (may inspire interfaces, never becomes core):

- Morris: ingestion worker, document/revision schema, handling classification,
  audit repository, group ACLs.
- Cooper: tenant/subscription/grouping model.
- Maestro: agent/session/story persistence, MCP server, architecture-knowledge
  ontology.

Note on Morris's `internal/classify`: it is a clean, dependency-free rule engine,
but it encodes handling *policy*. It stays in Morris by design — flagged here so
its omission is deliberate, not an oversight.

## 8. Cross-Repo Work Requests

- **maestro-llms** — *no blocking work for estimation.* A
  `middleware.TokenEstimator` already exists, but it is request-shaped
  (`EstimateChat` / `EstimateEmbeddings` → `ratelimit.UsageUnits`, for the rate
  limiter) with an unexported char-based core — there is no `text → int` helper to
  reuse yet. So `maestro-cms` `chunk` consumes an injected `func(string) int`
  defaulting to a local char/N estimate (ADR 0002). The small, non-blocking
  follow-up is to have maestro-llms export a `text → int` helper (or a
  tokenizer-backed one) for fidelity-consistent counts. This was handed to the
  maestro-llms team for their next release (tracked in that repo).
- **Morris** — review the bespoke embedding interface + Vertex adapter and either
  justify the duplication or replace it with a thin use of `llms` plus the
  `maestro-cms` embed runner (ADR 0001).

## 9. Recorded Decisions

See [`docs/adr/`](adr/README.md):

- ADR 0001 — Consume the maestro-llms embedding interface directly.
- ADR 0002 — Token estimation belongs in maestro-llms; no `tokens` package here.
- ADR 0003 — Content is a single-parent provenance tree of Source + Artifacts.
- ADR 0004 — `embed` is an orchestration runner over a pure `chunk`.
- ADR 0005 — Defer the graph engine to v2; v1 content needs only stable IDs.
- ADR 0006 — Optional adapters live in subpackages; core stays dependency-free.

## 10. Resolved Phase 1 Contracts

Decisions taken before cutting Phase 1 code, recorded so they are not
re-litigated:

- **Adapter isolation** (was open Q1/Q2) — optional adapters live in
  subpackages and the core stays dependency-free: `store/gcs`, and per-format
  `extract/pdf` / `extract/docx`. `import store` / `import extract` pull only
  stdlib. See ADR 0006.
- **IDs** — `Source`/`Artifact` IDs are app-assigned and opaque. The library
  validates their presence (`Validate`) but never mints them.
- **Content hash** — the caller owns `Source.Hash`. `extract` never computes or
  mutates source identity; a `content.HashBytes` helper may be added later.
- **`extract` signature** — media type is an explicit input. The shape will
  shake out in code, roughly `Extract(ctx, mediaType, reader, parentID)
  -> ([]Artifact, error)` plus a registry.
- **First migration target** — Morris extraction + storage first, then chunk +
  embed, for the fastest real feedback.

## 11. Still Open

1. **`embed` record shape** — Phase 1 records carry source ID, artifact ID,
   chunk index, text, offsets, token count, vector, model ref, and a metadata
   hook; the exact final field set is pinned when `embed` is implemented.
2. **Chunker API** — Morris's chunker is unvalidated; expect to revise its
   `Config`/boundary-selection surface as the first real end-to-end pipeline
   exercises it. Keep the first API boring and pure.
