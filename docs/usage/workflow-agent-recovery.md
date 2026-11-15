# Recovering a failed Agent continuation

An operator's retry command does not replace the execution identity of a durable Agent completion. Automatic continuations already resolve that identity through Identity; process retries now use the same live resolution.

For a process whose latest execution is an `agent_task_terminal` continuation, Runtime validates the exact Workspace/process/workflow/object/record binding, terminal unleased execution, original run/node/task/version, immutable completion proof and latest task iteration. The recorded resume nodes must match the original graph outcome, and the failed node must be reachable through automatic nodes without crossing a human approval, another Agent task or a suspension boundary. Process variables and request fields never supply execution authority. Missing or invalid Agent provenance is rejected before the retry mutates the process.

The operator remains the `process_retry_started` actor. The event separately records the resolved execution user and role. Identity resolves current access, and each Action independently enforces that Principal's current permission and record scope. No old permission bundle is restored, and a revoked identity never falls back to the operator.

A completed retry synchronizes the exact continuation execution with the process under its existing status/lease/fencing condition. It clears obsolete dead-letter/backoff metadata; it does not run the completed Agent task again. Replaying the same retry key returns the existing result. Ordinary retries without an Agent continuation retain their existing caller/run-as behavior.

Regression coverage includes error-branch recovery, operator audit, idempotency, inactive/foreign identity, directory errors, revoked Action permission, wrong provenance, newer task iteration, active leases, disconnected nodes and approval barriers. This does not establish general concurrent process-command atomicity or recovery across every suspension type; those remain governed by their own continuation contracts.
