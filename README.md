# Maestro CMS

Maestro CMS is an open-source Go library for building document, media, and knowledge engines.

It is intended to be a sibling to `github.com/SnapdragonPartners/maestro-llms`: a reusable package that product applications can depend on without inheriting another application's business rules, database schema, authorization model, or UI.

The initial consumers are expected to be:

- Morris, a family-office product with high-security document ingestion and retrieval needs.
- Cooper, a lightweight multi-tenant CMS with an integrated chatbot.
- Maestro, an agent orchestration system with project knowledge, MCP tools, and future code-aware retrieval needs.

## Purpose

Maestro CMS should provide reusable primitives for:

- Extracting text and metadata from source content.
- Chunking extracted text for embedding, retrieval, summarization, and context-window assembly.
- Supporting multiple chunking strategies, including prose-oriented and code-aware chunkers.
- Representing content sources, versions, derived artifacts, and retrieval handles.
- Embedding text and, over time, media-derived artifacts.
- Indexing and retrieving content through storage adapters.
- Supporting graph-shaped knowledge, such as project architecture nodes and relationships.
- Exposing retrieval results in forms suitable for chat, MCP tools, citations, and application UI.

The library should help applications build content-aware systems without forcing them into one product's assumptions.

## Non-Goals

Maestro CMS is not intended to be:

- A hosted CMS product.
- A web application framework.
- A tenant or subscription management system.
- A replacement for `maestro-llms`.
- A copy of Morris's document model.
- A copy of Maestro's agent workflow.
- The owner of application-specific authorization decisions.

Applications should own their own product policies. Maestro CMS should provide composable tools and contracts.

## Design Philosophy

The core package should be storage-neutral and product-neutral.

Product-specific concerns should sit at application edges:

- Morris owns family-office authorization, classification, audit, and active-revision policy.
- Cooper owns tenants, subscriptions, public/private content, teams, and publishing workflows.
- Maestro owns sessions, stories, agents, workspaces, and MCP tool orchestration.

Where a behavior is broadly useful, Maestro CMS can define an interface or helper. Where a behavior encodes product policy, the application should implement it.

## Likely Package Areas

Early package boundaries may include:

- `extract`: MIME-aware text extraction from PDF, DOCX, HTML, Markdown, and plain text.
- `chunk`: chunking interfaces and implementations for prose, documents, and code.
- `tokens`: token counting, estimation, and budget-aware truncation.
- `content`: source, version, artifact, media type, and provenance primitives.
- `embed`: provider-neutral embedding request and response contracts.
- `retrieval`: search result, context window, source handle, citation, and retriever contracts.
- `graph`: structured knowledge graph parsing, validation, and subgraph extraction.
- `store`: object-store interfaces and optional adapters.
- `index`: optional storage adapters for Postgres/pgvector, AlloyDB, SQLite FTS, and other backends.

These names are provisional. The first specification should refine them into an implementation plan.

## Relationship To Maestro LLMs

`maestro-llms` owns provider integration for LLMs and embeddings.

Maestro CMS should not duplicate provider adapters. Instead, it should define content-oriented contracts and depend on `maestro-llms` where provider behavior is needed.

For example, Maestro CMS may define chunking and embedding workflows, while `maestro-llms` supplies the concrete embedding client.

## Status

This repository is newly created.

The first milestone is a v1 specification that identifies reusable code from Morris and Maestro, defines initial package boundaries, and proposes a concrete extraction plan.

