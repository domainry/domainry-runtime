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
`_agent_task_runs`, `_agent_interactive_runs`, `_agent_worker_scopes`.

### Audit

`_audit_events`, `_audit_export_artifacts`.

### Data Exchange

`_data_exchange_jobs`, `_data_exchange_job_chunks`,
`_data_exchange_artifacts`, `_data_exchange_queue_scopes`.

### Identity

Schema control and catalog:

`_identity_managed_database`, `_identity_manifest_catalog`,
`_identity_workspace_write_fences`, `_identity_metadata_refresh_intents`,
`_identity_localized_texts`.

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

`_integration_connector_definitions`,
`_integration_event_mapping_definitions`, `_integration_connections`,
`_integration_connector_provider_states`, `_integration_api_keys`,
`_integration_secret_materials`, `_integration_secrets`,
`_integration_external_identities`, `_integration_invocations`,
`_integration_events`, `_integration_event_mapping_intents`,
`_integration_webhook_nonces`, `_integration_webhook_subscriptions`,
`_integration_credential_refresh_leases`,
`_integration_web_push_subscriptions`.

There is no `integration_outbox_messages` table. Runtime-to-provider durable
handoff uses the Runtime-owned `_publication_outbox` table.

### Lifecycle

`_lifecycle_policy_versions`, `_lifecycle_legal_holds`,
`_lifecycle_cleanup_jobs`, `_lifecycle_subject_requests`,
`_lifecycle_external_erasure_requests`, `_lifecycle_audit_evidence`,
`_lifecycle_archive_entries`, `_lifecycle_deletion_registry`,
`_lifecycle_file_artifacts`.

### Metadata

`_metadata_definitions`, `_metadata_definition_versions`,
`_metadata_localized_texts`, `_metadata_projection`.

The generic catalog owns every manifest-backed Metadata resource type. The
SDK exposes business ports only. Identity role definitions and Identity
Profile Binding runtime state remain Identity-owned.

### Notification

`_notification_templates`, `_notification_template_versions`,
`_notification_template_publication_requests`,
`_notification_template_publication_locks`,
`_notification_delivery_policies`, `_notification_recipient_preferences`,
`_notification_delivery_reservations`, `_notification_events`,
`_notification_event_failures`, `_notification_channel_plans`,
`_notification_inbox_items`, `_notification_inbox_delegations`,
`_notification_alert_groups`, `_notification_inbox_saved_views`,
`_notification_retention_archive_entries`,
`_notification_migration_controls`.

The authorization resource remains `notification_delivery_policy`; it is not a
physical table name.

### Report

`_report_definitions`, `_report_operation_state_examples`,
`_report_sensitive_field_policies`, `_report_export_controls`,
`_report_snapshots`.

### Scheduler

`_scheduler_definitions`, `_scheduler_definition_states`, `_scheduler_runs`,
`_scheduler_run_events`, `_scheduler_dead_letters`.

The manifest property remains `scheduler_definitions`; it is not a physical
table name.

## Runtime-owned tables

### Application Schema

`_application_schema_projection`, `_application_schema_seed_checkpoints`,
`_application_schema_exact_decimal_migration_receipts`.

### Workflow

`_workflow_definitions`, `_workflow_definition_versions`,
`_workflow_executions`, `_workflow_execution_receipts`,
`_workflow_process_instances`, `_workflow_node_instances`, `_workflow_tasks`,
`_workflow_process_events`.

### Automation

`_automation_rule_executions`, `_automation_instruction_executions`.

### Action

`_action_executions`, `_action_assurance_grants`.

### Record and transaction

`_record_mutation_executions`, `_record_localized_values`,
`_transaction_boundary_intents`.

### Publication

`_publication_outbox`.

The durable worker queue kind remains `runtime_publication_outbox`; it is not a
physical table name.

### Release

`_release_cohorts`, `_release_instances`.

### Operations

`_operation_requests`, `_operation_controls`,
`_operation_break_glass_grants`, `_operation_database_retirements`.

### Worker, idempotency, and rate limiting

`_worker_queue_scopes`, `_idempotency_cleanup_leases`,
`_rate_limit_buckets`.

## Removed or rejected structures

- `_capability_frontend_manifests` and `frontend_capability_manifests`: the
  entire unpublished frontend-capability vertical slice is removed.
- `_authorization_rls_policies` and `_runtime_rls_policies`: no policy table is
  registered. PostgreSQL-native RLS and `DATABASE_RLS_ENABLED` are removed;
  framework-level RLS/CLS remains.
- `integration_outbox_messages`: never materialized and not retained.
- `_schema_materializations` and `_runtime_schema_migrations`: obsolete ledgers are dropped, never adopted or
  backed up. `_schema_migrations` is the only migration ledger.
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
- `_identity_definition_versions`: this was a legacy bridge into the generic
  Metadata definition-version store, not a current Identity table. Generic
  versions live in `_metadata_definition_versions`; Identity-owned profile
  binding and role versions live in
  `_identity_profile_binding_definition_versions` and
  `_identity_role_definition_versions`.
- Unpublished compatibility copies, rename migrations, and backup tables for
  the retired names are not created.
