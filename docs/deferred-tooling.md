# Deferred Tooling

Repo-tooling items intentionally left out of the initial scaffolding
(`maestro-llms` has them; we don't yet, because the trigger doesn't exist).
Add each when its trigger lands so we don't rediscover the gap later.

Status: items 1 and 4 open; items 2 and 3 done (kept here for the audit trail).

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

## 4. PDF parser spike — OPEN (queued)

- **Why:** `github.com/dslipak/pdf` can HANG (not just panic) on some inputs — a
  page with no `/Contents` stream makes `GetPlainText` spin without returning.
  `recover()` cannot catch it and it ignores `context`, so it is a DoS vector for
  untrusted PDFs. `extract/pdf` ships a wall-clock **timeout watchdog** as a
  stopgap (ADR 0007), but a timed-out parse leaks its goroutine, so the residual
  memory-growth risk under sustained hostile input is not closed.
- **Spike goals:** (a) diagnose the hang — is it the missing `/Contents` case
  specifically, and can we pre-validate page structure to avoid it, or is it
  fixable upstream; (b) evaluate maintained alternatives to dslipak/pdf;
  (c) decide whether PDF parsing should run out-of-process (the real fix for an
  uninterruptible parser). Outcome supersedes ADR 0007.
- **Until then:** do not point `extract/pdf` at high-volume untrusted input
  without external process isolation.
