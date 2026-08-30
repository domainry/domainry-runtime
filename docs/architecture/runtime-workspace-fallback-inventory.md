# Runtime Workspace Fallback Inventory

This is the conservative production-code scan for empty Runtime workspace
fallbacks and hard-coded `default` values in Application and Infrastructure.
The baseline is tightened whenever a reviewed fallback is removed. A match remains in
the inventory until it is removed or explicitly adjudicated; provider concepts
such as Asana's own workspace and installation-global default policy keys are
kept visible rather than silently excluded.

Scan scope: production `*.go` files below `runtime/application` and
`runtime/infrastructure`; `*_test.go` is excluded. The scan matches
empty workspace comparisons, workspace/default proximity, and literal
`"default"` / `'default'` values.

| Matches | Production file |
| ---: | --- |
| 1 | `runtime/application/seed/business/records.go` |
| 1 | `runtime/application/seed/globalcapability/runtime.go` |
| 1 | `runtime/application/automation/automation_definition_validation_application_service.go` |
| 2 | `runtime/application/integration/integration_application_delivery_management.go` |
| 1 | `runtime/application/integration/integration_application_execution_evidence.go` |
| 3 | `runtime/application/integration/integration_application_failure_alerts.go` |
| 2 | `runtime/application/integration/integration_application_inbound_webhooks.go` |
| 1 | `runtime/application/appschema/appschema_localized_text_coverage_application_service.go` |
| 1 | `runtime/application/workflow/workflow_execution_idempotency_application_service.go` |
| 3 | `runtime/infrastructure/connectors/delivery_operations/asana/asana_delivery_operations_adapter.go` |
| 1 | `runtime/infrastructure/connectors/delivery_operations/asana/asana_delivery_operations_schema_adapter.go` |
| 1 | `runtime/infrastructure/persistence/database/action/action_business_execution_store.go` |
| 1 | `runtime/infrastructure/persistence/database/automation/sql_values.go` |
| 3 | `runtime/infrastructure/persistence/database/deployment/runtime_status_store.go` |
| 1 | `runtime/infrastructure/persistence/database/integration/integration_worker_scope.go` |
| 1 | `runtime/infrastructure/persistence/database/appschema/manifest_store.go` |
| 1 | `runtime/infrastructure/persistence/database/appschema/definition_store.go` |
| 1 | `runtime/infrastructure/persistence/database/schema/idempotency_receipt_migration.go` |
| 1 | `runtime/infrastructure/persistence/database/schema/evidence_tables.go` |
| 1 | `runtime/infrastructure/persistence/database/transaction/boundary_intent_store.go` |

The next checklist item turns this inventory into an executable exact baseline
that may only decrease. This scan itself does not authorize any fallback.
