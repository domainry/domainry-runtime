# Runtime Cross-Boundary Consistency Contract

> Status: enforced for the transaction foundation on 2026-07-19.
> Authority: [Domainry 后端开发指导](backend-development-guide.md) and [Runtime Transaction Mutation Inventory](runtime-transaction-mutation-inventory.md).

## Rule

Work that cannot share the owner database transaction must never begin from an in-memory-only decision. The local owner first persists one durable fact, then performs the external/process-local step. Completion, unknown outcome, reconciliation, compensation, and manual review are explicit persisted states.

Connector delivery uses two owner-local facts. Runtime atomically commits `_publication_outbox` with the business mutation and retains only handoff status plus an opaque Integration receipt reference. Integration accepts that handoff idempotently, owns provider invocation evidence and reconciles uncertain provider outcomes in its own database. Runtime never reads or writes Integration tables.

Runtime does not keep a third generic transaction-intent queue. The former
`_transaction_boundary_intents` abstraction had no production producer or
worker and was removed. A real asynchronous boundary must be represented by
the existing typed Outbox/Operations contract or by the destination owner's
durable run state; lease, reconciliation and compensation semantics stay with
that concrete owner.

## Boundary mapping

| Profile | Durable fact before boundary | Reconciliation | Compensation/manual outcome |
| --- | --- | --- | --- |
| T04 Action external step | Runtime commits `_publication_outbox(status=queued)` in the same transaction as the business mutation, then its fenced publication worker calls `Integration Delivery.Accept` with the message/deduplication key | Runtime safely retries owner acceptance by message identity; Integration persists and reconciles its own invocation/receipt evidence without exposing provider state to Runtime | Runtime can cancel or dead-letter the publication handoff; provider compensation and uncertain-outcome review belong to Integration |
| T05 Bulk | bulk operation receipt plus deterministic row keys | retry resumes/replays row units without repeating completed receipts | already committed rows are reviewed/compensated; no false all-or-nothing claim |
| T06 Import | import receipt plus deterministic row keys | retry resumes from durable row/operation evidence | partial imports remain explicit and require compensating mutations when reversal is required |
| T11 Metadata Runtime refresh | Metadata atomically publishes its source-owned current projection; Runtime consumes only that committed projection | startup/request refresh rereads the current projection and returns failure directly; there is no asynchronous Runtime refresh worker or generic intent row | any future asynchronous refresh must register a typed Operations kind or remain in Metadata-owned run state |
| T13 ChangePlan apply | change-plan draft/operation receipts and per-item owner commits | operation receipts and published/draft revision expose exact completed scope for retry | rollback policy selects automatic, compensating plan, or manual compensation by resource type |
| T17 Notification publication | publication request state machine and durable delivery rows | due processing retries from publication/delivery state | terminal delivery/publication failures remain reviewable rather than being erased |
| T21/T22 Integration events | Integration acceptance atomically writes its event inbox and mapping intent in the Integration-owned database | Integration owns event lease/fencing, retry/dead-letter, replay, and provider reconciliation | a target callback uses an idempotent SDK receipt; Integration retains retry/dead-letter and target-specific manual review |

## Proof obligations

- A Runtime business transaction failure must prevent `_publication_outbox` creation and therefore prevent any Integration owner call.
- Integration must durably accept a message identity before a Provider call and reconcile stale prepared invocations inside the Integration owner.
- Crash-after-commit recovery must close the producer database/process owner, reopen the same durable database through a new worker owner, and prove that polling can still discover and claim the committed Outbox row without any in-memory wakeup.
- Runtime publication completion stores only the opaque Integration receipt reference; it must not query Integration invocation or event tables.
- A metadata active-revision failure must roll back the owner definition,
  version, Audit and current projection together.
- No producer may recreate a generic transaction-intent row; new asynchronous
  boundaries must prove their typed Outbox, Operations or owner-run recovery
  contract.
