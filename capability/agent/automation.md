# When should automatic behavior use Automation?

## Problems solved

- Expresses a deterministic lifecycle reaction once at the Runtime boundary without hiding it in browser hooks or project-local event plumbing.
- Separates record-triggered Automation from independent recurring Scheduler windows that invoke a governed Business Operation.

## Business scenarios

- Publishing one fulfillment event after an order commit.
- Applying a bounded before-save rule or dispatching an after-commit reaction tied to a known Runtime lifecycle event.
- Running an overdue-order Business Operation every 15 minutes under a published service role.

## Use when

Use Automation for a deterministic reaction attached to a Runtime lifecycle event, with an explicit trigger and bounded action.

## Do not use when

Do not use Automation for user commands, multi-step waiting processes, recurring calendar work, or hidden business rules that should be validated atomically.

## How to use

Define trigger timing, condition, action, idempotency, and failure policy through the published `automation.rule` authoring contract.

For independent recurring calendar work, define `scheduler.schedule` plus `scheduler.business_job` with `target_type=business_action`. Supply the exact Action key, Object key, JSON object payload, and a `service` / `system_managed` `run_as_role` that owns the Action permission. Runtime resolves a managed workload principal and invokes the normal Action boundary once per Scheduler window; it does not run the Handler directly or silently elevate to system authority.

If occurrences must honor working dates, reference a published Business Calendar and choose `skip` or `roll_forward`. The calendar applies to Scheduler occurrence dates; it is not an Automation condition and does not belong inside the target Handler.

## Adaptation cookbook

| Business requirement | Adapt with | Concrete implementation | Wrong adaptation |
| --- | --- | --- | --- |
| Publish one fulfillment event after an order commit. | After-commit Automation reaction | Bind the reaction to the committed order lifecycle event, make delivery idempotent, and preserve the successful order write if later delivery fails. | Calling a provider before commit or duplicating the event hook in project code. |
| Reject one write when a deterministic pre-save invariant fails. | Before-save Automation only when the published contract owns that rule | Evaluate the bounded condition before persistence and reject the same write without partial effects. | Using an asynchronous callback for an invariant that must be atomic. |
| Retry work every hour or wait for a manager. | Scheduler or Workflow | Use Scheduler for independent recurring windows and Workflow for human or resumable waiting. | Hiding recurrence or long-running state in Automation. |
| Run one Business Operation on every Cron window. | Scheduler `business_action` | Publish a `scheduler.business_job`, bind the exact Action/Object/service role, and keep the window idempotency identity stable across retries. | Calling a Handler directly, using an administrator principal, or implementing recurrence with `RunBusinessJob`. |
| Do not run a daily operation on holidays. | Scheduler Business Calendar | Reference the versioned calendar and select `skip`, or use `roll_forward` when the occurrence must move to the next working date. | Evaluating holidays after the run is created or adding project calendar callbacks. |

## Example

“After an order is committed, publish one fulfillment event” may be Automation if delivery is a lifecycle reaction. “Approve order” remains a Business Operation; “retry every hour” belongs to Scheduler.

## Permissions and scope

Automation runs as an explicit bounded service authority. Scheduler `business_action` runs as its published managed service-role workload. Neither may silently elevate to administrator or bypass the Workspace boundary.

## Boundaries

Before-save invariants belong in the atomic Handler when possible. Workflow owns long-running state; Scheduler owns recurrence. `RunBusinessJob` is a one-shot durable timer created from an Action and is not a recurring Scheduler substitute.
