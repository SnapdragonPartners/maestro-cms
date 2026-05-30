# ADR 0004: `embed` is an orchestration runner over a pure `chunk`

- Status: Accepted
- Date: 2026-05-30

## Context

Chunking-plus-embedding "feels simple but is easy to get wrong" — the same shape
as `llms/toolloop`, which sits as a composable layer *over* `ChatClient` rather
than inside it. The primitive stays pure; the orchestration that is easy to get
wrong is opt-in around it.

`maestro-llms` deliberately leaves batching and chunking to the caller, and the
optimal embedding batch spans chunks from *many* sources. Fusing embedding into
the chunker would couple deterministic CPU work to network/cost/partial-failure
work, break independent re-chunk vs re-embed, and lose cross-document batching.

## Decision

- **`chunk` stays pure**: `text → []Chunk`. Deterministic, no network, testable
  with no fakes. It accepts an injected `func(string) int` estimator (char/4
  default; see [ADR 0002](0002-token-estimation-belongs-in-maestro-llms.md)).
- **`embed` is a runner** over `llms.EmbeddingClient`
  ([ADR 0001](0001-consume-maestro-llms-embedding-interface.md)) that owns the hard part:
  cross-document batching, partial-failure handling, ID/order matching, retry
  (via `llms` middleware), and token-budget-aware batch sizing. Input: `[]Chunk`
  (plus estimator). Output: persist-ready embedded records.
- An optional `Pipeline` convenience chains `extract → chunk → embed → records`.
- **Persistence stays in the app.** The runner returns records (optionally via a
  sink interface); it does not write to any store.

**Partial-failure semantics.** `llms.EmbeddingClient` is all-or-nothing *per
call*: one batch returns a full response or an error. The runner packs chunks into
multiple batches (sized by provider limits and token budget), so failure is
per-batch, not per-run. On a batch error the runner relies on `llms` middleware for
retry/backoff; if the batch still fails it MAY bisect the batch to isolate a poison
input. Successful batches are always preserved and returned; each unrecoverable
batch is reported as a diagnostic (the chunks it covered, the error, and whether it
is retryable). The runner never partially writes anything — the caller decides
whether to persist the successful records and re-run the failures.

## Consequences

- Re-chunk (config change) and re-embed (model change) are independent operations.
- Batching efficiency is available because the runner sees many documents' chunks.
- Apps get "hand me a source and an `llms` client, get back records ready to
  persist" — with the seam in the right place.
