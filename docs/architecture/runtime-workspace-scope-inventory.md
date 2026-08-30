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

- `_schema_materializations`, `_schema_migrations` — `installation_scoped`
- `runtime_release_cohorts`, `runtime_release_instances` — `installation_scoped`;
  they coordinate one process release identity across the whole Runtime
  installation and must never be partitioned by tenant workspace
- `runtime_operations` — explicit discriminator: tenant commands are
  `workspace_scoped`; system-purpose commands are `runtime_global`
- `runtime_operation_controls` — `runtime_global`; every row requires an
  explicit system purpose and does not accept a tenant workspace discriminator
- `runtime_worker_queue_scopes` — `runtime_global`; it enumerates explicit
  workspace scope keys for governed cross-workspace worker queue discovery
- `runtime_rate_limit_bucket` — `runtime_global` technical storage; the bucket
  key supplied by each tenant-facing caller includes its explicit workspace or
  tenant-owned credential scope, while the shared limiter itself does not infer
  or substitute a workspace
- `agent_runtime_state`, `agent_task_runs`, `agent_interactive_runs` —
  `workspace_scoped`; each persisted state/run row has a mandatory
  `workspace_id`, and repository reads and mutations use workspace builders
- `runtime_break_glass_grants` — `workspace_scoped`; every grant names one
  target workspace and its audited approval/revocation lifecycle cannot be
  queried through a wildcard tenant scope
- `runtime_database_retirements` — `runtime_global`; object retirement, access
  observations, approvals and backup evidence never inherit tenant scope
- `_business_seed_provenance` — `installation_scoped`
- `_audit_events`, `business_audit_export_artifacts`, `transaction_boundary_intents` — `workspace_scoped`
- record/action/idempotency: `business_action_executions`,
  `action_assurance_grants`, `record_mutation_executions`
  — `workspace_scoped`
- `idempotency_cleanup_leases` — `runtime_global`
- workflow definitions: `workflow_definitions`, `workflow_definition_identities`,
  `workflow_definition_versions` — `installation_scoped`
- workflow execution: `_workflow_executions`, `workflow_execution_receipts`, `workflow_process_instances`,
  `workflow_node_instances`, `workflow_tasks`, `workflow_process_events`
  — `workspace_scoped`
- `automation_rule_definitions` — `installation_scoped`
- `automation_rule_executions`, `automation_instruction_executions` — `workspace_scoped`
- `metadata_catalog`, `metadata_definition_versions`, `metadata_exact_decimal_migrations`, `business_change_plan_drafts`
  — `installation_scoped`
- `business_change_plan_operations`, `business_localized_text`, `business_record_localized_value` — `workspace_scoped`
- definition catalog: `object_definitions`, `field_definitions`,
  `validation_definitions`, `view_definitions`, `action_definitions`,
  `scheduler_definitions`, `preference_definitions`, `rule_set_definitions`, `report_definitions`,
  `operation_state_example_definitions`, `sensitive_field_policy_definitions`,
  `report_export_control_definitions`, `dictionary_definitions`,
  `connector_definitions`, `integration_event_mapping_definitions`,
  `surface_definitions`, `component_definitions`, `entrypoint_definitions`,
  `skill_definitions`, `agent_definitions`, `agent_entrypoint_definitions`,
  `agent_service_principal_definitions`, `agent_task_definitions`, `role_definitions`,
  `identity_profile_binding_definitions` — `installation_scoped`
- `report_snapshots` — `workspace_scoped`
- Data Exchange Module-owned `data_exchange_jobs`, `data_exchange_chunks`, `data_exchange_artifacts` — `workspace_scoped`; `data_exchange_queue_scopes` contains only payload-free workspace scheduling identities. SaaS mode keeps the same ownership boundary remotely.
- Party Module-owned foundation tables in the borrowed Runtime database (or isolated behind Party SaaS): `party_parties`, `party_persons`,
  `party_organizations`, `party_contact_points`, `party_addresses`,
  `party_identifiers`, `party_communication_preferences`, `party_consents`,
  `party_privacy_preferences`, `party_marketing_subscriptions`,
  `party_job_catalog`, `party_positions`, `party_organization_extensions`,
  `party_organization_extension_memberships` — `workspace_scoped`
- integration: `integration_connections`, `integration_api_keys`,
  `integration_secret_materials`, `integration_secrets`,
  `integration_external_identities`, `integration_credential_refresh_leases`,
  `integration_webhook_subscriptions`, `integration_webhook_nonces`,
  `web_push_subscriptions`,
  `integration_events`, `integration_invocations`,
  `integration_outbox_messages`, `integration_event_mapping_intents`,
  `connector_provider_states`
  — `workspace_scoped`
- Notification SaaS publication handoff: `notification_publication_outbox`
  — `workspace_scoped`; all Notification-owned tables are outside the Runtime
  schema and are governed by the selected Module or SaaS Binding.
- `frontend_capability_manifests` — `workspace_scoped`
- lifecycle governance: `lifecycle_policy_versions`, `lifecycle_legal_holds`,
  `lifecycle_cleanup_jobs`, `lifecycle_subject_requests`,
  `lifecycle_external_erasures`, `lifecycle_audit_evidence`,
  `lifecycle_archive_entries`, `lifecycle_deletion_registry`,
  `lifecycle_file_artifacts`
  — `workspace_scoped`
- Runtime health/version capability response (no persisted tenant data) — `public`

## File and object-storage surfaces

- upload roots and record attachment paths (`application/upload`, `UploadService`) — `workspace_scoped`
- report/export/download artifacts (`agentReportDownloadTasks`, export result references) — `workspace_scoped`
- staged transaction files and commit markers (`StagedFile`, `CommitMarker`) — inherit the owning `workspace_scoped` mutation
- migration SQL, migration backups, manifests and installation artifacts — `installation_scoped`

## Process-memory and cache surfaces

- `agentDialogSessions`, `agentDialogProposals`, `agentReportQueryRuns`,
  `agentReportExportAudits`, `agentReportDownloadTasks`
  — `workspace_scoped`
- `dictionaryCache` — `installation_scoped`
- in-memory rate-limit buckets (`MemoryLimiter.buckets`) — `workspace_scoped`
- resilience/circuit state (`MemoryStore.states`) — `workspace_scoped`
- idempotency metrics (`MemoryMetricsCollector`) — `workspace_scoped`
- localization catalog resources (installation-static, read-only after load) — `installation_scoped`

## Durable task and payload surfaces

- workflow executions, processes, nodes, tasks, events and execution receipts — `workspace_scoped`
- automation rule/instruction executions — `workspace_scoped`
- scheduler job definitions, run records, run events and retry/dead-letter state — `workspace_scoped`
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
| Scheduler | complete | `TestSchedulerApplicationAuthorizesWorkspaceBeforeRepositoryAccess` |
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
| Deployment | complete | `TestDeploymentStoreWorkspaceIsolationContract`; `TestFrontendCapabilityStoreWorkspaceIsolationContract`; tenant receipt operations and frontend manifests require workspace, while health/metrics aggregation and cleanup use explicit `runtime_global` system scope |
| Integration | complete | `TestIntegrationConfigStoreWorkspaceIsolationContract`; `TestIntegrationEventStoreWorkspaceIsolationContract`; `TestIntegrationDeliveryStoreWorkspaceIsolationContract`; `TestIntegrationWorkerStoreRejectsMissingTenantAndSystemScopes`; tenant config/event/delivery ports require explicit workspace, worker-wide scans require explicit `runtime_global` system scope, and installation seeding passes `InstallationWorkspaceID` explicitly |
| Lifecycle | complete | `TestLifecycleStoreWorkspaceIsolationContract`; lifecycle policy, legal-hold, cleanup, subject-request, external-erasure, archive, deletion-registry, audit and metrics methods require explicit tenant workspace; cross-workspace cleanup discovery requires explicit `runtime_global` `SystemScope`; owner cleanup deletes remain constrained to the job workspace |
| Metadata | complete | `TestMetadataStoreWorkspaceIsolationContract`; definition and manifest operations require explicit `installation` system scope, localized text reads/writes require a non-empty matching workspace, A/B workspaces remain isolated, and installation projection/seeding uses `InstallationWorkspaceID` explicitly |
| Notification | complete | `TestNotificationStoreWorkspaceIsolationContract`; installation-scoped templates/policies require explicit installation `SystemScope`, tenant preferences/reservations/metrics require a non-empty workspace, and same-key A/B data remains isolated |
| Record and cross-owner record readers | complete | `TestRecordStoreWorkspaceIsolationContract`; tenant Record repository methods and consumer-owned Action, Automation, Deployment, Metadata, Pipeline, Report, Scheduler, SurfaceContext, and Workflow readers require explicit workspace; read/write/update/delete/list/unique/mutation paths reject missing workspace and isolate A/B workspaces |
| Scheduler | complete | `TestSchedulerRecordStateWorkspaceIsolationContract`; Scheduler consumer repository requires explicit workspace, job definition/run/event/dead-letter reads and mutations preserve it, and system worker entrypoints map an authorized installation `SystemScope` to `InstallationWorkspaceID` explicitly |
| Workflow | complete | `TestWorkflowStoreWorkspaceIsolationContract`; execution/process/node/task/event models persist workspace, Process and Worker repository ports require it, decision/state transactions scope conditional updates by `(workspace_id, id)`, worker claim/update and Action/Deployment/installation seed consumers preserve explicit scope |
