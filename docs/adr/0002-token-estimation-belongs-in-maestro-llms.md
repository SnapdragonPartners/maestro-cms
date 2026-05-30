# ADR 0002: Token estimation belongs in maestro-llms; no `tokens` package here

- Status: Accepted
- Date: 2026-05-30

## Context

The draft spec proposed a `tokens` package, seeded from Maestro's
`pkg/utils/tiktoken.go` (tiktoken-go wrapper) and Morris's char/4 estimate.

Token estimation is provider- and model-shaped, which is `maestro-llms`'s domain,
not content's. `maestro-llms` already has a `middleware.TokenEstimator`
(`llms/middleware/estimator.go`) and owns post-call accounting (`Usage`).

When this ADR was first written, that estimator was **request-shaped** only
(`EstimateChat` / `EstimateEmbeddings` → `ratelimit.UsageUnits`, char-based core
unexported), so there was no `text → int` helper to reuse, and we filed a small
request for one.

**Delivered (maestro-llms v0.6.0, their ADR-0013):** `llms.EstimateTextTokens`, a
`func(string) int` directly assignable to `chunk`'s injection point. It is
**neutral ~4 chars/token, rune-counted** — deliberately not the rate limiter's
high-biased ~3 chars/token (over-reserving is safe for a limiter; for chunking it
just makes smaller chunks and more embedding calls), and not byte-counted (bytes
over-count non-Latin scripts, the opposite of what chunking wants). `llms` is the
smallest possible import (no providers, no middleware). Both estimators live in
that one package, so the "two estimators silently diverging" failure mode is
structurally prevented.

## Decision

- No `tokens` package in `maestro-cms`. Do not re-implement a model-aware
  tokenizer here.
- `chunk` stays pure and dependency-light: it accepts an injected
  `func(string) int` (see [ADR 0004](0004-embed-is-a-runner-over-a-pure-chunker.md)).
  The standard injection is `llms.EstimateTextTokens` (**requires maestro-llms
  ≥ v0.6.0**); a local char/N estimate remains the zero-dependency default. `chunk`
  itself does not import `maestro-llms` — the consumer wires the estimator in.
- Any safety margin is the caller's responsibility (shrink the budget passed to
  `chunk`, or wrap the estimator), since there is no single right bias for
  chunking. The maestro-llms helper is intentionally neutral.

## Consequences

- No duplicate tokenizer; model knowledge stays in `maestro-llms`, and chunk-time
  counts no longer drift from reservation-time counts.
- `chunk` composes with any estimator via the injected function; char/N is the
  zero-dependency default, `llms.EstimateTextTokens` the standard one.
- A future tokenizer-backed, model-aware `TextEstimator` in maestro-llms
  (deferred, on their roadmap) would be a drop-in `func(string) int` — no change
  to `chunk`'s contract.
