# Runtime Operations Reliability Test Matrix

This matrix is the release evidence contract for the Runtime Operations control
plane. A green unit test alone is not production acceptance: the `ci` profile
must pass on a clean commit with real PostgreSQL and MySQL services, and the
`drill` profile must produce validated recovery evidence through the
infrastructure-owned executable configured by `RUNTIME_DRILL_DRIVER`.

## Gate profiles

| Profile | Required environment | Evidence | Release meaning |
| --- | --- | --- | --- |
| `deterministic` | local Go toolchain and SQLite | correctness, worker recovery, HTTP/OpenAPI/Runbook, migration and bounded-capacity logs | deterministic mock and local database contract |
| `race` | race-capable Go platform | worker, metrics/capacity/cache, lifecycle, Operations and Runtime drain logs | no detected shared-memory race in the reviewed operational paths |
| `real-dialects` | real PostgreSQL and MySQL DSNs; SQLite is created locally | serialized cross-package logs; concurrent tests still run inside packages | the same idempotency, fencing, migration and workspace contracts hold on all three SQL dialects |
| `drill` | absolute executable `RUNTIME_DRILL_DRIVER`, real PostgreSQL and MySQL DSNs | per-engine restore JSON with actual RPO/RTO plus lifecycle-retention logs | backup/restore remains infrastructure-owned while Runtime verifies migrations and restored module state |
| `ci` | release workflow PostgreSQL 16 and MySQL 8.4 services | one versioned gate summary and all logs | mandatory precondition of Runtime release artifact construction |

`RUNTIME_REQUIRE_REAL_DIALECTS=1` converts a missing external DSN from a test
skip into a failure. Real-dialect packages are serialized because they share
one schema and contain destructive migration/retirement contracts; concurrency
inside each package remains enabled.

## Requirement mapping

| Requirement | Primary executable evidence |
| --- | --- |
| replay, fingerprint conflict, crash/reclaim | Operations store/application tests; record idempotency crash-window tests |
| cross-owner rollback and recovery | transaction fault injection, action atomic rollback, workflow/integration worker tests |
| Workspace A/B and explicit system scope | Operations store/application scope tests and workspace owner dialect matrix |
| fencing, retry, DLQ, cancel and drain | worker testkit scenarios, owner DLQ adapters, lease release and two-instance rolling drain tests |
| three SQL dialects and two Runtime instances | `TestOperationsContractAcrossRealDialects` plus existing dialect lock/lease suites |
| migration, checksum, lock and restore | Runtime migration tests plus the infrastructure-owned `RUNTIME_DRILL_DRIVER` receipt contract |
| HTTP, Capability, OpenAPI and Runbook | HTTP/OpenAPI suites and Operations boundary tests |
| load, soak, retry storm, backlog recovery | Foundation capacity/rate-limit soak, PostgreSQL pool soak, record queue and integration pressure recovery tests |
| rolling deployment and restart | controlled two-instance drain and bounded Runtime shutdown tests |
| telemetry, alert and audit | diagnostics/readiness, break-glass audit alert and mandatory-audit tests |

## External dependency levels

- `deterministic_mock`: scripted clocks, IDs, randomness, faults and provider
  outcomes; no network dependency.
- `sandbox`: disposable local SQLite files and Docker databases; real protocol
  and database engine, no production data or provider account.
- `real_provider`: an explicitly provisioned provider account/endpoint. These
  tests must declare their credential source and must never silently fall back
  to a mock. The Operations release gate currently requires real database
  engines but no external SaaS provider.

## Evidence governance

The gate summary records commit, dirty-worktree state, runtime/schema/config
identity, contract hashes, toolchain/OS, external dependency availability and
the exact log set. Flaky exemptions live only in
`runtime-reliability-flaky-tests.json`; every entry requires owner, reason and
expiry, and an expired entry fails the gate. An empty list means there is no
approved flaky exemption.

Failures from unrelated repository baselines are recorded separately from a
gate regression; neither category permits deleting or weakening a test. Drill
JSON is the source of actual RPO/RTO, data-count comparison and reconciliation
actions. Evidence artifacts are uploaded even when the release workflow fails.
