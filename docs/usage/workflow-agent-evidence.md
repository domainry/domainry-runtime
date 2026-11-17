# Reading Agent execution evidence in a Workflow Action

A record Action can opt into one named Workflow's
`runtimeext.WorkflowReadAgentTaskEvidenceOperation` through its existing
`HandlerDescriptor.Workflows` grants. In the Handler binding, call
`runtimeext.ReadWorkflowAgentTaskEvidence` with that Workflow key, its source
Agent node ID, and exact Task key/version.

Runtime derives Workspace, process, current Action node, target Object/record
and execution identity from the authorized Workflow invocation. HTTP, Agent
and unrelated Workflow callers cannot supply a process/run ID to this API.
Reads are allowed only before the Action's first write. Start/withdraw grants
never imply evidence access.

The `record_owner` target-organization capability may lock the target record
before the Handler starts. That metadata resolution keeps the logical prewrite
phase open for this local, trusted Workflow snapshot read. The physical Action
transaction is retained, so synchronous external Connector calls remain
forbidden. The first Handler write or locking record query still closes the
evidence-read window; committed and rolled-back executions cannot read it.

The Workflow owner retains a redacted completion snapshot in the source node,
atomically with terminal node state and its continuation. Request data cannot
seed this reserved snapshot. The reader uses the latest node iteration and
checks its exact Task, actor, target and terminal identity before returning it.
A failed preparation with no completed Owner Task has `Available=false`; no
receipt is invented. Missing/corrupt or out-of-scope evidence fails visibly.

The projection contains the real Task run reference, lifecycle/outcome, model,
Agent/Task identity and bounded tool receipt/status/input-output hashes. Times
are UTC Unix milliseconds. It excludes model input/output, provider payloads,
credentials, usage payloads and permission bundles. It proves execution only;
it does not certify a model conclusion or a customer's access to Knowledge.

Product code may persist this projection alongside its business result in the
same Action transaction. It must continue to enforce its own Case revision,
valid outcomes and visibility policy. Historical runs without this snapshot
are not backfilled or treated as evidence-complete.

## Record-scoped execution status

Project HTTP can use `runtimeengine.RecordWorkflowReader.ReadRecordWorkflow`
with an exact object, record and Workflow key when a result Action could not
commit. This separate read projection returns only whether one process exists,
its reference, lifecycle, safe error code and UTC Unix-millisecond instants.
It never exposes variables, definitions, Task input/output or recovery commands.

The current identity must have read access to the target object and record.
Runtime repeats that check after the bounded process lookup; ordinary record
readers do not need Workflow operations permissions. Workspace, object, record
and Workflow key must all match the process. Two matching processes produce an
ambiguity error, not an arbitrarily selected attempt. Missing state is reported
as `Found=false`; read failures must not be interpreted as execution failures.

Consumers keep persisted business state separate from execution state. A failed
process may coexist with a pending result record, and a successful Task does not
prove its result Action committed. This API cannot rerun a Task, repair state,
route work, or authorize any administrative recovery operation.

## Preparation failures

The additive `runtimeext-v66` contract distinguishes a failed Task preparation
from a Native Task completion. A persisted source node stopped during dispatch
may return `Available=false` with `PreparationFailure`: exact Workflow/process,
node and planned Task identity, safe error code and UTC Unix-millisecond stop
time. There is no Task run reference, model, tool ledger or completion outcome.
This records a preparation stop without claiming a Native Task was created or
completed; an uncertain start still requires reconciliation by its owner.

Only the source Task node's declared `OnError=error_branch` continuation may
read this projection. Current Workflow Action/target/workspace checks still
apply. Missing/corrupt stop metadata or a successful/foreign continuation fails
closed. Task dispatch errors and invalid start results follow that error branch
only after the failed node was durably saved. Failure persistence errors and
`OnError=fail` continue to fail the Workflow; no result write is fabricated.
