# ADR 0005: Defer the graph engine to v2; v1 content needs only stable IDs

- Status: Accepted
- Date: 2026-05-30

## Context

Maestro's knowledge graph is mechanically reusable, but its ontology (node and
edge types) — and even its SQLite `CHECK` constraints — are hardcoded to an
architecture-knowledge schema. That ontology is Maestro-specific. Maestro's
knowledge component is also its least mature, optional part; `.maestro/knowledge.dot`
is optional, not mandatory.

A *generic* directed-graph primitive (caller-defined node/edge schema, subgraph,
traversal, optional DOT, optional FTS) does have at least three plausible
consumers: Maestro knowledge, the planned Morris family-tree tool, and content
tagging/grouping. That those three ontologies are all different is exactly why the
schema must be pluggable.

## Decision

- No `graph` package in v1.
- Revisit in v2 as a generic primitive with **caller-defined** node/edge schema.
  The Maestro architecture-knowledge ontology stays in Maestro.
- v1's only forward-compatibility obligation to a future graph is **stable opaque
  IDs** on Source and Artifact (see
  [ADR 0003](0003-content-as-single-parent-provenance-tree.md)). No edges or
  relationship types are modeled in v1.

## Consequences

- v1 stays focused on the high-confidence content pipeline.
- When built, the graph primitive can index over content IDs without a content
  rewrite.
- The three known ontologies (architecture, family tree, tags) lock in the
  pluggable-schema requirement before any code is written.
