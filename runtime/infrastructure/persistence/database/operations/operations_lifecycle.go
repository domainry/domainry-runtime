package operations

import (
	lifecyclecontract "github.com/domainry/domainry-lifecycle/contract"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	lifecyclepersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/lifecyclemodule"
)

func LifecycleExecutor(store *database.RuntimeStore) lifecyclecontract.OwnerLifecycleExecutor {
	return lifecyclepersistence.NewRelationalOwnerExecutor(store, "operations",
		lifecyclepersistence.RelationalCleanupSpec{PolicyKey: "operations.receipt.v1", Table: "runtime_operations", IDColumn: "id", TenantColumn: "workspace_id", TimeColumn: "updated_at", StatusColumn: "status", IneligibleStatuses: []string{"pending", "running", "pausing"}},
		lifecyclepersistence.RelationalCleanupSpec{PolicyKey: "operations.break_glass.v1", Table: "runtime_break_glass_grants", IDColumn: "id", TenantColumn: "workspace_id", TimeColumn: "expires_at"},
	)
}
