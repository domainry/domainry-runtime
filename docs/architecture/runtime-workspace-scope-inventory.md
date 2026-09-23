# Runtime Workspace Scope Inventory

This inventory is the auditable source list for the Runtime workspace-isolation
roadmap. Every adapter has one required scope classification. A classification
describes the security boundary the adapter must enforce, not whether the
current implementation already enforces it.

The only permitted classifications are `workspace_scoped`, `runtime_global`,
`installation_scoped`, and `public`.

## Database schema adapters

The machine gate creates a fresh Runtime SQLite schema and requires every
physical table to be named in this document. Runtime-generated business object
tables are additionally represented by `ObjectSchema.Key`; they are
`workspace_scoped` and are not known until a manifest is installed.

Identity accounts, credentials, roles, permissions and sessions are outside
the Runtime schema. The in-process module owns a separate Identity database;
SaaS mode keeps them in the remote Identity service.

Registered schema tables:

- `_schema_migrations` — `installation_scoped` and the sole host/module migration ledger
- `_workspaces` — `runtime_global`; it is the platform registry for canonical
  Workspace identity, initial-installation identity and current company
  organization authority; its typed commercial limits, billing contact and
  commercial revision are fields of the same aggregate row
- owner=`workspace`, kind=`workspace.provisioning` or
  `workspace.administration` rows in `_operations` — `runtime_global`; request
  idempotency is enforced for platform-wide Workspace operations without an
  implicit tenant scope, and resulting Workspace/company/user identities are
  linked from the operation
- `_release_cohorts`, `_release_instances` — `installation_scoped`;
  they coordinate one process release identity across the whole Runtime
  installation and must never be partitioned by tenant workspace
- `_operations` — explicit discriminator: tenant commands are
  `workspace_scoped`; system-purpose commands are `runtime_global`
- `_artifacts`, `_artifact_bindings` — `workspace_scoped`; owner and kind
  registrations partition shared metadata while content remains in the
  deployment BlobStore
- `_operation_controls` — `runtime_global`; every row requires an
  explicit system purpose and does not accept a tenant workspace discriminator
- `_worker_scopes` — `runtime_global`; it enumerates registered owner/scope
  keys for governed cross-workspace discovery and holds bounded technical
  checkpoints, capacity and fenced singleton leases
- `_rate_limit_buckets` — `runtime_global` technical storage used by the
  `database` rate-limit backend; the bucket
  key supplied by each tenant-facing caller includes its explicit workspace or
  tenant-owned credential scope, while the shared limiter itself does not infer
  or substitute a workspace
- Agent-owned `_agent_runtime_states`, `_agent_task_runs`,
  `_agent_interactive_runs` — `workspace_scoped`; each persisted state/run row
  has a mandatory `workspace_id`, and Agent repository reads and mutations use
  workspace builders. SaaS mode keeps the same rows in Agent persistence.
- `_operation_break_glass_grants` — `workspace_scoped`; every grant names one
  target workspace and its audited approval/revocation lifecycle cannot be
  queried through a wildcard tenant scope
- owner=`operations`, kind=`database_retirement` rows in `_operations` —
  `runtime_global`; object retirement, access observations, approvals and
  backup evidence never inherit tenant scope
- `_audit_events`, `_artifacts`, `_artifact_bindings` — `workspace_scoped`
- record/action/idempotency: `_action_assurance_grants`, owner=`action`,
  kind=`action.execution` rows, and owner=`record`, kind=`record.mutation` rows
  in `_operations` — `workspace_scoped`
- idempotency cleanup uses the registered `idempotency_cleanup` row in
  `_worker_scopes`; no owner-specific lease table exists
- workflow definitions use shared `_definitions` / `_definition_versions`
  under owner=`workflow`, kind=`workflow`; root resources are keyed by Workflow
  key and semantic versions by `version:<version-id>` —
  `installation_owner_kind_scoped`
- workflow execution: `_workflow_executions`, owner=`workflow`,
  kind=`workflow.execution.start` rows in `_operations`, `_workflow_process_instances`,
  `_workflow_node_instances`, `_workflow_tasks`, `_workflow_process_events`
  — `workspace_scoped`; operator-controlled execution/process rows link back
  to the accepted shared receipt through `operation_id`
- notification delivery policy, template roots and immutable template versions
  use shared `_definitions` / `_definition_versions` under owner=`notification`;
  owner=`notification`, kind=`template_publication` rows in `_operations` hold
  workspace-scoped approval/scheduling state and fenced worker claims
- `_automation_runs` — `workspace_scoped`
- `_project_model_state` — `runtime_global`
- `_record_localized_values` — `workspace_scoped`
- Shared `_definitions` and `_definition_versions` —
  `installation_owner_kind_scoped`;
  `_metadata_localized_texts` — `workspace_scoped`.
  Runtime reaches them only through the Metadata SDK Binding.
- Integration connector catalog and workspace event-mapping requirements use
  shared `_definitions` / `_definition_versions` under owner=`integration`,
  kinds `integration_connector` and `integration_event_mapping` —
  `installation_owner_kind_scoped` with workspace source isolation for mappings
- Scheduler definitions, snapshot cursor and publisher fence use shared
  `_definitions` / `_definition_versions` under owner=`scheduler`,
  kind=`scheduler`, keyed by Runtime ID — `installation_owner_kind_scoped`;
  Scheduler execution schedules remain Module-owned `_scheduler_schedules`
- Report definitions, operation-state examples, sensitive-field policies and
  export controls use shared `_definitions` / `_definition_versions` under
  owner=`report`, kind=`report` — `installation_owner_kind_scoped`;
  Report Module-owned `_report_snapshots` — `workspace_scoped`
- Identity-owned `_identity_profile_binding_definitions`,
  `_identity_profile_binding_definition_versions`, `_identity_role_definitions`,
  and `_identity_role_definition_versions` — `installation_scoped`
- Agent Module-owned `_agent_skill_definitions`, `_agent_definitions`, `_agent_entrypoint_definitions`,
  `_agent_service_principal_definitions`, `_agent_task_definitions` — `installation_scoped`
- Data Exchange Module-owned `_data_exchange_jobs` and `_data_exchange_job_chunks` — `workspace_scoped`; output metadata and job associations use shared `_artifacts` / `_artifact_bindings` with owner=`data_exchange`; owner=`data_exchange` rows in `_worker_scopes` contain only payload-free workspace scheduling identities. SaaS mode keeps the same application boundary remotely.
- Integration Module-owned tables in the borrowed Runtime database (or isolated behind Integration SaaS): `_integration_connections`,
  `_integration_secret_materials`, `_integration_secrets`,
  `_integration_external_identities`,
  `_integration_webhook_subscriptions`, `_integration_webhook_nonces`,
  `_integration_web_push_subscriptions`,
  `_integration_events`, `_integration_invocations`,
  `_integration_provider_runs`
  — `workspace_scoped`
- Runtime durable publication handoff: `_publication_outbox`
  — `workspace_scoped`; `publication_type` separates `integration.connector`
  from `notification.saas` while sharing lease, retry, fencing and recovery.
  Operator recovery stores the shared receipt in `operation_id`.
  Notification-owned tables remain outside the Runtime schema and are governed
  by the selected Module or SaaS Binding.
- lifecycle retention policy definitions: `_definitions`, `_definition_versions`
  — installation catalog partitioned by owner `lifecycle`, kind
  `retention_policy`, and workspace `source_id`; current publication and
  immutable history use Definition CAS.
- lifecycle governance: `_lifecycle_legal_holds`, `_lifecycle_cleanup_jobs`, `_subject_requests`,
  `_subject_steps`
  — `workspace_scoped`; subject requests use indexed typed rows for ordinary
  requests, account-erasure approvals and external reconciliation, and the
  successful erase root carries backup replay state. Subject erasure admission
  is the indexed root request plus its owner `lifecycle`, operation
  `erase_fence` step, not a separate fence table.
  Operator-triggered cleanup stores the shared receipt in
  `_lifecycle_cleanup_jobs.operation_id`; autonomous worker claims preserve it.
- lifecycle archives and upload files: shared `_artifacts` and
  `_artifact_bindings` — `workspace_scoped`; owner/kind partitions metadata,
  bindings carry cleanup-job/source-resource relationships, and BlobStore owns
  immutable bytes. There are no Lifecycle-private archive or file tables.
- lifecycle compliance and sensitive-access facts: shared `_audit_events`
  — `workspace_scoped`; Lifecycle appends only registered fact families through
  the Audit Binding. Cleanup progress/failure attempts remain cleanup-job state.
- Runtime health/version capability response (no persisted tenant data) — `public`

## File and object-storage adapters

- upload roots and record attachment paths (`application/upload`, `UploadService`) — `workspace_scoped`
- report/export/download artifacts (Report-owned jobs and export result references) — `workspace_scoped`
- staged transaction files and commit markers (`StagedFile`, `CommitMarker`) — inherit the owning `workspace_scoped` mutation
- migration SQL, migration backups, manifests and installation artifacts — `installation_scoped`

## Process-memory and cache adapters

- `dictionaryCache` — `installation_scoped`
- in-memory rate-limit buckets (`foundation/ratelimit.MemoryLimiter`) — `workspace_scoped`
- idempotency metrics (`MemoryMetricsCollector`) — `workspace_scoped`
- localization catalog resources (installation-static, read-only after load) — `installation_scoped`

## Durable task and payload adapters

- workflow executions, processes, nodes, tasks, events and execution receipts — `workspace_scoped`
- automation rule/instruction executions — `workspace_scoped`
- Runtime-published Scheduler definition projections — installation metadata scope; Scheduler-owned operational state is outside this Runtime inventory
- `record_timer` and `record_timer_event` — `workspace_scoped`
- integration events, invocations, outbox messages and reconciliation work — `workspace_scoped`
- Notification SaaS publication outbox rows — `workspace_scoped`; Notification
  domain state is owned and scoped outside Runtime by the selected Binding
- report/export/download task payloads — `workspace_scoped`
- idempotency cleanup leases — `runtime_global`; cross-boundary intents — `workspace_scoped`

## External connection and identity adapters

- integration connections, API keys, secrets and encrypted secret materials — `workspace_scoped`
- provider credentials and credential-refresh leases — `workspace_scoped`
- external identities and provider callback/subscription mapping — `workspace_scoped`
- connector invocation/request references and provider idempotency references — `workspace_scoped`

## Application authorization migration

The P1 authorization gate is migrated owner by owner. `complete` means every
Repository entry in that Application owner rejects an unknown/empty-workspace
Principal before the first Repository call and has a zero-call failure test.

| Application owner | Status | Evidence |
| --- | --- | --- |
| Agent session/proposal/report | complete | `TestAgentApplicationAuthorizesWorkspaceBeforeRepositoryAccess` |
| Audit | complete | `TestAuditApplicationAuthorizesWorkspaceBeforeRepositoryAccess` |
| Upload | complete | `TestUploadApplicationAuthorizesWorkspaceBeforePortAccess` |
| Report | complete | `TestReportApplicationAuthorizesWorkspaceBeforePorts` |
| Record | complete | `TestRecordApplicationAuthorizesWorkspaceBeforeRepositoryAccess`; `TestInternalMutationServiceRejectsDisallowedPoliciesBeforeRepository` |
| Action | complete | `TestActionApplicationAuthorizesWorkspaceBeforeRepositoryAccess` |
| Automation | complete | `TestAutomationApplicationAuthorizesWorkspaceBeforeRepositoryAccess` |
| ChangePlan | complete | `TestChangePlanApplicationAuthorizesWorkspaceBeforeRepositoryAccess` |
| Deployment | complete | `TestDeploymentApplicationAuthorizesWorkspaceBeforeRepositoryAccess` |
| Integration | complete | `TestIntegrationApplicationAuthorizesWorkspaceBeforeRepositoryAccess` |
| Lifecycle | complete | `TestLifecycleApplicationAuthorizesWorkspaceBeforeRepositoryAccess` |
| Metadata | complete | `TestMetadataApplicationAuthorizesWorkspaceBeforeRepositoryAccess` |
| Notification | complete | `TestNotificationApplicationAuthorizesWorkspaceBeforeRepositoryAccess`; `TestNotificationOutboxPolicyEvaluationCarriesWorkspace` |
| Scheduler facade | complete | `TestSchedulerAdapterAuthorizationUsesSchedulerCapabilities`; Runtime authorizes projection/preview/delegation only and holds no Scheduler state repository |
| Record Timer | complete | worker commands require explicit Runtime `SystemScope`; operator recovery requires Workspace principal and Record Timer capability |
| Workflow | complete | `TestWorkflowApplicationAuthorizesWorkspaceBeforeRepositoryAccess` |

## Repository workspace contract migration

The P1 Repository-port gate is migrated owner by owner. `complete` means every
tenant method exposes a mandatory workspace argument, rejects an empty or
mismatched workspace, and has an A/B workspace isolation contract test. The
roadmap checkbox remains open until every owner is complete.

| Repository owner | Status | Evidence |
| --- | --- | --- |
| Agent state | complete | `TestAgentStateStoreWorkspaceIsolationContract` |
| Audit | complete | `TestAuditStoreWorkspaceIsolationContract` |
| Automation | complete | `TestAutomationStoreWorkspaceIsolationContract`; execution history and instruction lease claim/heartbeat/complete are workspace-scoped |
| ChangePlan | complete | `TestBusinessChangePlanOperationWorkspaceIsolationContract`; tenant operation claim/complete/fail are workspace-scoped, while drafts and seed provenance remain explicitly `installation_scoped` |
| Deployment | complete | `TestDeploymentStoreWorkspaceIsolationContract`; tenant receipt operations require workspace, while health/metrics aggregation and cleanup use explicit `runtime_global` system scope |
| Integration | complete | `TestIntegrationConfigStoreWorkspaceIsolationContract`; `TestIntegrationEventStoreWorkspaceIsolationContract`; `TestIntegrationDeliveryStoreWorkspaceIsolationContract`; `TestIntegrationWorkerStoreRejectsMissingTenantAndSystemScopes`; tenant config/event/delivery ports require explicit workspace, worker-wide scans require explicit `runtime_global` system scope, and installation seeding passes `InstallationWorkspaceID` explicitly |
| Lifecycle | complete | `TestLifecycleStoreWorkspaceIsolationContract`; lifecycle policy, legal-hold, cleanup, subject-request, external-erasure, archive, deletion-registry, audit and metrics methods require explicit tenant workspace; cross-workspace cleanup discovery requires explicit `runtime_global` `SystemScope`; owner cleanup deletes remain constrained to the job workspace |
| Metadata | complete | Metadata source-owner tests prove definition/projection operations are installation-scoped and localized text reads/writes require an explicit workspace; Runtime consumes these operations only through the SDK Binding and has no Metadata SQL repository |
| Notification | complete | `TestNotificationStoreWorkspaceIsolationContract`; installation-scoped templates/policies require explicit installation `SystemScope`, tenant preferences/reservations/metrics require a non-empty workspace, and same-key A/B data remains isolated |
| Record and cross-owner record readers | complete | `TestRecordStoreWorkspaceIsolationContract`; tenant Record repository methods and consumer-owned Action, Automation, Deployment, Metadata, Pipeline, Report, Record Timer, and Workflow readers require explicit workspace; read/write/update/delete/list/unique/mutation paths reject missing workspace and isolate A/B workspaces |
| Record Timer | complete | `TestRecordTimerWorkerProcessesEveryWorkspace`, `TestRecordTimerWorkspaceFailureDoesNotStarveOtherTenantOrExceedGlobalBatch`, and Record Timer lease dialect tests prove explicit Workspace partitioning, bounded cross-Workspace rotation and fenced conditional claims; Scheduler has no Runtime repository row |
| Workflow | complete | `TestWorkflowStoreWorkspaceIsolationContract`; execution/process/node/task/event models persist workspace, Process and Worker repository ports require it, decision/state transactions scope conditional updates by `(workspace_id, id)`, worker claim/update and Action/Deployment/installation seed consumers preserve explicit scope |
