# When should automatic behavior use Automation?

## Problems solved

- Expresses a deterministic lifecycle reaction once at the Runtime boundary without hiding it in browser hooks or project-local event plumbing.

## Business scenarios

- Publishing one fulfillment event after an order commit.
- Applying a bounded before-save rule or dispatching an after-commit reaction tied to a known Runtime lifecycle event.

## Use when

Use Automation for a deterministic reaction attached to a Runtime lifecycle event, with an explicit trigger and bounded action.

## Do not use when

Do not use Automation for user commands, multi-step waiting processes, recurring calendar work, or hidden business rules that should be validated atomically.

## How to use

Define trigger timing, condition, action, idempotency, and failure policy through the published `automation.rule` authoring contract.

## Adaptation cookbook

| Business requirement | Adapt with | Concrete implementation | Wrong adaptation |
| --- | --- | --- | --- |
| Publish one fulfillment event after an order commit. | After-commit Automation reaction | Bind the reaction to the committed order lifecycle event, make delivery idempotent, and preserve the successful order write if later delivery fails. | Calling a provider before commit or duplicating the event hook in project code. |
| Reject one write when a deterministic pre-save invariant fails. | Before-save Automation only when the published contract owns that rule | Evaluate the bounded condition before persistence and reject the same write without partial effects. | Using an asynchronous callback for an invariant that must be atomic. |
| Retry work every hour or wait for a manager. | Scheduler or Workflow | Use Scheduler for independent recurring windows and Workflow for human or resumable waiting. | Hiding recurrence or long-running state in Automation. |

## Example

“After an order is committed, publish one fulfillment event” may be Automation if delivery is a lifecycle reaction. “Approve order” remains a Business Operation; “retry every hour” belongs to Scheduler.

## Permissions and scope

Automation runs as an explicit bounded service authority. It must not silently elevate to administrator or bypass the triggering record’s Workspace boundary.

## Boundaries

Before-save invariants belong in the atomic Handler when possible. Workflow owns long-running state; Scheduler owns recurrence.
