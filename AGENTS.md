# Agent Instructions

This file provides guidance for Codex and other AI coding agents working in this repository.

## Current State

`maestro-cms` is a new open-source Go module planned at:

`github.com/SnapdragonPartners/maestro-cms`

The repository is pre-implementation. The first committed artifacts are conceptual docs:

- `README.md` — high-level product/library intent.
- `docs/spec-v1.md` — draft v1 specification and extraction plan.

The current goal is to turn the draft specification into a concrete engineering plan, then extract the first reusable package slices from Morris and Maestro.

## What This Package Is

`maestro-cms` is an app-neutral Go library for building document, media, and knowledge engines.

It is a sibling to `github.com/SnapdragonPartners/maestro-llms`, not a product application. It should provide reusable content and knowledge primitives that can be consumed by:

- **Morris** — family-office product with strict security, classification, audit, and document retrieval requirements.
- **Cooper** — lightweight multi-tenant content-management product with an integrated chatbot.
- **Maestro** — agent orchestration system with project knowledge, MCP tools, and future code-aware retrieval needs.

The core design tension to keep in mind on every change: **nothing in this package may import product-specific assumptions from any one app.** Tenant billing, family-office ACLs, audit taxonomy, admin approval workflows, Maestro session/story state, and UI behavior belong in the applications. When a feature seems to need app context, prefer a small interface, callback, option, or adapter boundary over concrete app logic.

## Binding Design Documents

Read `docs/spec-v1.md` before making structural changes. It is the current planning document for:

- Initial consumers.
- Candidate package boundaries.
- What should be extracted first.
- What should not be extracted yet.
- Open design questions.

As the repository matures, significant architectural choices should be recorded as ADRs under `docs/adr/`. Do not "fix" deliberate limitations described in an ADR without superseding it.

## Expected Architecture

Planned module path:

```text
github.com/SnapdragonPartners/maestro-cms
```

Provisional package areas:

```text
extract/      MIME-aware extraction from documents and content sources
chunk/        prose, structured-document, and code-aware chunking
tokens/       token counting, estimation, truncation, and budget helpers
content/      source, version, artifact, media type, provenance primitives
embed/        content-facing embedding request/response contracts
retrieval/    search, context window, citation, source handle contracts
graph/        structured knowledge graph parsing, validation, subgraphs
store/        object-store interfaces and optional adapters
index/        optional retrieval/index adapters such as pgvector or SQLite FTS
testcms/      deterministic fakes and test helpers
```

These package names are not final. Keep them small and consumer-driven.

Load-bearing structural decisions:

- **Core packages stay provider-neutral and storage-neutral.** Optional adapters may import provider SDKs, database drivers, or cloud clients; core packages should not.
- **Provider integration belongs primarily in `maestro-llms`.** Do not duplicate LLM or embedding provider adapters here unless a content-specific wrapper is clearly justified.
- **Application policy stays outside the library.** Morris classification, Cooper subscriptions, and Maestro workflow state are consumers of this library, not part of it.
- **Capability growth should be additive.** Prefer optional interfaces, small adapters, and new packages over widening central interfaces prematurely.
- **Fakes are first-class.** Shared interfaces should have deterministic tests and fakes so consumers can test without real cloud services, databases, or model calls.

## Dependency Discipline

Prefer the Go standard library where practical.

Avoid adding third-party dependencies for simple functionality. As a rule of thumb, if the needed behavior can be recreated clearly in roughly 200 lines of straightforward, well-tested Go, prefer implementing it locally over importing another package. The point is not dependency minimalism for its own sake; it is to keep the import tree small, auditable, and easy for downstream products to reason about.

Third-party dependencies are appropriate when:

- The domain is complex or risky to implement incorrectly, such as PDF parsing, tokenizer fidelity, image/video processing, tree-sitter parsers, database drivers, or cloud SDKs.
- The package is mature, maintained, and materially reduces product risk.
- The dependency can live in an optional adapter package rather than core.

When adding a dependency, document why it is justified and keep it out of core packages if possible.

## Extraction Guidance

Likely first extraction candidates:

- Morris `internal/extract` into `extract`.
- Morris `internal/chunk` into `chunk`.
- Maestro token counting helpers into `tokens`.
- Morris embedding contracts into `embed`, after deciding the exact relationship with `maestro-llms`.
- Morris object-store interface into `store`, with Morris-specific path conventions removed.

Do not extract these as-is:

- Morris ingestion worker.
- Morris document/revision schema.
- Morris classification or audit repositories.
- Cooper tenant/subscription model.
- Maestro agent/session/story persistence.
- Maestro MCP server implementation.

These may inspire interfaces, but should not become core library code.

## Build And Test Commands

This repository does not yet have a Go module, Makefile, lint config, or CI workflow. As implementation begins, copy and adapt the repo-management posture from `maestro-llms`:

- `go.mod` with module path `github.com/SnapdragonPartners/maestro-cms`.
- `Makefile` targets for `build`, `test`, `lint`, and later `test-integration`.
- Strict `golangci-lint` configuration.
- GitHub Actions for lint/build/test and CodeQL where appropriate.
- Deterministic unit tests for every package before adding adapters.

Once these exist, prefer the canonical Make targets over ad hoc commands.

## Versioning

Pre-1.0. Expect v0.x minor versions to evolve quickly while Morris, Cooper, and Maestro validate boundaries.

Each release should have a clear scope. Do not expand a release mid-stream just because a related idea is nearby. If a structural decision affects multiple consumers, capture it in the spec or an ADR.

