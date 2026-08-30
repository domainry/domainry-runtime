package automation

import (
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	lifecyclepersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/lifecyclemodule"
)

func LifecycleExecutor(store *database.RuntimeStore, archives lifecyclecontract.ArchiveStore) lifecyclecontract.OwnerLifecycleExecutor {
	statuses := []string{"succeeded", "completed"}
	failures := []string{"failed", "dead_letter", "cancelled"}
	return lifecyclepersistence.NewRelationalOwnerExecutor(store, archives, "automation",
		lifecyclepersistence.RelationalCleanupSpec{PolicyKey: "automation.execution.v1", Table: "_automation_rule_executions", IDColumn: "id", TenantColumn: "workspace_id", TimeColumn: "updated_at", StatusColumn: "status", EligibleStatuses: statuses, RetentionGroup: "succeeded"},
		lifecyclepersistence.RelationalCleanupSpec{PolicyKey: "automation.execution.v1", Table: "_automation_rule_executions", IDColumn: "id", TenantColumn: "workspace_id", TimeColumn: "updated_at", StatusColumn: "status", EligibleStatuses: failures, RetentionGroup: "failed"},
		lifecyclepersistence.RelationalCleanupSpec{PolicyKey: "automation.execution.v1", Table: "_automation_instruction_executions", IDColumn: "id", TenantColumn: "workspace_id", TimeColumn: "updated_at", StatusColumn: "status", EligibleStatuses: statuses, RetentionGroup: "succeeded"},
		lifecyclepersistence.RelationalCleanupSpec{PolicyKey: "automation.execution.v1", Table: "_automation_instruction_executions", IDColumn: "id", TenantColumn: "workspace_id", TimeColumn: "updated_at", StatusColumn: "status", EligibleStatuses: failures, RetentionGroup: "failed"},
	)
}
