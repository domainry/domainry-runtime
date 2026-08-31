# Runtime Workspace Scope Inventory

This inventory is the auditable source list for the Runtime workspace-isolation
roadmap. Every surface has one required scope classification. A classification
describes the security boundary the surface must enforce, not whether the
current implementation already enforces it.

The only permitted classifications are `workspace_scoped`, `runtime_global`,
`installation_scoped`, and `public`.

## Database schema surfaces

The machine gate creates a fresh Runtime SQLite schema and requires every
physical table to be named in this document. Runtime-generated business object
tables are additionally represented by `ObjectSchema.Key`; they are
`workspace_scoped` and are not known until a manifest is installed.

Identity accounts, credentials, roles, permissions and sessions are outside
the Runtime schema. The in-process module owns a separate Identity database;
SaaS mode keeps them in the remote Identity service.

Registered schema tables:

- `_schema_migrations` — `installation_scoped` and the sole host/module migration ledger
- `_workspaces`, `_tenant_registry` — `runtime_global`; they are the platform
  registries for canonical workspace and tenant identities, and their canonical
  codes are globally unique rather than inferred from a caller workspace
- `_tenant_installation` — `installation_scoped`; its singleton row is written
  atomically with the first tenant and binds this Runtime installation to that
  real tenant/workspace before tenant-facing services are assembled
- `_workspace_configuration` — `workspace_scoped`; every row is keyed by the
  newly provisioned workspace and is created in the same guarded transaction
- `_workspace_provisioning_receipts` — `runtime_global`; request idempotency is
  enforced for the platform-wide provisioning operation and never supplies an
  implicit tenant scope
- `_release_cohorts`, `_release_instances` — `installation_scoped`;
  they coordinate one process release identity across the whole Runtime
  installation and must never be partitioned by tenant workspace
- `_operation_requests` — explicit discriminator: tenant commands are
  `workspace_scoped`; system-purpose commands are `runtime_global`
- `_operation_controls` — `runtime_global`; every row requires an
  explicit system purpose and does not accept a tenant workspace discriminator
- `_worker_queue_scopes` — `runtime_global`; it enumerates explicit
  workspace scope keys for governed cross-workspace worker queue discovery
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
- `_operation_database_retirements` — `runtime_global`; object retirement, access
  observations, approvals and backup evidence never inherit tenant scope
- `_audit_events`, `_audit_export_artifacts`, `_transaction_boundary_intents` — `workspace_scoped`
- record/action/idempotency: `_action_executions`,
  `_action_assurance_grants`, `_record_mutation_executions`
  — `workspace_scoped`
- `_idempotency_cleanup_leases` — `runtime_global`
- workflow definitions: `_workflow_definitions`,
  `_workflow_definition_versions` — `installation_scoped`
- workflow execution: `_workflow_executions`, `_workflow_execution_receipts`, `_workflow_process_instances`,
  `_workflow_node_instances`, `_workflow_tasks`, `_workflow_process_events`
  — `workspace_scoped`
- `_automation_rule_executions`, `_automation_instruction_executions` — `workspace_scoped`
- `_application_schema_projection`, `_application_schema_seed_checkpoints`, `_application_schema_exact_decimal_migration_receipts`
  — `runtime_global`
- `_record_localized_values` — `workspace_scoped`
- Metadata Module-owned `_metadata_definitions`,
  `_metadata_definition_versions`, and `_metadata_projection` —
  `installation_scoped`; `_metadata_localized_texts` — `workspace_scoped`.
  Runtime reaches them only through the Metadata SDK Binding.
- Integration Module-owned `_integration_connector_definitions` and
  `_integration_event_mapping_definitions` — `installation_scoped`
- Scheduler Module-owned `_scheduler_definitions` — `installation_scoped`
- Report Module-owned `_report_definitions`, `_report_operation_state_examples`,
  `_report_sensitive_field_policies`, `_report_export_controls` — `installation_scoped`;
  `_report_snapshots` — `workspace_scoped`
- Identity-owned `_identity_profile_binding_definitions`,
  `_identity_profile_binding_definition_versions`, `_identity_role_definitions`,
  and `_identity_role_definition_versions` — `installation_scoped`
- Agent Module-owned `_agent_skill_definitions`, `_agent_definitions`, `_agent_entrypoint_definitions`,
  `_agent_service_principal_definitions`, `_agent_task_definitions` — `installation_scoped`
- Data Exchange Module-owned `_data_exchange_jobs`, `_data_exchange_job_chunks`, `_data_exchange_artifacts` — `workspace_scoped`; `_data_exchange_queue_scopes` contains only payload-free workspace scheduling identities. SaaS mode keeps the same ownership boundary remotely.
- Party Module-owned foundation tables in the borrowed Runtime database (or isolated behind Party SaaS): `_party_parties`, `_party_persons`,
  `_party_organizations`, `_party_contact_points`, `_party_addresses`,
  `_party_identifiers`, `_party_communication_preferences`, `_party_consents`,
  `_party_privacy_preferences`, `_party_marketing_subscriptions`,
  `_party_job_catalog_items`, `_party_positions`, `_party_organization_extensions`,
  `_party_organization_extension_memberships` — `workspace_scoped`
- Integration Module-owned tables in the borrowed Runtime database (or isolated behind Integration SaaS): `_integration_connections`, `_integration_api_keys`,
  `_integration_secret_materials`, `_integration_secrets`,
  `_integration_external_identities`, `_integration_credential_refresh_leases`,
  `_integration_webhook_subscriptions`, `_integration_webhook_nonces`,
  `_integration_web_push_subscriptions`,
  `_integration_events`, `_integration_invocations`,
  `_integration_event_mapping_intents`,
  `_integration_connector_provider_states`
  — `workspace_scoped`
- Runtime durable publication handoff: `_publication_outbox`
  — `workspace_scoped`; `publication_type` separates `integration.connector`
  from `notification.saas` while sharing lease, retry, fencing and recovery.
  Notification-owned tables remain outside the Runtime schema and are governed
  by the selected Module or SaaS Binding.
- lifecycle governance: `_lifecycle_policy_versions`, `_lifecycle_legal_holds`,
  `_lifecycle_cleanup_jobs`, `_lifecycle_subject_requests`,
  `_lifecycle_external_erasure_requests`, `_lifecycle_audit_evidence`,
  `_lifecycle_archive_entries`, `_lifecycle_deletion_registry`,
  `_lifecycle_file_artifacts`
  — `workspace_scoped`
- Runtime health/version capability response (no persisted tenant data) — `public`

## File and object-storage surfaces

- upload roots and record attachment paths (`application/upload`, `UploadService`) — `workspace_scoped`
- report/export/download artifacts (Report-owned jobs and export result references) — `workspace_scoped`
- staged transaction files and commit markers (`StagedFile`, `CommitMarker`) — inherit the owning `workspace_scoped` mutation
- migration SQL, migration backups, manifests and installation artifacts — `installation_scoped`

## Process-memory and cache surfaces

- `dictionaryCache` — `installation_scoped`
- in-memory rate-limit buckets (`foundation/ratelimit.MemoryLimiter`) — `workspace_scoped`
- idempotency metrics (`MemoryMetricsCollector`) — `workspace_scoped`
- localization catalog resources (installation-static, read-only after load) — `installation_scoped`

## Durable task and payload surfaces

- workflow executions, processes, nodes, tasks, events and execution receipts — `workspace_scoped`
- automation rule/instruction executions — `workspace_scoped`
- Runtime-published Scheduler definition projections — installation metadata scope; Scheduler-owned operational state is outside this Runtime inventory
- `record_timer` and `record_timer_event` — `workspace_scoped`
- integration events, invocations, outbox messages and reconciliation work — `workspace_scoped`
- Notification SaaS publication outbox rows — `workspace_scoped`; Notification
  domain state is owned and scoped outside Runtime by the selected Binding
- report/export/download task payloads — `workspace_scoped`
- idempotency cleanup leases — `runtime_global`; cross-boundary intents — `workspace_scoped`

## External connection and identity surfaces

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
| Record | complete | `TestRecordApplicationAuthorizesWorkspaceBeforeRepositoryAccess`; `TestOwnerDepartmentPathRebuilderRejectsMissingSystemScopeBeforeRepository` |
| Action | complete | `TestActionApplicationAuthorizesWorkspaceBeforeRepositoryAccess` |
| Automation | complete | `TestAutomationApplicationAuthorizesWorkspaceBeforeRepositoryAccess` |
| ChangePlan | complete | `TestChangePlanApplicationAuthorizesWorkspaceBeforeRepositoryAccess` |
| Deployment | complete | `TestDeploymentApplicationAuthorizesWorkspaceBeforeRepositoryAccess` |
| Integration | complete | `TestIntegrationApplicationAuthorizesWorkspaceBeforeRepositoryAccess` |
| Lifecycle | complete | `TestLifecycleApplicationAuthorizesWorkspaceBeforeRepositoryAccess` |
| Metadata | complete | `TestMetadataApplicationAuthorizesWorkspaceBeforeRepositoryAccess` |
| Notification | complete | `TestNotificationApplicationAuthorizesWorkspaceBeforeRepositoryAccess`; `TestNotificationOutboxPolicyEvaluationCarriesWorkspace` |
| Scheduler facade | complete | `TestSchedulerSurfaceAuthorizationUsesSchedulerCapabilities`; Runtime authorizes projection/preview/delegation only and holds no Scheduler state repository |
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
