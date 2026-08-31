# Runtime Cross-Boundary Consistency Contract

> Status: enforced for the transaction foundation on 2026-07-19.
> Authority: [Domainry 后端开发指导](backend-development-guide.md) and [Runtime Transaction Mutation Inventory](runtime-transaction-mutation-inventory.md).

## Rule

Work that cannot share the owner database transaction must never begin from an in-memory-only decision. The local owner first persists one durable fact, then performs the external/process-local step. Completion, unknown outcome, reconciliation, compensation, and manual review are explicit persisted states.

Connector delivery uses two owner-local facts. Runtime atomically commits `_publication_outbox` with the business mutation and retains only handoff status plus an opaque Integration receipt reference. Integration accepts that handoff idempotently, owns provider invocation evidence and reconciles uncertain provider outcomes in its own database. Runtime never reads or writes Integration tables.

The reusable `_transaction_boundary_intents` state machine is:

`pending -> executing -> succeeded`

`executing -> reconciliation_required -> executing`

`reconciliation_required -> compensating -> compensated`

`reconciliation_required|compensating -> manual_review`

Claims use a lease owner, expiry, and monotonically increasing fencing token. `(workspace_id, owner, operation, idempotency_key)` is unique. A stale worker cannot complete a newer claim.

## Boundary mapping

| Profile | Durable fact before boundary | Reconciliation | Compensation/manual outcome |
| --- | --- | --- | --- |
| T04 Action external step | Runtime commits `_publication_outbox(status=queued)` in the same transaction as the business mutation, then its fenced publication worker calls `Integration Delivery.Accept` with the message/deduplication key | Runtime safely retries owner acceptance by message identity; Integration persists and reconciles its own invocation/receipt evidence without exposing provider state to Runtime | Runtime can cancel or dead-letter the publication handoff; provider compensation and uncertain-outcome review belong to Integration |
| T05 Bulk | bulk operation receipt plus deterministic row keys | retry resumes/replays row units without repeating completed receipts | already committed rows are reviewed/compensated; no false all-or-nothing claim |
| T06 Import | import receipt plus deterministic row keys | retry resumes from durable row/operation evidence | partial imports remain explicit and require compensating mutations when reversal is required |
| T11 Metadata Runtime refresh | the definition publication transaction inserts `_transaction_boundary_intents(owner=metadata, operation=runtime_refresh, status=executing)` with definition/version/Audit/active revision | inline refresh failure moves the intent to `reconciliation_required`; an expired/incomplete execution can be claimed with a newer fencing token | repeated unsafe failure can move to `manual_review`; publication history remains immutable |
| T13 ChangePlan apply | change-plan draft/operation receipts and per-item owner commits | operation receipts and published/draft revision expose exact completed scope for retry | rollback policy selects automatic, compensating plan, or manual compensation by resource type |
| T17 Notification publication | publication request state machine and durable delivery rows | due processing retries from publication/delivery state | terminal delivery/publication failures remain reviewable rather than being erased |
| T21/T22 Integration events | Integration acceptance atomically writes its event inbox and mapping intent in the Integration-owned database | Integration owns event lease/fencing, retry/dead-letter, replay, and provider reconciliation | a target callback uses an idempotent SDK receipt; Integration retains retry/dead-letter and target-specific manual review |

## Proof obligations

- A Runtime business transaction failure must prevent `_publication_outbox` creation and therefore prevent any Integration owner call.
- Integration must durably accept a message identity before a Provider call and reconcile stale prepared invocations inside the Integration owner.
- Crash-after-commit recovery must close the producer database/process owner, reopen the same durable database through a new worker owner, and prove that polling can still discover and claim the committed Outbox row without any in-memory wakeup.
- Runtime publication completion stores only the opaque Integration receipt reference; it must not query Integration invocation or event tables.
- A metadata active-revision failure must roll back definition, version, Audit, and refresh intent.
- A process-local refresh failure must leave a claimable `reconciliation_required` intent.
- Illegal state transitions and stale fencing tokens must fail without changing the durable state.
- Compensation completion must preserve the original intent and evidence; it must not delete or rewrite history.
