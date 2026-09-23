package automation

import (
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	"github.com/domainry/domainry-orm/query"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	lifecyclepersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/lifecyclemodule"
)

func LifecycleExecutor(store *database.RuntimeStore, archives lifecyclecontract.ArchiveStore) lifecyclecontract.OwnerLifecycleExecutor {
	statuses := []string{"succeeded", "completed"}
	failures := []string{"failed", "dead_letter", "cancelled"}
	runKind := func(kind string) func(string) query.Predicate {
		return func(string) query.Predicate { return query.Equal("run_kind", kind) }
	}
	return lifecyclepersistence.NewRelationalOwnerExecutor(store, archives, "automation",
		lifecyclepersistence.RelationalCleanupSpec{PolicyKey: "automation.execution.v1", Table: automationRunsTable, IDColumn: "id", TenantColumn: "workspace_id", TimeColumn: "updated_at", StatusColumn: "status", EligibleStatuses: statuses, RetentionGroup: "succeeded", AdditionalPredicate: runKind(automationRuleRunKind)},
		lifecyclepersistence.RelationalCleanupSpec{PolicyKey: "automation.execution.v1", Table: automationRunsTable, IDColumn: "id", TenantColumn: "workspace_id", TimeColumn: "updated_at", StatusColumn: "status", EligibleStatuses: failures, RetentionGroup: "failed", AdditionalPredicate: runKind(automationRuleRunKind)},
		lifecyclepersistence.RelationalCleanupSpec{PolicyKey: "automation.execution.v1", Table: automationRunsTable, IDColumn: "id", TenantColumn: "workspace_id", TimeColumn: "updated_at", StatusColumn: "status", EligibleStatuses: statuses, RetentionGroup: "succeeded", AdditionalPredicate: runKind(automationInstructionKind)},
		lifecyclepersistence.RelationalCleanupSpec{PolicyKey: "automation.execution.v1", Table: automationRunsTable, IDColumn: "id", TenantColumn: "workspace_id", TimeColumn: "updated_at", StatusColumn: "status", EligibleStatuses: failures, RetentionGroup: "failed", AdditionalPredicate: runKind(automationInstructionKind)},
	)
}
