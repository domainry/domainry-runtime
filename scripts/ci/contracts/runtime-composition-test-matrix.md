# Runtime composition release gate

All commands run with `GOWORK=off`. The gate therefore tests the versions and
source sums in `go.mod`, `go.sum`, and `config/runtime-module-set.lock.json`, not
neighboring repository checkouts.

| Evidence | PR | main/nightly |
| --- | --- | --- |
| 11 real top-level Bindings open together | SQLite, all-Module and live Monitoring-SaaS mix; external Module/SaaS project graphs compile at the exact pins | PostgreSQL and MySQL all-Module |
| Host persistence contract | one `_schema_migrations`; ten persistent source owners; stable restart row count | same assertions on both real dialects |
| Contract compatibility | implementation version/sum + SDK version/sum + live capability digest + aggregate set digest | same lock before the full suite |
| Identity → Action → Notification → Audit | authorization deny, transactional rollback, replay/concurrency idempotency, outbox delivery, recipient isolation, cold restart | repeated under race detector |
| Identity → Action → Workflow → Record | action-scoped authorization and durable workflow continuation | repeated under race detector |
| Report → Data Exchange | canonical export submission plus paged/finalized projection | covered by full suite |
| Integration/HTTP workspace boundary | event replay and cross-workspace denial with audit evidence | repeated under race detector |
| Failure/recovery | SaaS owner workers stay disabled; Runtime trigger ownership is isolated; permission reconciliation compensates an earlier owner | real-dialect locks plus full related action/transaction/workflow/outbox/operations packages |

The PR profile is `scripts/ci/verify_runtime_composition.sh pr`. The heavy
profile is `scripts/ci/verify_runtime_composition.sh heavy` and requires
`RUNTIME_POSTGRES_TEST_DSN` plus `RUNTIME_MYSQL_TEST_DSN`.

The repository-wide `go test ./...` command is deliberately not this gate: the
current clean Runtime baseline contains unrelated stale unit fixtures and
release-policy checks. The composition gate runs the complete persistence
packages that own the shared transaction, action receipt, workflow,
notification outbox, operations recovery, and dialect contracts.
