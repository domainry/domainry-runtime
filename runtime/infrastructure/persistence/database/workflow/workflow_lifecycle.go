package workflow

import (
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	lifecyclepersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/lifecyclemodule"
)

func LifecycleExecutor(store *database.RuntimeStore, archives lifecyclecontract.ArchiveStore) lifecyclecontract.OwnerLifecycleExecutor {
	return lifecyclepersistence.NewRelationalOwnerExecutor(store, archives, "workflow",
		lifecyclepersistence.RelationalCleanupSpec{
			PolicyKey: "workflow.definition.v1", Table: "_workflow_definition_versions", IDColumn: "id", TimeColumn: "updated_at", StatusColumn: "status", EligibleStatuses: []string{"archived"},
			ReferenceChecks: []lifecyclepersistence.RelationalReferenceCheck{
				{Table: "_workflow_definitions", ReferenceColumn: "current_draft_version_id"},
				{Table: "_workflow_definitions", ReferenceColumn: "current_published_version_id"},
				{Table: "_workflow_process_instances", ReferenceColumn: "workflow_definition_version_id"},
			},
		},
		lifecyclepersistence.RelationalCleanupSpec{PolicyKey: "workflow.receipt.v1", Table: "_workflow_execution_receipts", IDColumn: "id", TenantColumn: "workspace_id", TimeColumn: "expires_at", StatusColumn: "status", IneligibleStatuses: []string{"processing"}},
		lifecyclepersistence.RelationalCleanupSpec{
			PolicyKey: "workflow.execution.v1", Table: "_workflow_process_instances", IDColumn: "id", TenantColumn: "workspace_id", TimeColumn: "updated_at", StatusColumn: "status",
			EligibleStatuses: []string{"completed", "rejected", "cancelled", "resolved"}, RetentionGroup: "succeeded",
			ChildCollections: []lifecyclepersistence.RelationalChildCollection{
				{Table: "_workflow_process_events", IDColumn: "id", TenantColumn: "workspace_id", ParentColumn: "process_id"},
				{Table: "_workflow_tasks", IDColumn: "id", TenantColumn: "workspace_id", ParentColumn: "process_id"},
				{Table: "_workflow_node_instances", IDColumn: "id", TenantColumn: "workspace_id", ParentColumn: "process_id"},
			},
		},
		lifecyclepersistence.RelationalCleanupSpec{PolicyKey: "workflow.execution.v1", Table: "_workflow_executions", IDColumn: "id", TenantColumn: "workspace_id", TimeColumn: "updated_at", StatusColumn: "status", EligibleStatuses: []string{"succeeded", "completed"}, RetentionGroup: "succeeded"},
		lifecyclepersistence.RelationalCleanupSpec{PolicyKey: "workflow.execution.v1", Table: "_workflow_executions", IDColumn: "id", TenantColumn: "workspace_id", TimeColumn: "updated_at", StatusColumn: "status", EligibleStatuses: []string{"failed", "dead_letter", "cancelled"}, RetentionGroup: "failed"},
	)
}
