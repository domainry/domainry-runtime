package workflow

import (
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	"github.com/domainry/domainry-orm/query"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	lifecyclepersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/lifecyclemodule"
)

func LifecycleExecutor(store *database.RuntimeStore, archives lifecyclecontract.ArchiveStore) lifecyclecontract.OwnerLifecycleExecutor {
	return lifecyclepersistence.NewRelationalOwnerExecutor(store, archives, "workflow",
		lifecyclepersistence.RelationalCleanupSpec{PolicyKey: "workflow.receipt.v1", Table: "_operations", IDColumn: "id", TenantColumn: "workspace_id", TimeColumn: "expires_at", StatusColumn: "status", IneligibleStatuses: []string{"processing"}, AdditionalPredicate: func(string) query.Predicate {
			return query.And(query.Equal("owner", workflowReceiptOwner), query.Equal("kind", workflowReceiptKind))
		}},
		lifecyclepersistence.RelationalCleanupSpec{
			PolicyKey: "workflow.execution.v1", Table: "_workflow_process_instances", IDColumn: "id", TenantColumn: "workspace_id", TimeColumn: "updated_at", StatusColumn: "status",
			EligibleStatuses: []string{"completed", "rejected", "cancelled", "resolved"}, RetentionGroup: "succeeded",
			ChildCollections: []lifecyclepersistence.RelationalChildCollection{
				{Table: "_workflow_process_events", IDColumn: "id", TenantColumn: "workspace_id", ParentColumn: "process_id"},
				{Table: "_workflow_tasks", IDColumn: "id", TenantColumn: "workspace_id", ParentColumn: "process_id"},
				{Table: "_workflow_node_instances", IDColumn: "id", TenantColumn: "workspace_id", ParentColumn: "process_id"},
				{Table: "_workflow_route_steps", IDColumn: "id", TenantColumn: "workspace_id", ParentColumn: "process_id"},
			},
		},
		lifecyclepersistence.RelationalCleanupSpec{PolicyKey: "workflow.execution.v1", Table: "_workflow_executions", IDColumn: "id", TenantColumn: "workspace_id", TimeColumn: "updated_at", StatusColumn: "status", EligibleStatuses: []string{"succeeded", "completed"}, RetentionGroup: "succeeded"},
		lifecyclepersistence.RelationalCleanupSpec{PolicyKey: "workflow.execution.v1", Table: "_workflow_executions", IDColumn: "id", TenantColumn: "workspace_id", TimeColumn: "updated_at", StatusColumn: "status", EligibleStatuses: []string{"failed", "dead_letter", "cancelled"}, RetentionGroup: "failed"},
	)
}
