# ADR 0001: Consume the maestro-llms embedding interface directly

- Status: Accepted
- Date: 2026-05-30

## Context

`maestro-llms` already defines a canonical, public, provider-neutral embedding
contract in its `llms` package: `EmbeddingClient`, `EmbeddingRequest`,
`EmbeddingResponse`, with eight task hints (plus an unspecified zero value),
batching, per-request `Dimensions`, optional `Title`, and `Usage`. It is validated across the OpenAI and Gemini
providers, and providers are leaf packages — importing `llms` alone is light and
does not pull in provider SDKs.

Morris has already independently defined a near-clone of this interface
(`internal/embeddings`) plus a Vertex adapter that maps its request type onto the
`maestro-llms` type. That duplication is precisely what `maestro-cms` exists to
prevent. `maestro-cms` will depend on `maestro-llms` regardless.

## Decision

`maestro-cms` does **not** define its own embedding contract. It consumes
`llms.EmbeddingClient` / `llms.EmbeddingRequest` / `llms.EmbeddingResponse`
directly.

The `maestro-cms` `embed` package is an orchestration layer (see
[ADR 0004](0004-embed-is-a-runner-over-a-pure-chunker.md)), not a competing
contract.

Morris's bespoke embedding interface and adapter should be reviewed by team
Morris and either justified or replaced by a thin use of `llms` plus the
`maestro-cms` embed runner.

## Consequences

- One embedding vocabulary across `maestro-llms`, `maestro-cms`, and all
  consumers; task hints and input IDs flow through unchanged.
- `maestro-cms` tracks the `maestro-llms` embedding API. Acceptable: same team,
  stable core, additive growth.
- No adapter boilerplate and no risk of the two interfaces drifting apart.
