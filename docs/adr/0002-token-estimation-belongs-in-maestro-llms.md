# ADR 0002: Token estimation belongs in maestro-llms; no `tokens` package here

- Status: Accepted
- Date: 2026-05-30

## Context

The draft spec proposed a `tokens` package, seeded from Maestro's
`pkg/utils/tiktoken.go` (tiktoken-go wrapper) and Morris's char/4 estimate.

Token estimation is provider- and model-shaped, which is `maestro-llms`'s domain,
not content's. `maestro-llms` already has a `middleware.TokenEstimator`
(`llms/middleware/estimator.go`) and owns post-call accounting (`Usage`).

Caveat, confirmed by reading the code: that estimator is **request-shaped**, not a
text helper — its methods are `EstimateChat(llms.ChatRequest)` and
`EstimateEmbeddings(llms.EmbeddingRequest)`, both returning
`ratelimit.UsageUnits` for the rate limiter. Its char-based core (`tokensForText`,
~3 chars/token, biased high) is **unexported**. So there is no `text → int` helper
to reuse today.

## Decision

- No `tokens` package in `maestro-cms`. Do not re-implement a model-aware
  tokenizer here.
- `chunk` stays pure and dependency-light: it accepts an injected
  `func(string) int`, defaulting to a small local char/N estimate (see
  [ADR 0004](0004-embed-is-a-runner-over-a-pure-chunker.md)). `chunk` does not
  import `maestro-llms`.
- The only `maestro-llms` ask is **small and non-blocking**: optionally export a
  `text → int` helper (e.g. promote `tokensForText`, or add a tokenizer-backed
  variant) so apps that want fidelity-consistent counts can inject it. Char/N is
  the default until then.

## Consequences

- No duplicate tokenizer; model knowledge stays in `maestro-llms`.
- `chunk` composes with any estimator via the injected function; char/N is the
  zero-config, zero-dependency default.
- Higher fidelity later is a drop-in `func(string) int` — no change to `chunk`'s
  contract — once maestro-llms exposes a text-level helper.
