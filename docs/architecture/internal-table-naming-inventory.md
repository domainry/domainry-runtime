# Internal table naming and ownership inventory

This inventory is the naming baseline for fresh databases. It records physical
table names, not API fields, permission resources, queue kinds, manifest keys,
or migration names.

## Rules

- Built-in source-module tables use `_module_business_name`.
- Runtime-owned tables use the concrete business owner, such as `_workflow_*`,
  `_operation_*`, or `_application_schema_*`; there is no `_runtime_*` prefix.
- Dynamically generated business-object tables keep their business object key.
- Every physical database has exactly one host-owned migration ledger:
  `_schema_migrations`.
- Permission resources, JSON properties, queue kinds, and migration names are
  business contracts and do not inherit physical-table underscores.

## Source module tables

### Agent

`_agent_skill_definitions`, `_agent_definitions`,
`_agent_task_definitions`, `_agent_entrypoint_definitions`,
`_agent_service_principal_definitions`, `_agent_runtime_states`,
`_agent_task_runs`, `_agent_interactive_runs`; Agent task discovery uses its
registered owner rows in `_worker_scopes`.

### Audit

`_audit_events`. Audit export metadata uses shared `_artifacts` and
`_artifact_bindings`; bytes stay in deployment blob storage.

### Data Exchange

`_data_exchange_jobs`, `_data_exchange_job_chunks`; Data Exchange output
metadata and job bindings use shared `_artifacts` / `_artifact_bindings`, and
workspace discovery uses registered owner rows in `_worker_scopes`.

### Identity

Schema control and catalog:

`_identity_managed_database`, `_identity_manifest_catalog`.

Authentication and account projection:

`_identity_users`, `_identity_credentials`, `_identity_mfa_factors`,
`_identity_external_accounts`, `_identity_auth_provider_credentials`,
`_identity_auth_login_transactions`, `_identity_auth_authorization_codes`,
`_identity_auth_refresh_tokens`, `_identity_auth_assertion_replays`,
`_identity_auth_otp_delivery_limits`, `_identity_auth_mutation_receipts`.

Authorization and review:

`_identity_roles`, `_identity_role_requests`,
`_identity_user_role_assignments`, `_identity_menus`,
`_identity_role_menu_assignments`, `_identity_authorization_catalogs`,
`_identity_authorization_catalog_revisions`, `_identity_access_reviews`,
`_identity_access_review_items`, `_identity_access_review_receipts`,
`_identity_authoring_receipts`, `_identity_entitlement_batch_receipts`.

Organization projection:

`_identity_organization_units`. Personnel and reporting-line facts are columns
of `_identity_users`; there is no separate Workforce aggregate or assignment
table.

Profile Binding:

`_identity_profile_binding_definitions`,
`_identity_profile_binding_definition_versions`,
`_identity_profile_bindings`, `_identity_profile_binding_receipts`,
`_identity_profile_binding_events`.

Profile Binding belongs to Identity. A definition describes how one business
profile type is linked; a runtime binding links the concrete business profile
record identified by `object_key`/`profile_id` to the global Identity user
identified by `identity_user_id`. Metadata stores generic definitions but does
not own this identity-link lifecycle.

### Integration

Connector catalog and event-mapping definitions use shared `_definitions` and
`_definition_versions` under owner=`integration`, kinds
`integration_connector` and `integration_event_mapping`.

Integration-owned state is `_integration_connections`,
`_integration_provider_runs`,
`_integration_secret_materials`, `_integration_secrets`,
`_integration_external_identities`, `_integration_invocations`,
`_integration_events`,
`_integration_webhook_nonces`, `_integration_webhook_subscriptions`,
`_integration_web_push_subscriptions`.

There is no `integration_outbox_messages` table. Runtime-to-provider durable
handoff uses the Runtime-owned `_publication_outbox` table.

### Lifecycle

`_lifecycle_legal_holds`, `_lifecycle_cleanup_jobs`,
`_subject_requests`, `_subject_steps`.

`_subject_requests` uses typed rows for ordinary subject requests,
approved account-erasure provenance and external-erasure reconciliation. A
successful erase row itself carries durable backup replay state, so Lifecycle
does not own separate approval, external-erasure or deletion-registry tables.
An erasure fence is owner `lifecycle`, operation `erase_fence` in the execution
step journal and is resolved through the request's indexed
`resolved_identity`; there is no separate subject-erasure fence table.
Lifecycle compliance and sensitive-access facts are appended through the Audit
Binding to shared `_audit_events`; Lifecycle does not own an audit/evidence
table or write worker progress/retry attempts as Audit events.
Retention archives and governed upload metadata use shared `_artifacts` and
`_artifact_bindings`; immutable bytes remain in the deployment BlobStore.
Lifecycle therefore owns no private archive-entry or file-artifact table.
`_lifecycle_cleanup_jobs.operation_id` records the latest shared Operations
receipt that explicitly ran the owner job; ordinary background claims preserve
that link instead of clearing it.

### Metadata

`_definitions`, `_definition_versions`, `_metadata_localized_texts`.

The shared catalog is partitioned by installation, owner and kind. It owns
manifest-backed Metadata resources, the current application projection
identity, and registered cross-module definitions. The SDK exposes business
ports only. Identity Profile Binding runtime state remains Identity-owned even
when its configuration definition is published under owner `identity`.
Lifecycle retention policies use owner `lifecycle`, kind `retention_policy`;
their current row and immutable history live here rather than in a private
Lifecycle policy-version table.

### Notification

`_notification_events`,
`_notification_deliveries`, `_notification_delivery_attempts`,
`_notification_inbox_items`, `_notification_inbox_delegations`,
`_notification_alert_groups`, `_notification_user_settings`.

`_notification_deliveries` uses typed `delivery` and `reservation` rows so
channel lease/retry state and recipient/channel/template/dedupe claims retain
their different query contracts without private sibling tables.
Notification workspace freeze/import/cutover state uses shared
`_operation_controls` rows under system purpose `notification_migration` and
kind `workspace_cutover`, with the workspace ID as owner and revision CAS for
cross-instance fencing.
Retention payloads are written through Lifecycle's shared ArchiveStore; there
is no Notification-owned archive table or archive row in portable bundles.
`_notification_delivery_attempts` distinguishes pre-materialization `event`
failures from channel-dispatch attempts. `_notification_user_settings`
distinguishes `delivery_preference` and `saved_view` rows by setting kind and
key.

Delivery policy is a typed shared Definition under owner `notification`, kind
`delivery_policy`, keyed by workspace. Policy writes use the current Definition
version as their compare-and-swap token; Notification owns no private policy
table.

Template roots and immutable published versions are typed shared Definitions
under owner `notification`, kinds `notification_template` and
`notification_template_version`. Draft, publish and disable transitions append
shared version history. Publishing the immutable version and advancing the root
revision commit in one transaction, so Notification owns neither a private
template table nor a private template-version table.

Template publication requests are owner `notification`, kind
`template_publication` rows in shared `_operations`. The template root
Definition stores the current request reservation and advances it by CAS in the
same transaction as Operation creation or terminal release. Scheduled claims
and stale-worker recovery use the Operation lease and fencing token, so
Notification owns neither a publication-request table nor a lock table.

The authorization resource remains `notification_delivery_policy`; it is not a
physical table name.

### Report

`_report_snapshots`.

Report definitions, operation-state examples, sensitive-field policies and
export controls are one immutable typed definition stored in shared
`_definitions` / `_definition_versions` under owner `report`, kind `report`.

### Scheduler

`_scheduler_schedules`, `_scheduler_runs`.

Canonical Scheduler definitions, their snapshot cursor and the active publisher
fence are one typed, versioned aggregate in shared `_definitions` /
`_definition_versions` under owner `scheduler`, kind `scheduler`, keyed by
Runtime ID. Scheduler owns no private definition, snapshot or publication
table. Publishing the shared Definition and advancing the typed execution
projection fence use one database transaction.

`_scheduler_schedules` is the typed current schedule aggregate. Runtime-authored
definitions and user-authored plans share one row shape; a scheduled-plan row
contains both the product plan payload and its executable definition/cursor, so
Scheduler no longer mirrors the same plan across `_scheduler_plans` and
`_scheduler_definition_states`. A single `definition_projection` control row
holds the applied publication cursor so an older reader cannot regress the
executable schedule projection after a newer canonical publication commits.

Module-mode Scheduler management idempotency is stored as owner `scheduler`,
kind `management_command` in shared `_operations`; Scheduler does not own a
private command-receipt table. SaaS does not expose the embedded management
adapter and therefore does not invent an equivalent private receipt store.

Dead-letter failure and resolution evidence is stored on the terminal/requeued
`_scheduler_runs` row. No product or operator API reads a separate run timeline,
so Scheduler owns neither a dead-letter table nor a run-event table.

The manifest property remains `scheduler_definitions`; it is not a physical
table name.

## Runtime-owned tables

### Application Schema

`_project_model_state`（固定 `id=current` 的单行当前状态）。

### Workflow

`_workflow_executions`,
`_workflow_process_instances`, `_workflow_node_instances`, `_workflow_tasks`,
`_workflow_process_events`, `_workflow_route_steps`.

Canonical Workflow roots and independently mutable semantic versions are typed
resources in shared `_definitions` / `_definition_versions` under owner
`workflow`, kind `workflow`. Root resources use the Workflow key; semantic
versions use `version:<version-id>`. Every state change appends immutable shared
history, while root/version changes that form one Workflow transition commit in
one database transaction. Workflow owns no private definition table.

`_workflow_executions` is not a duplicate node-instance table. It is the
durable process-start and continuation-intent queue: an intent may exist before
a process/node, and one process may receive multiple continuation intents.
Both it and `_workflow_process_instances` retain `operation_id` when an
operator retry or resolution is orchestrated by shared Operations, while the
Workflow rows remain the authoritative business run state.
`_workflow_route_steps` is also not an execution attempt table; it is the
independently queried and compare-and-set updated authority for a per-instance,
multi-step approval route. Process events remain product state because process
detail and approval-deadline idempotency read the ordered timeline.

### Automation

`_automation_runs`（`run_kind` 区分规则执行与指令执行）。

### Action

`_action_assurance_grants`.

### Record and transaction

`_record_localized_values`.

The Runtime-owned `record_timer` system object remains in Record storage and
records the latest controlling shared receipt in its indexed `operation_id`
field for dead-letter retry or resolution.

### Publication

`_publication_outbox`.

Operator retry, resolution and acknowledgement persist the controlling shared
receipt in `_publication_outbox.operation_id`; Provider delivery state remains
owned by Integration.

The durable worker queue kind remains `runtime_publication_outbox`; it is not a
physical table name.

### Release

`_release_cohorts`, `_release_instances`.

### Operations

`_operations`, `_operation_controls`, `_operation_break_glass_grants`.
Action executions, Dispatch callbacks, Record mutations, Workflow start-command
receipts, Report export preparation, Workspace administration, and Workspace
provisioning are owner/kind-scoped rows in `_operations`. Database retirement
current state is a runtime-global `operations/database_retirement` row in the
same store; none owns a separate receipt/state table. Operation result evidence
is recursively redacted before inline persistence and is bounded to 16 KiB.
Larger JSON results are stored as governed `operations/result` artifacts; the
operation row retains only the artifact identity, size and checksum required for
authorized, integrity-checked replay. Every Runtime operator kind registers a
retention class and policy key: read-only inspection, bulk dry-run and
diagnostics use `operations.technical_receipt.v1`; mutation and recovery kinds
use `operations.receipt.v1`. Lifecycle cleanup matches the exact registered
`owner` + `kind` pair, so owner-specific rows sharing `_operations` are not
captured by a generic Operations policy.
Operations injects the accepted receipt ID into the owner execution context
before invoking a mutation. Workflow run rows, Lifecycle cleanup jobs,
publication handoffs and Record Timer rows consume that context and persist
`operation_id`; `_operations` never absorbs their business state.

### Artifacts

`_artifacts`, `_artifact_bindings`. Artifact rows contain metadata and an
immutable deployment BlobStore reference only; file bytes and base64 payloads
are not database columns. Large Operation results bind the artifact to the exact
operation with binding kind `operation` and resource type `operation`; replay
requires that binding and verifies both byte count and SHA-256 before returning
the JSON result.

### Worker, idempotency, and rate limiting

`_worker_scopes`, `_rate_limit_buckets`.

## Removed or rejected structures

- `_capability_frontend_manifests` and `frontend_capability_manifests`: the
  entire unpublished frontend-capability vertical slice is removed.
- `_authorization_rls_policies` and `_runtime_rls_policies`: no policy table is
  registered. PostgreSQL-native RLS and `DATABASE_RLS_ENABLED` are removed;
  framework-level RLS/CLS remains.
- `integration_outbox_messages`: never materialized and not retained.
- `_schema_materializations` and `_runtime_schema_migrations`: obsolete
  unpublished ledgers are absent from fresh-schema code; no drop, adoption or
  backup path remains. `_schema_migrations` is the only migration ledger.
- `_identity_departments`, `_identity_workforce_*`, `_party_*`, and
  `_party_schema_migrations`: unpublished structures are not part of the fresh
  schema and receive no compatibility or rename migration.
- `_identity_change_plan_drafts`, `_identity_change_plan_operations`,
  `_identity_portability_export_receipts`,
  `_identity_portability_import_receipts`, and
  `_identity_portability_write_fence_events`: these were retired compatibility
  names, not current Identity-owned persistence. They are not recreated under
  new names. Portability write-fence state is owned by
  `_identity_workspace_write_fences`, while durable evidence is written to
  `_audit_events`.
- `_identity_definition_versions`: this was a legacy bridge into the shared
  definition-version store, not a current Identity table. Shared versions live
  in `_definition_versions`; Identity-owned profile
  binding and role versions live in
  `_identity_profile_binding_definition_versions` and
  `_identity_role_definition_versions`.
- Unpublished compatibility copies, rename migrations, and backup tables for
  the retired names are not created.
