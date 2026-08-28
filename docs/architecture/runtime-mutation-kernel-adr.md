# Runtime Mutation Kernel ADR

Status: accepted for direct pre-release migration.

## Decision

Runtime uses one owner-neutral Mutation Kernel for business record correctness. A Business Action is a named command boundary. Direct CRUD is available only for objects whose published write policy explicitly permits simple maintenance. Workflow, Automation, Scheduler, Integration, Agent tools, State Machine and Pipeline may authorize, schedule or orchestrate work, but they do not own a second normalization, validation, Audit, Outbox or commit implementation.

The target execution chain is:

```text
Entrypoint authorization
  -> immutable MutationContext
  -> PlanCreate / PlanUpdate / PlanDelete / PlanRestore
  -> canonical MutationPlan or MutationPlanBatch
  -> CommitMutationPlan / CommitMutationBatch
  -> persisted Audit + Outbox + Workflow/Event Intent + receipt
  -> post-commit dispatch
```

Planning owns object resolution, defaults, normalization, RLS/CLS/field policy, relation/domain/unique/lifecycle checks, conditional predicates and declared effect authority. Commit owns the unit of work. Post-commit consumers may retry persisted intents but may not repair missing strongly consistent business facts.

## Automation boundary

Before Automation is restricted to deterministic, replayable validation and candidate derivation. It may not invoke Actions, Workflows, Connectors, immediate messages or nested commits. Rules that must hold for every entrypoint move into Definition or Domain policy.

After Automation consumes a committed event or Outbox fact. Downstream Action or Workflow invocation uses a new idempotency scope, an explicit identity snapshot/revocation policy, causation and correlation identifiers, loop detection, maximum depth and recoverable retry.

## Pre-release migration

This repository has not been deployed to production, so Runtime does not preserve legacy execution semantics. The machine-readable inventory at `runtime/boundary/testdata/mutation_entrypoint_inventory_v1.json` identifies duplicate owners so they can be removed. Repository fixtures and tests migrate in the same change. Facades may preserve a stable use-case shape, but fallback planners, validators, committers and dual execution branches are forbidden. A new strong-consistency write capability may not be added to an unregistered entrypoint.

Internal projection writes require an explicit object allowlist and system scope. This is a narrow exception, not a generic repository bypass.

## Consequences

- Single-record CRUD becomes a one-plan commit; atomic Action batches become a multi-plan single commit.
- Workflow business nodes invoke published Actions.
- Simulation and execution consume the same planner output.
- Architecture tests reject missing inventory owners, unknown entrypoints and unreviewed repository bypasses.
