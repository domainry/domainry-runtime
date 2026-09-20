# When does a process need Workflow instead of one Operation?

## Problems solved

- Preserves durable process state across people, services, waits, retries, and resumptions that cannot finish inside one transaction.

## Business scenarios

- Routing a purchase request through requester, manager, and finance approval tasks.
- Pausing an onboarding or compliance process until a deadline, external result, or authorized human decision arrives.

## Use when

Use Workflow when a process crosses multiple steps, actors, human tasks, waits, retries, or resumable state.

## Do not use when

Do not use Workflow for one atomic command that can validate and commit inside a single Handler. Do not use it merely to rename CRUD.

## How to use

Define stable step inputs/outputs, task assignees, wait conditions, resume paths, and failure compensation through Runtime's published `workflow.definition` contract.

## Adaptation cookbook

| Business requirement | Adapt with | Concrete implementation | Wrong adaptation |
| --- | --- | --- | --- |
| A purchase request waits for manager and finance decisions. | Workflow with separate human tasks | Define requester submission, assignee resolution, approved/rejected branches, task permissions, and terminal Business Operations. | Two synchronous approve/reject Handlers with no durable waiting process. |
| One onboarding process pauses until a compliance deadline. | Workflow wait/timer | Persist the wait on the existing process and resume the same Workflow instance when due. | Creating one Scheduler definition per onboarding record. |
| Activate an account immediately after one validation. | One Business Operation | Execute the validation and mutation atomically in the Handler. | Creating a Workflow merely to rename a one-step command. |

## Example

Purchase approval with requester submission, manager task, finance task, and a resume after human decisions is Workflow. “Activate account if pending” is one Business Operation.

## Permissions and scope

Starting a Workflow and completing each task require separate exact permissions. Resolve current principals and organizations at execution time; do not trust IDs supplied by the frontend.

## Boundaries

Workflow owns process state and one-time waits. Scheduler owns independent recurrence; Handlers own atomic domain mutations invoked by steps.
