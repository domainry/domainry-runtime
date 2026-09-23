# When does a process need Workflow instead of one Operation?

## Problems solved

- Preserves durable process state across people, services, waits, retries, and resumptions that cannot finish inside one transaction.
- Resolves approval, CC, and escalation recipients from variables, records, bounded relations, manager chains, roles, or governed project resolvers with durable evidence.

## Business scenarios

- Routing a purchase request through requester, manager, and finance approval tasks.
- Selecting reviewers from a related department record or a bounded project-specific resolver.
- Pausing an onboarding or compliance process until a deadline, external result, or authorized human decision arrives.
- Freezing a validated per-instance approval route at start when this request legitimately differs from the template default.

## Use when

Use Workflow when a process crosses multiple steps, actors, human tasks, waits, retries, or resumable state.

## Do not use when

Do not use Workflow for one atomic command that can validate and commit inside a single Handler. Do not use it merely to rename CRUD.

## How to use

Define stable step inputs/outputs, task assignees, wait conditions, resume paths, and failure compensation through Runtime's published `workflow.definition` contract.

### Assignee resolution contract

Approval, CC, and escalation share the same ordered resolver model. Runtime snapshots the selected user's role, resolver key, and source evidence when the node is activated; later organization changes do not rewrite an existing task electorate.

| Resolver | Use |
| --- | --- |
| `users` / `role` | Select published users or every active user in one role. |
| `variable_user` | Read user IDs from the immutable process variables. |
| `record_user_field` | Read a user field from the process business record through the bounded Workflow Record Reader. |
| `relation_user` | Follow at most five declared business relation fields; the final field must resolve Identity users. |
| `relation_role` | Follow at most five declared business relations, read role keys from the terminal record, and resolve active users in those roles. |
| `manager_chain` | Starting from the initiator, a process variable, or a record user field, resolve an active manager chain up to the declared `max_depth`. |
| `project` | Invoke an installed project resolver by `resolver_key`; `config` must match the descriptor disclosed in `instance.assignee_resolvers`. |

Project resolvers are build-time extensions, not tenant code. Their descriptors disclose config fields, record/relation grants, Identity projections, candidate roles, read/candidate budgets, and timeout. Runtime rejects undeclared reads, invalid candidates, budget overruns, and unpublished resolver keys. Declaring a candidate Role only grants the resolver permission to return that Role; Runtime still verifies that each returned active user is actually assigned to it.

For a deadline expressed in working time, reference a published `business_calendar_key` and declare the business-duration offset. Runtime resolves the versioned calendar, adds time only inside its working intervals, and persists the exact calendar revision with the Timer. Use an absolute or 24x7 timer when working-calendar semantics are not part of the requirement.

## Adaptation cookbook

| Business requirement | Adapt with | Concrete implementation | Wrong adaptation |
| --- | --- | --- | --- |
| A purchase request waits for manager and finance decisions. | Workflow with separate human tasks | Define requester submission, assignee resolution, approved/rejected branches, task permissions, and terminal Business Operations. | Two synchronous approve/reject Handlers with no durable waiting process. |
| A contract reviewer comes from its owner field or related region. | `record_user_field`, `relation_user`, or `relation_role` resolver | Read only declared record/relation paths, cap traversal at five segments, and require the terminal value to resolve to active Identity users or Roles. | Storing long-lived user IDs in the definition or letting a resolver query arbitrary tables. |
| Escalate to the initiator's manager chain. | `manager_chain` resolver | Choose the allowed start source and `max_depth`; if no active manager is found, apply the declared empty-assignee failure policy. | Falling back silently to an administrator or confusing organization parent with personnel manager. |
| One request chooses its approved route at submission. | Validated per-instance route snapshot | Validate allowed nodes/roles during the start transaction and freeze the selected route as instance evidence. | Reading a mutable route field on every step or letting a later template change rewrite a running instance. |
| One onboarding process pauses until a compliance deadline. | Workflow wait/timer | Persist the wait on the existing process and resume the same Workflow instance when due. | Creating one Scheduler definition per onboarding record. |
| An approval is due after eight office hours excluding holidays. | Workflow business-calendar timer | Reference the published calendar key and working-duration offset; keep the resolved revision on the Timer. | Adding eight elapsed hours or resolving the latest calendar again when the Timer fires. |
| Activate an account immediately after one validation. | One Business Operation | Execute the validation and mutation atomically in the Handler. | Creating a Workflow merely to rename a one-step command. |

## Example

A purchase approval starts from `purchase_request.submit`, creates a manager Approval resolved from the initiator's manager chain, follows `approved` to a finance Approval resolved from Role `finance_reviewer`, follows either rejection to a terminal rejected node, and on final approval invokes `purchase_request.settle`. Each task has its own decide permission; start permission does not imply either approval. The definition also declares an empty-assignee failure policy and an eight-working-hour timer bound to a published Business Calendar revision.

Runtime snapshots the selected candidates and resolver evidence when each node activates. A duplicate decision returns the prior durable outcome; concurrent opposing decisions allow one versioned winner; an Action failure follows the declared retry/dead-letter path; recovery resumes from durable node evidence without invoking the Action twice. A request-specific route, when allowed, is validated and frozen in the start transaction. None of this creates a `schema.state_machine`: no such public authoring contract is currently disclosed.

## Permissions and scope

Starting a Workflow and completing each task require separate exact permissions. Resolve current principals and organizations at execution time; do not trust IDs supplied by the frontend.

## Boundaries

Workflow owns process state and one-time waits. Scheduler owns independent recurrence; Handlers own atomic domain mutations invoked by steps. A later Business Calendar revision cannot rewrite an existing Timer's due time.
