# Runtime Operations Inventory

This inventory records the production-reachable operational entrypoints and
their adoption of the unified Operations control plane. An existing owner
action is not evidence that it already registers a shared durable operation.

## Unified command contract

Every mutating operation must eventually register an
`operationsmodel.OperationsCommand` before owner execution. The envelope
requires an operation ID, owner kind, permission, explicit workspace or system
scope, idempotency key, semantic request fingerprint, requester, reason,
optional ticket/reference, and created/started/finished lifecycle timestamps.
The stable `OperationsReceipt` carries a status URL, redacted result evidence,
related IDs, correlation, next action, and one of `retryable`, `terminal`, or
`manual_intervention` for failures.

Same owner/key plus the same fingerprint replays the original operation.
Reusing the key with a different fingerprint is a conflict. Owner permission,
precondition and current-readiness checks still run before any receipt body is
disclosed.

## Unified control-plane endpoints

- `POST /operations` registers a durable `created` job and returns `202`, a
  receipt body, and `Location: /operations/{operationID}`;
- `GET /operations/{operationID}` returns a workspace-isolated receipt;
- `GET /operations?status=...&limit=...` returns a bounded workspace list;
- `GET /operations/catalog` exposes the machine-checked definition contract;
- `PUT /operations/controls/{controlKind}/{owner}` applies a revision-fenced,
  system-scoped maintenance, worker-pause, or instance-drain control and returns
  the terminal operation receipt; `GET /operations/controls` lists shared state;
- `POST /operations/leases/{owner}/{resourceID}/force-release` accepts only a
  registered owner plus the observed lease owner/token, verifies expiry or
  explicit stuck evidence, then clears ownership and increments fencing in one
  serializable CAS transaction;
- `GET /operations/dead-letters/{owner}/{deadLetterID}` returns a redacted
  owner projection; the sibling `resolve|retry|ack` mutation routes register a
  durable receipt and delegate the transition back to the registered owner;
- `POST /operations/bulk/dead-letters/dry-run|apply` binds a deduplicated
  explicit-ID filter (maximum 100) to a short-lived confirmation fingerprint,
  then records every owner outcome in one durable parent receipt;
- `POST /operations/diagnostics/snapshots` captures only registered sections
  (`schema_migration`, `db_pool`, `worker_lease`, `queue_lag`, `dlq`,
  `backup_age`) with page size 50 and cost 300 hard limits; every section is
  redacted and links a machine-readable runbook;
- `POST /operations/break-glass`, `GET /operations/break-glass`, and the revision-fenced disable route use
  `_operation_break_glass_grants`; grants require two approvers distinct from the
  actor, an incident, an alert target, durable audit, and expire within one hour;
- the same key/fingerprint returns the original receipt with
  `Idempotency-Replayed: true`; a changed fingerprint returns conflict;
- `OperationsApplicationService.Start` and `Finish` enforce expected-state
  transitions for process-owned executors using explicit system scope.

The HTTP mutation gate reads maintenance and current-instance drain state from
`_operation_controls` on every non-exempt mutation. Reads and recovery
under `/operations/*` remain reachable; a control-store failure rejects the
mutation. Bootstrap owner supervisors poll the same rows, stop only the named
owner, and reconstruct desired state after restart. Readiness consumes the same
durable maintenance/drain state and the stable `RUNTIME_INSTANCE_ID`.

## Existing owner operations

| Owner | Production entrypoint | Current permission / scope | Current idempotency | Current audit / result | Unified job gap |
| --- | --- | --- | --- | --- | --- |
| Scheduler | `POST /operations/scheduler/definitions/{definitionID}/run` | `scheduler.command` or legacy Scheduler run grants; workspace principal | required caller key | owner audit plus terminal shared receipt headers | owner execution is wrapped by `scheduler.job.run`; replay returns stored owner result without a second owner call |
| Scheduler | `POST /scheduler/runs/{runID}/retry` | admin; workspace principal and owner precondition | required caller key | run event, owner audit and terminal shared receipt | wrapped by `scheduler.run.retry` |
| Scheduler | `POST /scheduler/runs/{runID}/cancel` | admin; workspace principal and owner precondition | required caller key | run event, owner audit and terminal shared receipt | wrapped by `scheduler.run.cancel` |
| Scheduler | `POST /scheduler/dead-letters/{deadLetterID}/resolve` | admin; workspace principal and dead-letter policy | required caller key | dead-letter event, owner audit and terminal shared receipt | legacy route is wrapped by `scheduler.dead_letter.resolve`; unified `/operations/dead-letters/scheduler/*` additionally separates inspect/resolve/ack and lets Scheduler reject retry |
| Workflow | `POST /workflow-processes/{processID}/retry` | authenticated owner policy; workspace principal | required caller key | process/execution evidence and terminal shared receipt | wrapped by `workflow.process.retry` |
| Workflow | `POST /workflow-processes/{processID}/cancel` | authenticated owner policy; workspace principal | required caller key | process/execution evidence and terminal shared receipt | wrapped by `workflow.process.cancel` |
| Workflow | `POST /workflow-processes/{processID}/resolve` | authenticated owner policy; workspace principal | required caller key | process/execution evidence and terminal shared receipt | wrapped by `workflow.process.resolve` |
| Workflow | `POST /workflow-executions/{executionID}/retry` | authenticated owner policy; workspace principal | required caller key | execution evidence and terminal shared receipt | wrapped by `workflow.execution.retry` |
| Workflow | `POST /workflow-executions/{executionID}/resolve` | authenticated owner policy; workspace principal | required caller key | execution evidence and terminal shared receipt | wrapped by `workflow.execution.resolve` |
| Automation | rule enable/disable/delete HTTP commands and process-owned rule/instruction/outbox execution | authenticated owner policy; workspace or restored worker scope | caller key for operator commands; execution/outbox owner receipts | terminal shared receipt for operator commands; execution trace/outbox evidence for workers | enable/disable/delete are wrapped by `automation.rule.enable|disable`; worker pause is the shared owner control |
| Integration | `POST /tenant-admin/integrations/events/{eventID}/replay` | `integration.admin`; workspace principal | owner event identity plus replay semantics | Integration-owned correlated event evidence | source-owned `integration.event.replay`; Runtime exposes no Integration recovery facade |
| Runtime publication handoff | `POST /operations/dead-letters/runtime_publication_outbox/{messageID}/retry` | Runtime operator policy; workspace principal | required caller key plus publication identity | Runtime handoff evidence and terminal shared receipt | wrapped by `runtime.publication.retry`; retry ends at idempotent Integration acceptance and never owns Provider delivery outcome |
| Metadata / Migration | `GET /metadata/migration-plan`, manifest provision/apply, Runtime startup migration | provision/admin or process configuration; installation/system scope | migration checksum/lock | durable migration ledger, checksum, release identity and backup ID | process-owned migration receipt is the ordered ledger; it is not exposed as a workspace HTTP mutation |
| Backup / Restore | `go run ./scripts/operations/runtime_disaster_recovery backup|restore|plan-restore` | infrastructure operator boundary; restore request requires operator/change plan and maintenance/drain evidence | backup ID and immutable target guards | validated machine-readable backup/drill receipt with actual RPO/RTO and reconciliation | external receipt is intentionally stored outside the database being restored and uploaded by the release/drill gate |
| Retention | lifecycle policy and cleanup workers | owner/system scope | caller key plus owner cleanup semantics | policy/cleanup evidence and terminal shared receipt | manual cleanup execution is wrapped by `retention.cleanup`; scheduled cleanup remains a fenced process-owned worker |
| Idempotency receipt recovery | `/operations/idempotency/receipts/*` | admin; workspace principal | required caller key plus receipt identity | explicit security audit and terminal shared receipt | retry/reset delegate to Deployment owner through `idempotency.receipt.retry|reset`; replay does not duplicate owner mutation or audit |

## Remaining migration work

- production-reachable Scheduler, Workflow, Integration, Automation, retention
  cleanup and idempotency recovery mutations register and finish the shared
  ledger around the real owner call. The wrapper never owns the business state
  transition; it prevents duplicate owner calls after a terminal replay and
  exposes the receipt through response headers without changing legacy bodies.
- release construction is gated by
  `scripts/operations/verify_runtime_operations_reliability.sh ci`; the gate requires real
  PostgreSQL/MySQL contracts, race coverage and versioned evidence. Recovery
  drills use the sibling `drill` profile and retain per-engine RPO/RTO.

These gaps remain open in
the Runtime operations reliability verification gate; this inventory must
be updated in the same change whenever a gap becomes production-reachable.
