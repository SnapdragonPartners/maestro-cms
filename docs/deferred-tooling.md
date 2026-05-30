# Deferred Tooling

Repo-tooling items intentionally left out of the initial scaffolding
(`maestro-llms` has them; we don't yet, because the trigger doesn't exist).
Add each when its trigger lands so we don't rediscover the gap later.

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

## 2. golangci-lint depguard: core-must-not-import-adapters

- **What `maestro-llms` has:** a `depguard` rule enforcing that core packages
  (`llms/*`, middleware, ratelimit, testllm) cannot import provider packages —
  providers are leaf imports only apps pull in.
- **Why deferred:** `maestro-cms` has no adapter packages yet, so there is
  nothing to fence off. A placeholder comment marks the spot in
  `.golangci.yaml`.
- **Add when:** the first optional adapter package exists (`store/*`, `index/*`).
  The rule should deny core packages (extract, chunk, content, tokens, embed,
  retrieval) from importing `store/<adapter>` and `index/<adapter>`, so cloud
  SDKs and DB drivers stay confined to adapters. This enforces the load-bearing
  boundary in ADRs and `docs/spec-v1.md` (core stays provider/storage-neutral).

## 3. (note) maestro-llms text-token-estimator work request

Tracked by the `maestro-llms` team for their next release (handed off, not in
this repo). Relevant here only as the dependency behind
[ADR 0002](adr/0002-token-estimation-belongs-in-maestro-llms.md): until they
expose a `text -> int` helper, `chunk` uses an injected `func(string) int` with a
local char/N default.
