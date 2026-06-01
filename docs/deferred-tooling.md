# Deferred Tooling

Repo-tooling items intentionally left out of the initial scaffolding
(`maestro-llms` has them; we don't yet, because the trigger doesn't exist).
Add each when its trigger lands so we don't rediscover the gap later.

Status: item 1 still open; items 2 and 3 done (kept here for the audit trail).

## 1. Integration test target + workflow

- **What `maestro-llms` has:** a `test-integration` Make target (OS-aware:
  macOS routes through an ad-hoc-codesign script, Linux/CI runs
  `go test -tags=integration`), a `test-integration-local` escape hatch, and a
  manual-dispatch `integration.yml` GitHub workflow that runs live tests against
  real services.
- **Why deferred:** `maestro-cms` has no integration tests yet. Core packages
  (extract/chunk/content/tokens) are pure and unit-tested.
- **Add when:** the first adapter that talks to a real external service lands —
  e.g. `store/gcs` (real GCS / emulator) or `index/pgvector` (real Postgres).
  At that point add the build-tagged tests, the `test-integration` target, and a
  manual-dispatch workflow; keep the default `make test` and CI network-free.

## 2. golangci-lint depguard: core-must-not-import-adapters — DONE

- **Done:** landed with the first adapter subpackage (`extract/html`). The
  `depguard` `core-no-adapters` rule in `.golangci.yaml` denies core packages
  (`content`, `chunk`, root `extract`, root `store`) from importing the adapter
  subpackages (`extract/html|pdf|docx`, `store/gcs`, `index/*`); `_test` files
  are exempt. Verified to fire (a `store -> extract/html` import is rejected) and
  to pass on real code. Enforces the ADR 0006 boundary.

## 3. maestro-llms text-token-estimator — DELIVERED

`llms.EstimateTextTokens` shipped in maestro-llms v0.6.0 (neutral ~4 chars/token,
rune-counted). The `chunk` package consumes an injected `func(string) int`
(standard injection `llms.EstimateTextTokens`; local rune-counted char/4
default). See [ADR 0002](adr/0002-token-estimation-belongs-in-maestro-llms.md).
