# Runtime After-Commit Hook Contract

> Status: enforced for the transaction foundation on 2026-07-19.
> Authority: [Domainry 后端开发指导](backend-development-guide.md) and [Runtime Cross-Boundary Consistency Contract](runtime-cross-boundary-consistency-contract.md).

After-commit hooks are local coordination optimizations after a successful database commit. They never extend the transaction and never turn a committed business fact into a failed transaction response.

Allowed purposes are limited to:

- waking a worker for durable work already committed in an Outbox or Intent;
- invalidating a process-local cache whose source of truth is durable;
- refreshing a local projection when a durable intent or polling path can rebuild it.

The following work is forbidden in an after-commit hook: email or Webhook delivery, external Provider/API calls, authoritative business mutations, and file publication. Those operations require Outbox, durable intent, staged-file protocol, provider idempotency, reconciliation, or explicit compensation.

Every hook declares `DurableRecovery=true` and a supported purpose before the transaction can register it. Hooks registered by a rolled-back or retried attempt are discarded. Only hooks belonging to the committed attempt run.

If a hook fails:

1. the database commit remains successful and the Unit of Work does not retry the business transaction;
2. the failure is recorded with hook name, purpose, correlation ID, and cause;
3. durable polling or reconciliation remains responsible for eventual execution;
4. operators may alert on the recorded failure, but must not reinterpret it as a rollback.

## Durable async boundary mapping

| Effect | Durable fact before dispatch | Post-commit behavior |
| --- | --- | --- |
| Email and Webhook delivery | `integration_outbox_messages` staged as a source-owned Action durable intent or by record automation | workers claim and deliver; an inline hook may only wake the worker |
| Other asynchronous Connector/API work | `integration_outbox_messages` with a stable `request_ref` | workers retry from durable state with provider idempotency |
| Asynchronous Workflow | `_workflow_executions(status=pending)` stored in the same record/action mutation commit | the fast path must first claim the pending execution; polling remains the recovery path |
| Explicit synchronous source-owned Action Connector call | `integration_invocations(status=prepared)` persisted before the Provider call | terminal outcome, reconciliation, and compensation evidence are persisted; this is not an after-commit hook |

Application mutation services must prepare Outbox messages and Workflow intents before `CommitRecordMutation`/`CommitRecordMutationBatch`. They may request post-commit processing only after the commit succeeds. Direct email, Webhook, Provider/API, or Workflow execution is forbidden before the durable fact commits.

## File publication

The local file-storage adapter writes new content to a same-directory `.domainry-stage-*` file, applies permissions, flushes it, and uses atomic rename as the commit marker. Readers therefore observe either the previous complete file or the new complete file. A failed write/flush/rename removes its stage, and every later write cleans stages older than 24 hours so process crashes have a recoverable cleanup path. File publication is never an after-commit hook.

## Lost wakeup recovery

Worker wakeup is optional. Runtime starts recurring Integration Outbox, Integration event, Workflow, Notification publication, and metadata reconciliation loops. Each loop queries durable due state on startup and on every interval. A process crash or lost in-memory wakeup after commit therefore delays work by at most the polling interval; it does not erase the work. Integration polling selects queued/due or expired-lease Outbox rows, while Workflow polling selects durable pending/due executions and claims them with compare-and-swap/fencing semantics.

Conversely, a transaction that fails during its final commit must expose no executable durable work. Outbox/Intent rows share the business transaction: callback errors, optimistic conflicts, deferred constraint failures, cancellation, and commit errors roll them back together. Workers query only committed rows and therefore cannot claim work from a failed transaction.
