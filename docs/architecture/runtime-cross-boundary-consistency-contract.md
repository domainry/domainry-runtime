# Runtime Cross-Boundary Consistency Contract

> Status: enforced for the transaction foundation on 2026-07-19.
> Authority: [Domainry 后端开发指导](backend-development-guide.md) and [Runtime Transaction Mutation Inventory](runtime-transaction-mutation-inventory.md).

## Rule

Work that cannot share the owner database transaction must never begin from an in-memory-only decision. The local owner first persists one durable fact, then performs the external/process-local step. Completion, unknown outcome, reconciliation, compensation, and manual review are explicit persisted states.

The reusable `_transaction_boundary_intents` state machine is:

`pending -> executing -> succeeded`

`executing -> reconciliation_required -> executing`

`reconciliation_required -> compensating -> compensated`

`reconciliation_required|compensating -> manual_review`

Claims use a lease owner, expiry, and monotonically increasing fencing token. `(workspace_id, owner, operation, idempotency_key)` is unique. A stale worker cannot complete a newer claim.

## Boundary mapping

| Profile | Durable fact before boundary | Reconciliation | Compensation/manual outcome |
| --- | --- | --- | --- |
| T04 Action external step | `_integration_invocations(status=prepared)` is inserted before the Provider call; request and compensation metadata are redacted and durable | the reconciliation worker scans stale `prepared` facts with an empty `response_ref`, then uses a prepared-state compare-and-swap to mark exactly one `reconciliation_required` fact with `backend.integration.invocation.external_receipt_missing`; normal completion atomically replaces status/outcome metadata | reserve steps retain compensation operation/request and persist the compensation invocation/evidence; unresolved outcome requires manual review |
| T05 Bulk | bulk operation receipt plus deterministic row keys | retry resumes/replays row units without repeating completed receipts | already committed rows are reviewed/compensated; no false all-or-nothing claim |
| T06 Import | import receipt plus deterministic row keys | retry resumes from durable row/operation evidence | partial imports remain explicit and require compensating mutations when reversal is required |
| T11 Metadata Runtime refresh | the definition publication transaction inserts `_transaction_boundary_intents(owner=metadata, operation=runtime_refresh, status=executing)` with definition/version/Audit/active revision | inline refresh failure moves the intent to `reconciliation_required`; an expired/incomplete execution can be claimed with a newer fencing token | repeated unsafe failure can move to `manual_review`; publication history remains immutable |
| T13 ChangePlan apply | change-plan draft/operation receipts and per-item owner commits | operation receipts and published/draft revision expose exact completed scope for retry | rollback policy selects automatic, compensating plan, or manual compensation by resource type |
| T17 Notification publication | publication request state machine and durable delivery rows | due processing retries from publication/delivery state | terminal delivery/publication failures remain reviewable rather than being erased |
| T21/T22 Integration events | acceptance atomically writes `_integration_events` plus `_integration_event_mapping_intents` | event lease/fencing, retry/dead-letter state, and mapping intent support replay | cross-owner target failure uses retry/dead-letter and target-specific compensation/manual review |

## Proof obligations

- A persistence failure for a prepared external invocation must prevent the Provider call.
- A stale prepared invocation with no external receipt must be detected and marked once; completed invocations and concurrent/repeated scans must not be reported.
- Crash-after-commit recovery must close the producer database/process owner, reopen the same durable database through a new worker owner, and prove that polling can still discover and claim the committed Outbox row without any in-memory wakeup.
- A metadata active-revision failure must roll back definition, version, Audit, and refresh intent.
- A process-local refresh failure must leave a claimable `reconciliation_required` intent.
- Illegal state transitions and stale fencing tokens must fail without changing the durable state.
- Compensation completion must preserve the original intent and evidence; it must not delete or rewrite history.
