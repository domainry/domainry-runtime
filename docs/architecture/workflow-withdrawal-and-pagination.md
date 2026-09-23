# Workflow withdrawal and process pagination

This change adds two framework capabilities. Business ownership stays with the project: the project saves withdrawal receipts, releases document occupancy, and applies its own eligibility and reapplication rules.

## Transactional withdrawal

An Action declares `handler.workflows.<workflow_key>: ["withdraw"]`, or `["start", "withdraw"]` when both operations are needed. Plane generates only the declared methods. The generated `Withdraw(ctx, recordID, processID)` fixes the Workflow and Object keys and calls `runtimeext.StageWorkflowWithdrawal`.

Runtime verifies the workspace, Workflow, Object, record, initiator, and waiting process revision. It stages cancellation with the Action's canonical business mutations. Committing compares the observed process revision, cancels every active task, node, route step and execution without a list limit, and writes one command event. A receipt contains `ProcessID`, `CommandID`, and `WithdrawnAt`; it becomes durable with the Action. A failed business write, stale process revision, or event write failure rolls back the whole transaction. Repeating the same Action idempotency key replays its saved result.

Withdrawal is accepted at a durable `waiting` boundary. Starting and running processes must reach that boundary first; cancellation does not undo an external operation already executing. Every approval vote now advances and compares the same process revision, including partial votes. Timer resumption claims the revision and completes its timer node atomically before running the next node. Stale engine writes cannot revive cancelled rows or create a live execution for a cancelled process.

The generic participant `/workflow/processes/{processID}/withdraw` route rejects record-bound processes with `backend.workflow.business_withdrawal_required`. Such processes must use their project withdrawal Action to include the project's receipt and release mutations. Unbound processes use the atomic Workflow withdrawal store. Runtime does not create project receipts or infer project occupancy tables.

The public Runtimeext contract advances to `runtimeext-v39`; generated server SDK artifacts advance to `runtime-domain-sdk-v50`. The new SDK must be delivered with the matching Runtime implementation. Existing published module versions and signed delivery artifacts are not republished by this source change.

## Cursor pagination

`GET /workflow/processes?page_size=200` and the recovery list accept `page_size` from 1 through 200 and return:

```json
{"items": [], "has_more": false}
```

When more authorized rows exist, the response includes `next_cursor`. Pass it as `cursor` with unchanged filters and caller authorization to fetch the next page. There is no overall process-count limit. Requests without `page_size` or `cursor` keep their legacy array and `limit` behavior.

The keyset position follows the existing `created_at DESC, id DESC` order, including equal timestamps. Cursors bind the workspace, user, role, authorization revision, list surface and normalized filters. Every page runs live authorization again; cursors carry no authority. They preserve traversal position rather than freezing mutable Workflow status. New rows above the first page do not shift later pages; changed filters or authorization require a fresh traversal.

The Workflow owner SDK and generated static API reference describe both wire shapes. `RuntimeAdminClient.listWorkflowProcessesPage` exposes the participant page request and response while retaining existing array calls.

## Verification

Regression coverage includes business/Workflow rollback on event failure, Action receipt replay, initiator checks, rejected native business bypass, concurrent approval/withdrawal with one winner, cancellation of more than 500 pending tasks while preserving history, timer and late-worker fencing, 501 authorized processes across timestamp ties, workspace and caller isolation, cursor/filter/revision checks, and client query serialization.

Validation uses temporary local module overrides for the changed Runtime source and the SDK source required by the repository's concurrent Agent changes. Repository `go.mod` files and delivery artifacts remain untouched.
