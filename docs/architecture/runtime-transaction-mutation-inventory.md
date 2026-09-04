# Runtime Transaction Mutation Inventory

> Status: P0 scope inventory established on 2026-07-19.
> Authority: [Domainry 后端开发指导](backend-development-guide.md) and [Runtime Cross-Boundary Consistency Contract](runtime-cross-boundary-consistency-contract.md).

## Scope rule

This inventory records the top-level Runtime mutation use-case families that need an explicit consistency decision. Public facade aliases are collapsed into one row; internal helpers are listed only when they own an independently recoverable mutation. Queries, validation, preview, simulation, worker configuration, and process startup are excluded.

The inventory is evidence of scope only. Read/write sets, locking, versions, Audit, Workflow Intent, Outbox, side effects, and consistency classification are recorded by the subsequent P0 tasks and must not be inferred from this table.

## Mutation use-case inventory

| Family | In-scope mutation entrypoints | Detail profiles | Current owner/source | Inventory finding |
| --- | --- | --- | --- | --- |
| Record CRUD | `CreateRecord`, `CreateRecordIdempotentResult`, `UpdateRecord`, `DeleteRecordExpected`, `RestoreRecord` | T01-T03 | `application/record/record_application_service.go` plus create/update/delete/restore services | Create, update, delete, and restore are all in scope; non-expected delete remains an alias that also needs a consistency decision. |
| Action | `Invoke` (HTTP record/object wrappers delegate to it) | T04 | `application/action/action_application_service.go` | One invocation path resolves a fixed Catalog owner, applies authorization/payload/assurance/idempotency governance, and delegates to either a closed System Operation or registered Business Handler. |
| Bulk | `ExecuteBulkAction` | T05 | `application/action/action_application_service.go`, `action_bulk_application_service.go` | Bulk is not folded into a single-record Action because row-level progress and partial failure are independent consistency concerns. |
| Import | `ApplyImport`, `ApplyImportIdempotent` | T06 | `application/record/record_import_facade.go`, `record_import_application_service.go` | Apply is in scope; preview is read-only and excluded from the mutation inventory. |
| Workflow decision | `DecideTask`, `CancelWorkflowProcessWithKey`, `RetryWorkflowProcessWithKey`, `ResolveWorkflowProcessFailure`, `RetryWorkflowExecutionWithKey`, `ResolveWorkflowExecution` | T07-T08 | `application/workflow/workflow_process_application_service.go`, `workflow_execution_application_service.go` | Human task decisions and operator recovery commands mutate related process/execution facts and require individual decisions. |
| Metadata publish | `UpsertMetadataDefinition`, `DisableMetadataDefinition`, `RollbackMetadataDefinitionIdempotent`, `UpsertLocalizedText`, `ReloadMetadata` | T09-T11 | `application/metadata/metadata_definition_orchestration_application_service.go`, `metadata_application_service.go` | Definition publication/lifecycle and localized-text publication are in scope; validate/list/read paths are excluded. |
| ChangePlan apply/rollback | `SaveDraft`, `ApplyIdempotent`, `Apply` | T12-T13 | `application/changeplan/changeplan_business_change_plan_draft_lifecycle_clone.go`, `changeplan_business_change_plan_apply_export.go` | Apply is present. No ChangePlan rollback use case exists in the current Application adapter; this is an explicit missing entrypoint, not silently represented by Metadata rollback. |
| Identity | `UpsertDepartment`, `UpsertUser`, `RemoveUser`, `SetUserStatus`, `UpsertRole`, `RemoveRole`, `SetRoleStatus`, `AssignUserRole`, `RemoveUserRole`, `CreateRoleRequest`, `ApproveRoleRequest`, `RejectRoleRequest`, `SetRolePermissions`, `SetRoleDataScopes`, `SetRoleFieldPermissions`, `UpsertMenu`, `RemoveMenu`, `SetRoleMenus` | T14-T15 | `application/identity/identity_application_service.go` embeds `domain/identity/service`; mutation implementations are under `domain/identity/service` | Identity is a multi-aggregate command family. The embedded Domain service adapter is the current Application entrypoint and is included rather than treated as an exemption. |
| Notification | `SaveDraft`, `RestoreVersionDraft`, `Publish`, `Disable`, `SaveDeliveryPolicy`, `SaveRecipientPreference`, `RequestPublication`, `ApprovePublication`, `RejectPublication`, `CancelPublication`, `ProcessDuePublications` | T16-T18 | `application/notification/notification_application_service.go` embeds `domain/notification/service` | Template lifecycle, delivery policy/preferences, approval flow, and due-publication worker mutations are all in scope. |
| Scheduler command | Scheduler service owns definition/clock/run/retry/cancel/dead-letter entrypoints | T19-T20 | `domainry-scheduler` / `domainry-scheduler-sdk`; Runtime host wiring is `bootstrap/composition/scheduler_sdk_module_host_wiring.go` | Runtime exposes no scheduling command. Its separate `application/dispatch` port accepts only a fully resolved target execution. |
| Integration event | Integration SDK `Operations.AcceptWebhook` and `LocalWorkers.ProcessDueEvents` | T21-T22 | `github.com/domainry/domainry-integration/internal/infrastructure/persistence/database/integration` | Integration atomically owns acceptance, event/mapping intent, claim/retry and receipt evidence; target Action/Workflow execution crosses the Runtime `TriggerSink`. |

## Current consistency detail profiles

The following profiles record the current implementation, including missing facts. `none observed` means the current call path does not wire that fact; it is not a waiver. “Lock/version” names the current guard, not the desired final contract.

| ID | Entrypoints | Read set | Write set | Lock/version | Audit | Workflow Intent | Outbox | External or post-commit side effect |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| T01 | `CreateRecord`, `CreateRecordIdempotentResult` | object schema, permission/policy facts, relation and uniqueness lookups, optional receipt | record row, receipt, Audit, prepared Workflow executions, Integration outbox rows | unique constraints plus receipt claim; no record version on create | built into `RecordMutationCommit` | prepared before commit and persisted in the commit | built into the commit | prepared workflows execute after commit; identity projection sync occurs after the record commit |
| T02 | `UpdateRecord` | object schema, current record, relation/unique/policy facts | updated record, Audit, Workflow executions, outbox rows, state-machine related mutations | optional `expected_updated_at` checked against the read record; repository commit is the final guard | built into `RecordMutationCommit` | prepared before commit and persisted in the commit | built into the commit | workflows and non-self state-machine effects run after the primary commit |
| T03 | `DeleteRecordExpected`, `RestoreRecord` | object schema, current/deleted record, reference and policy facts | delete/restore state, Audit, Workflow executions/outbox where configured | delete accepts expected updated-at; restore currently relies on repository semantics | mutation commit/audit path | record lifecycle triggers may prepare intents | lifecycle outbox where configured | committed workflows execute after commit |
| T04 | `Invoke` | fixed Action Catalog entry, permission/object scope, normalized payload, assurance, receipt, Handler queries | System Operation mutation or atomically committed Business Handler mutation batch, Action receipt, Audit and DurableIntent/Outbox rows | Action idempotency claim; canonical Record mutation optimistic/predicate guards | canonical Record mutation Audit plus Action evidence | generated Handler capabilities do not expose a second workflow/event channel | `StageDurableIntent` joins the Handler mutation batch and persists through the existing Outbox table at commit | post-commit workers consume the single durable channel; no direct provider call or Action runtime fallback |
| T05 | `ExecuteBulkAction` | action definition, each target record through the same `Invoke`, bulk receipt | per-row T04 facts, bulk result receipt, bulk Audit | operation receipt plus deterministic row keys; row expected versions optional | one bulk Audit plus row Audits | inherited per row | inherited per row | inherited per row; current loop permits earlier rows to commit before a later failure |
| T06 | `ApplyImport`, `ApplyImportIdempotent` | object schema, CSV, relation/unique lookups, import receipt | one created record transaction per row, row receipts, operation receipt, import Audit | operation receipt and deterministic row key in idempotent path; no all-file lock | import-level Audit plus record Audits | inherited from each record create | inherited from each record create | record create post-commit work runs per row; current import is resumable rather than one database transaction |
| T07 | `DecideTask` | task, process, workflow definition, current node/cursor, referenced records, principal | task decision, process/node cursor, record mutations, Audit and notifications produced by the decision | task/process status checks; no single documented expected-version contract yet | decision/process Audit | follow-up execution facts may be created | notification/integration delivery may enqueue | notification dispatch and continued process execution can occur after local writes |
| T08 | `CancelWorkflowProcessWithKey`, `RetryWorkflowProcessWithKey`, `ResolveWorkflowProcessFailure`, `RetryWorkflowExecutionWithKey`, `ResolveWorkflowExecution` | current process/execution, workflow definition, receipt | process/execution status, retry/resolve evidence, receipt, Audit | caller-key receipt for keyed variants; status transition guards otherwise | operator action Audit | retry may create execution work | none observed in facade | worker/process continuation is asynchronous after command state changes |
| T09 | `UpsertMetadataDefinition`, `DisableMetadataDefinition` | current definition, validation/reference graph, current manifest/snapshot | definition/current version/history, Audit, active catalog revision, Runtime refresh intent | repository expected/current revision semantics; publication uses one serializable commit | publication Audit is stored in the same commit; disable Audit path remains separate | durable `metadata/runtime_refresh` boundary intent | none observed | Runtime metadata is applied after commit; failure moves the durable intent to reconciliation-required |
| T10 | `RollbackMetadataDefinitionIdempotent` | current definition, version history, reference graph, receipt | rolled-back definition/version, active snapshot, Audit, receipt | operation receipt plus requested target version/current definition checks | rollback Audit supplied to repository | none | none observed | Runtime metadata apply occurs outside the definition repository write |
| T11 | `UpsertLocalizedText`, `ReloadMetadata` | localized-text key/current value or stored manifest | localized text, or synchronized metadata tables/snapshot | natural key for localized text; reload has no caller expected version | no explicit Application Audit observed for localized text/reload | none | none | in-memory/runtime metadata refresh follows persistence/sync |
| T12 | `SaveDraft` | current draft and revision | change-plan draft/revision | `expectedRevision` compare-and-swap | none observed | none | none | none |
| T13 | `ApplyIdempotent`, `Apply` | plan/draft, snapshot, reference graph, receipt | business-system/metadata changes through delegated operations, published draft, receipt and associated Audits | operation receipt plus draft revision/publish guard | delegated mutation Audits; no single enclosing Audit/UoW | delegated operations may prepare intents | delegated operations may enqueue | apply spans delegated operations; current process can expose partial progress. ChangePlan rollback is absent |
| T14 | Identity department/user/role/menu CRUD and status methods | current identity row, hierarchy/reference facts | department, user, role or menu rows; manager paths where applicable | repository uniqueness; no uniform expected version on all commands | HTTP handler currently appends Audit after service mutation | none | none observed | auth/session invalidation and record path rebuild can be separate operations |
| T15 | Identity role assignment, policy/menu assignment and role-request methods | user/role/request plus current assignment/policy sets | assignments, policy/data-scope/field/menu sets, request decision | natural/composite keys and status checks; no shared UoW | handler/service Audit occurs separately | none | none observed | approval may require follow-up authorization/session refresh outside the write |
| T16 | `SaveDraft`, `RestoreVersionDraft`, `Publish`, `Disable` | current template/version and expected updated-at | template current row, version/history, publication state | explicit `expectedUpdatedAt` on template lifecycle commands | repository/domain audit evidence where configured; not Application-owned | none | notification delivery store, not Integration outbox | `RefreshPublished` updates runtime cache after publication changes |
| T17 | `RequestPublication`, `ApprovePublication`, `RejectPublication`, `CancelPublication`, `ProcessDuePublications` | template, publication request, approval/status and due-time facts | publication request/status, template publication/version on approval/due processing | status transition checks; due processing has no documented lease/fencing in this inventory | publication actor/reason retained; separate Audit contract requires adjudication | none | delivery may be created by publication | due worker refreshes published state and may continue delivery work after writes |
| T18 | `SaveDeliveryPolicy`, `SaveRecipientPreference` | current policy/preference | policy or recipient preference | natural key; no expected version observed | actor retained, separate Audit contract not observed | none | none | none |
| T19 | Scheduler submit/retry/cancel/dead-letter commands | Scheduler-owned definition/run/dead-letter facts | Scheduler-owned run, event, retry/cancel/dead-letter state and receipt | Scheduler-owned idempotency and status transition guards | Scheduler run-event history | callback may start Runtime-owned downstream work | Scheduler-owned delivery | implemented and verified in `domainry-scheduler`; Runtime owns no copy of these writes |
| T20 | `ClaimRun`, `HeartbeatRun`, `AppendRunEvent`, `FinishRun`, `ProcessClaimedRun` | definition/run plus lease owner/token and workflow executions | lease/run state, run events, retry/dead-letter schedule, workflow execution state | lease owner, expiry and fencing/conditional update | run-event history is the worker evidence | process execution updates/creates workflow facts | none observed | source-owned Scheduler worker lifecycle; Runtime owns no copy of these writes |
| T21 | Integration `AcceptWebhook` | Connector verifier, event mapping schema, external event identity | Integration-owned event and mapping-intent row | unique external event identity and webhook replay evidence | owner-local acceptance evidence | durable mapping intent records mapping key/target | none before acceptance | implemented in `domainry-integration`; Runtime owns no copy of these writes |
| T22 | Integration `ProcessDueEvents` | due event, lease/fencing token and mapping intent | Integration event lease/status/retry and Runtime execution receipt reference | Integration lease owner and fencing token; Runtime target idempotency key | Integration-owned execution receipt | consumes the accepted mapping route | SDK `TriggerSink` executes Runtime Action/Workflow and may create Runtime publication handoff | cross-owner execution is compensatable/eventual and cannot share one SQL transaction |

## Consistency classification

Classification is attached to the whole use case, not to an individual repository call:

- `atomic_required`: the listed local database facts must commit or roll back together.
- `eventually_consistent`: a durable local fact is the boundary and downstream work may complete later.
- `compensatable`: the use case spans a boundary that cannot share the local database transaction and therefore needs durable reconciliation/compensation.
- `read_only`: no mutation is allowed; included below only to make exclusions explicit.

Secondary classifications describe the downstream portion after the primary boundary. They do not weaken an `atomic_required` local commit.

| Profile/entrypoint | Primary classification | Secondary classification | Decision |
| --- | --- | --- | --- |
| T01 Record create | `atomic_required` | `eventually_consistent` | record + Audit + Workflow Intent + Outbox + receipt are one local fact; execution/projection follows commit |
| T02 Record update | `atomic_required` | `eventually_consistent` | record and locally derived facts are indivisible; workflow/state-machine continuation follows commit |
| T03 Record delete/restore | `atomic_required` | `eventually_consistent` | lifecycle state and local evidence are indivisible; triggered work follows commit |
| T04 Action | `atomic_required` | `eventually_consistent`, `compensatable` | local action facts are atomic; deferred work is eventual and any direct provider step needs compensation/reconciliation |
| T05 Bulk | `eventually_consistent` | `compensatable` | each row is an atomic unit but the batch is resumable; already committed rows are not rolled back as one database transaction |
| T06 Import | `eventually_consistent` | `compensatable` | each row is atomic and replayable; operation completion reconciles partial progress |
| T07 Workflow task decision | `atomic_required` | `eventually_consistent` | task + process/node cursor + local record/Audit facts are one decision; next-node delivery can follow commit |
| T08 Workflow recovery commands | `atomic_required` | `eventually_consistent` | command transition/receipt is atomic and worker continuation is asynchronous |
| T09 Metadata upsert/disable | `atomic_required` | `eventually_consistent` | definition/version/history/Audit/active revision are one local publication; Runtime refresh retries after commit |
| T10 Metadata rollback | `atomic_required` | `eventually_consistent` | rollback facts and receipt are atomic; Runtime refresh follows commit |
| T11 `UpsertLocalizedText` | `atomic_required` | `eventually_consistent` | localized value is a natural-key write; projection/cache refresh may lag |
| T11 `ReloadMetadata` | `eventually_consistent` | `compensatable` | reload is a recoverable synchronization command over durable manifest/definition facts |
| T12 ChangePlan draft | `atomic_required` | none | expected-revision draft replacement is one local fact |
| T13 ChangePlan apply | `compensatable` | `eventually_consistent` | delegated operations currently span multiple commits; durable apply progress and rollback/reconciliation are required |
| Missing ChangePlan rollback | `compensatable` | none | absence is a blocking contract gap; rollback cannot be classified as an implemented atomic command |
| T14 Identity CRUD/status | `atomic_required` | `eventually_consistent` where session/projection refresh follows | each identity aggregate mutation and Audit must be local-atomic |
| T15 Identity assignments/policies/requests | `atomic_required` | `eventually_consistent` where authorization cache/session refresh follows | assignment set or request decision and Audit are indivisible |
| T16 Notification template lifecycle | `atomic_required` | `eventually_consistent` | template/version/publication fact is atomic; published cache refresh follows commit |
| T17 Notification publication workflow | `atomic_required` | `eventually_consistent` | request decision/template publication is atomic; scheduled processing and delivery follow durable state |
| T18 Notification policy/preferences | `atomic_required` | none | natural-key policy/preference and Audit are one local fact |
| T19 Scheduler caller commands | `atomic_required` | `eventually_consistent` | enforced by source-owned Scheduler; Runtime exposes only the schedule-agnostic `/dispatch/executions` target port and supplies definitions as reconcile input |
| T20 Scheduler worker lifecycle | `atomic_required` | `eventually_consistent` | lease-fenced run/event/retry state is atomic; another worker can recover durable work after crash |
| T21 Integration event acceptance | `atomic_required` | `eventually_consistent` | accepted event + durable mapping intent are one local fact; target-domain execution follows commit |
| T22 Integration event processing | `compensatable` | `eventually_consistent` | lease-fenced processing spans target-domain owners; durable intent/status must drive retry and reconciliation |

### Read-only exclusions

The following neighboring use cases are explicitly `read_only` and therefore remain outside the mutation table: Record export and Import preview; Action, Workflow, Metadata and ChangePlan validate/simulate/preview methods; Integration event/outbox list and get queries; list/get/diff/status/catalog methods across Runtime owners. Scheduler definition preview/validation and all run, event, retry, cancel, dead-letter, claim, heartbeat and finish state are source-owned by `domainry-scheduler`; Runtime must not recreate those queries or mutations. A future write introduced under another read-only name must first move into the mutation inventory.

## Consecutive multi-write entrypoints without a common transaction

This is a source-backed risk inventory. “No common transaction” means the owner invokes two or more independently committing write ports/helpers; a durable state machine may make that recoverable, but it still remains visible here until its consistency decision is implemented and tested.

| Risk ID | Entrypoint | Current write sequence | Evidence | Risk |
| --- | --- | --- | --- | --- |
| MW01 | `ExecuteBulkAction` | commit each row action -> write bulk Audit -> complete bulk receipt | `application/action/action_bulk_application_service.go` | crash can leave committed rows while operation receipt remains processing |
| MW02 | `ApplyImport` / `ApplyImportIdempotent` | create each row in its own Record commit -> complete operation receipt -> write import Audit | `application/record/record_import_application_service.go` | partial import is durable before operation completion; recovery depends on deterministic row keys |
Notification template publication is no longer a Plane-owned mutation window. Its durable state machine, fencing, publication and catalog reconciliation are owned by `github.com/domainry/domainry-notification/template`; Plane only schedules work and adapts authorization/transport contracts.

Already consolidated paths are not listed as violations: Record mutation uses `CommitRecordMutation`, terminal Workflow task decisions use `CommitWorkflowDecision`, and Identity user hierarchy updates use `UpsertIdentityUsersAtomically`. The retired Action JSON Step runtime is no longer a mutation entrypoint.

Resolved high-risk migrations are removed from the unresolved table. Scheduler run lifecycle consistency is enforced by the source-owned Scheduler module in both Module and SaaS topologies; Runtime owns only schedule-agnostic target execution and owner-specific timers such as `record_timer`.

Workflow MW04-MW06 are also resolved. Task-decision recovery and operator failure resolution use `WorkflowStateCommit` to update task/node/process/event/execution facts in one serializable transaction. Public `DecideTask` no longer falls back to the legacy sequential process engine when the transactional decision store is unavailable; it fails closed. Terminal decisions continue through `CommitWorkflowDecision` with task CAS.

Identity MW09-MW10 are resolved through `IdentityAtomicMutationRepository`. Role permission/data-scope/field-permission assignment rows and the denormalized role projection commit in one database transaction. Recursive menu removal first computes the complete child-first set, then removes assignments and soft-deletes the entire set atomically. Domain service methods fail closed when the atomic adapter is unavailable. The remaining Plane-owned unresolved table contains only deliberately resumable or compensatable batch flows (MW01-MW02), each classified as `eventually_consistent` and/or `compensatable` with durable progress/reconciliation evidence.

## Transaction callback side-effect audit

Audit date: 2026-07-19.

| Audit adapter | Result | Evidence/decision |
| --- | --- | --- |
| Application/Domain transaction callbacks | none currently exist | no production `WithinTransaction`, `WithTransaction`, or `InTransaction` contract is present under `runtime/application` or `runtime/domain` |
| Explicit SQL transaction owners | 22 production files | all current `BeginTx` calls are inside `runtime/infrastructure/persistence/database` adapters |
| Network calls inside explicit SQL transaction files | none found | the `BeginTx` file set has no `net/http` or `client.Do` usage |
| File/process side effects inside explicit SQL transaction files | none found | the `BeginTx` file set has no `os.WriteFile`, `os.Create`, `os/exec`, or equivalent process launch usage |
| Blocking sleeps inside explicit SQL transaction files | none found | the `BeginTx` file set has no production `time.Sleep` usage |

Current result: no network, file, process-launch, or deliberate blocking operation was found inside an existing transaction callback/explicit SQL transaction. This is a zero-baseline finding, not proof that future UoW callbacks are safe automatically. When P1 introduces a stable callback contract, the architecture gate must preserve this zero baseline and reject callback ports that expose connector/provider/file capabilities.

Potentially long external operations do exist in connector, identity-provider, file-storage, and MCP adapters, but they are outside the current database transaction owners. They must remain after-commit/outbox work rather than becoming callable from a transaction-aware port set.

## Failure-window verification matrix

The shared SQL Unit of Work contract runs the same four-window failure matrix for every P2 critical chain family owned by Runtime: Record mutation, Workflow task decision, Action execution, Record Timer state/evidence, Integration event acceptance, Metadata publication, and cross-boundary durable intent. Scheduler run-state transactions are verified in the Scheduler owner repository, not through Runtime's Unit of Work matrix.

| Injected window | Required result |
| --- | --- |
| first local write | the write fails and no new chain fact is visible |
| arbitrary middle local write | all earlier writes in the attempt roll back |
| after the final write, before commit | every write rolls back and the original error is preserved |
| after commit, in recoverable local coordination | all local facts remain committed; hook failure is recorded with correlation identity and must not reinterpret the commit as rollback |

The executable matrix is `TestCriticalTransactionChainsCoverEveryFailureWindow` in `infrastructure/persistence/database/transaction/unit_of_work_test.go`. Owner-specific P2 tests prove that each chain routes its indivisible local facts through its atomic commit adapter; this matrix proves the common transaction failure semantics applied by those adapters.

The Record adapter also runs `TestRecordMutationHundredConcurrentUpdatesHaveNoLostUpdateOrPartialCommit` against a real SQLite file database with 100 simultaneous updates using one expected revision. Exactly one mutation may commit; all other attempts must be rejected as an optimistic conflict or a typed transaction transient failure. The winning Record revision and its Audit, Integration Outbox, and Workflow Intent identities must match, with exactly one row in every fact table. This proves both no lost update and no partial side-fact commit under contention.

Opt-in real-dialect tests use `RUNTIME_POSTGRES_TEST_DSN` and `RUNTIME_MYSQL_TEST_DSN`. `TestRealDialectDeadlockContracts` creates an actual two-row/two-transaction lock cycle on PostgreSQL and MySQL and requires one committed transaction plus one typed `deadlock` failure. `TestRealDialectSerializableConflictContracts` runs concurrent serializable snapshots and writes: PostgreSQL must report `serialization_failure`; InnoDB reports the serializable SQLSTATE 40001 conflict as error 1213, which Runtime intentionally classifies by its more specific `deadlock` meaning. These are live engine errors, not fabricated message strings.

## Explicit gaps retained for adjudication

- ChangePlan has no Application rollback entrypoint.
- Identity and Notification expose mutation methods through embedded Domain services; later P0 analysis must decide the Application-owned consistency boundary without moving transaction policy into Transport.
- Bulk, Import, Record Timer processing, and due-publication processing are resumable/batch shapes and cannot inherit single-record transaction assumptions. The external Scheduler worker is outside this Runtime mutation inventory.

These gaps keep the inventory honest. They are not completion waivers and must be resolved or explicitly classified before P0 is complete.
