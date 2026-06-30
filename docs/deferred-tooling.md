# Deferred Tooling

Repo-tooling items intentionally left out of the initial scaffolding
(`maestro-llms` has them; we don't yet, because the trigger doesn't exist).
Add each when its trigger lands so we don't rediscover the gap later.

Status: all items done (kept here for the audit trail).

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

## 4. PDF parser spike — DONE

- **Resolved by [ADR 0010](adr/0010-pluggable-pdf-engines.md):** `extract/pdf` is
  now a pluggable engine interface with no unsafe default. The recommended engine
  `extract/pdf/pdftotext` runs Poppler out-of-process (killed on timeout/cancel —
  no goroutine leak, no in-process crash from untrusted input). The pure-Go
  `dslipak/pdf` parser (and its watchdog) is kept only as the explicit
  `extract/pdf/purego` fallback for small, trusted input. ADR 0007 is superseded.
