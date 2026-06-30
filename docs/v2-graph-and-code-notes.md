# v2 design notes: graph primitive + code-aware retrieval

Status: **Exploratory notes, not decisions.** Captures the design discussion for
the eventual v2 work (spec §6 Phase 3; [ADR 0005](adr/0005-defer-graph-engine-to-v2.md)).
Each decision here becomes an ADR when we commit to it. Important but not urgent.

## The core insight: three layers, and AST ≠ graph

"Ingest DOT/DIAG" and "extract ASTs for RAG" feel related but live in different
layers. Keeping them separate is what keeps the design clean.

| Layer | Job | Consumers | Status |
|---|---|---|---|
| `extract` | bytes → text artifacts | all formats | shipped |
| `chunk` (+ code-aware `Boundaries`) | structure-aware chunking + symbol metadata | tree-sitter code RAG, Markdown | seam exists; code segmenter additive |
| `graph` (v2) | generic directed graph, caller-defined schema | DOT/DIAG knowledge, code call/import graphs, family tree, content tags | to scope |

- **AST-for-RAG is a chunk-layer concern**, not a graph: tree-sitter is used to
  split code at function/class boundaries and attach symbol metadata; you embed
  *chunks*, not the tree. It plugs into the existing `chunk.Boundaries` seam
  (`func(text) []Unit`), exactly like `chunk.Headings`.
- **Only code *relationship* graphs** (call/import/reference) feed the graph
  primitive — a later, separate use. So tree-sitter spans both layers over time
  but starts at chunk.

## Graph primitive — design leanings

- **Schema-as-data** (the load-bearing choice). The caller passes a schema
  *value* (node kinds, edge kinds, which kinds may connect) and the engine
  validates against it. This generalizes Maestro `pkg/knowledge`'s hardcoded
  SQLite CHECK constraints without baking in its ontology. Storage is untyped
  (string ids/kinds + attribute maps). Avoid Go generics for the core (too rigid
  for DOT's open attributes); a typed wrapper can sit on top later.
- **Pure core, adapters for the rest.** Core = model + schema validation +
  traversal/subgraph, in-memory and deterministic (testable with fakes). SQLite
  **FTS5** and any persistence are *optional adapters* (`graph/sqlite` or under
  `index/*`), mirroring `store/gcs` and the planned `index/pgvector`. "Optional
  FTS" in ADR 0005 = adapter, not core.
- **Query API driven by the real need, not a query language.** The high-value
  operation (from Maestro's story-scoped knowledge packs) is **subgraph
  extraction**: seed node(s) + depth/kind filter → connected subgraph. Plus basic
  neighbors/paths. Family-tree and code consumers want the same shape. Resist a
  Cypher-lite.
- **Decoupled from `content`.** Graph nodes *may* reference content IDs (the
  stable opaque IDs v1 already carries, ADR 0003/0005), but the graph is a
  standalone primitive; `content` never imports `graph`.
- **Graph × vector retrieval is a Phase-2 composition**, not a graph feature
  (expanding a vector hit to its neighbors belongs in the deferred `retrieval`
  layer). Keep graph a primitive.
- **Ontology stays in the app.** The engine + schema *mechanism* is library;
  Maestro's specific architecture node/edge types + rules are a schema *value*
  Maestro supplies — same line as classify/audit/tenancy (spec §7).

## Tree-sitter / code-aware chunking

- **Near-term:** a code-aware `chunk.Boundaries` (opt-in subpackage; chunk core
  stays pure per ADR 0004) that segments at AST boundaries and emits symbol
  metadata: `{symbol, signature, enclosing type, file:span}`.
- **Design for continuity:** give symbols stable identity now so they can *become
  graph nodes later* without rework — mirroring how v1 content carries stable IDs
  for the future graph (the "additive, not rewrite" principle, spec §4).
- **Later:** code call/import/reference graphs become a graph consumer (a 4th
  ontology — architecture, family-tree, tags, code — which reinforces the
  schema-as-data requirement).

## CGO implications (tree-sitter, and PDF — see below)

tree-sitter is C with per-language C grammars → **CGO**. Realistic costs:

- **Cross-compilation** needs a C cross-toolchain per target (not just
  GOOS/GOARCH).
- **libc linkage** → the static-binary property is lost; glibc-linked binaries
  can fail on musl/Alpine.
- **CI/Docker** need a C compiler in the build stage.
- **Crash domain:** a C segfault kills the whole process (not a recoverable Go
  panic); C memory must be freed explicitly.
- **Blast radius:** ADR 0006 subpackage isolation controls *which* consumers pay,
  but once a consumer imports the CGO subpackage its whole binary is a CGO build.

So the CGO decision is really "are code-aware-chunking consumers OK accepting a
CGO build?" It deserves its own ADR (including the out-of-process alternative).

## DOT / DIAG

- Graph-structured DOT/DIAG ingestion = a **graph consumer**, not a text
  extractor. DOT (Graphviz) is one grammar; "DIAG" grammar still to be pinned
  (blockdiag/seqdiag family vs a Maestro-specific `.diag`).
- DOT parsing: reuse/learn from Maestro `pkg/knowledge`; `gonum.org/v1/gonum`'s
  `encoding/dot` is a mature pure-Go option if we don't lift Maestro's.

## Open questions (for when we scope)

1. Confirm **schema-as-data** over generics (the choice everything hangs off).
2. **DOT vs DIAG** grammars — one parser or two; what Maestro uses today.
3. **Lift-and-generalize Maestro `pkg/knowledge`** vs build fresh with it as a
   reference.
4. Maestro need is "both eventually": code chunking (near-term) → code graph
   (later).

## Note on PDF

The CGO discussion overlaps with the PDF sore point (issue #7 / ADR 0007): CGO
would unlock MuPDF (`go-fitz`) as a robust in-process parser. But PDF is being
solved **separately and urgently**, and for untrusted input it favors
*out-of-process* isolation (no CGO) over in-process CGO — see that work, which
supersedes ADR 0007.
