package automation

import (
	lifecyclecontract "github.com/domainry/domainry-lifecycle/contract"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	lifecyclepersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/lifecyclemodule"
)

func LifecycleExecutor(store *database.RuntimeStore) lifecyclecontract.OwnerLifecycleExecutor {
	statuses := []string{"succeeded", "completed"}
	failures := []string{"failed", "dead_letter", "cancelled"}
	return lifecyclepersistence.NewRelationalOwnerExecutor(store, "automation",
		lifecyclepersistence.RelationalCleanupSpec{PolicyKey: "automation.execution.v1", Table: "automation_rule_executions", IDColumn: "id", TenantColumn: "workspace_id", TimeColumn: "updated_at", StatusColumn: "status", EligibleStatuses: statuses, RetentionGroup: "succeeded"},
		lifecyclepersistence.RelationalCleanupSpec{PolicyKey: "automation.execution.v1", Table: "automation_rule_executions", IDColumn: "id", TenantColumn: "workspace_id", TimeColumn: "updated_at", StatusColumn: "status", EligibleStatuses: failures, RetentionGroup: "failed"},
		lifecyclepersistence.RelationalCleanupSpec{PolicyKey: "automation.execution.v1", Table: "automation_instruction_executions", IDColumn: "id", TenantColumn: "workspace_id", TimeColumn: "updated_at", StatusColumn: "status", EligibleStatuses: statuses, RetentionGroup: "succeeded"},
		lifecyclepersistence.RelationalCleanupSpec{PolicyKey: "automation.execution.v1", Table: "automation_instruction_executions", IDColumn: "id", TenantColumn: "workspace_id", TimeColumn: "updated_at", StatusColumn: "status", EligibleStatuses: failures, RetentionGroup: "failed"},
	)
}
