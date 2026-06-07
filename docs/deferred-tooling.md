# Deferred Tooling

Repo-tooling items intentionally left out of the initial scaffolding
(`maestro-llms` has them; we don't yet, because the trigger doesn't exist).
Add each when its trigger lands so we don't rediscover the gap later.

Status: item 4 open; items 1, 2, and 3 done (kept here for the audit trail).

## 1. Integration test target + workflow — DONE

- **Done:** landed with the first real-service adapter, `store/gcs`. A
  `test-integration` Make target starts a Dockerized `fsouza/fake-gcs-server`
  (no official Google GCS emulator exists), waits for readiness, runs the
  `//go:build integration` tests with `STORAGE_EMULATOR_HOST` set, and tears the
  container down. A manual-dispatch `integration.yml` workflow runs the same on
  CI. The default `make test` and CI stay network-/Docker-free: the tagged tests
  are excluded without the `integration` tag and `t.Skip` when the emulator host
  is unset.
- **Extend when:** the next real-service adapter lands (e.g. `index/pgvector`
  against real Postgres) — add its build-tagged tests under the same target. If a
  macOS ad-hoc-codesign step is ever needed (as in `maestro-llms`), add it then.

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
