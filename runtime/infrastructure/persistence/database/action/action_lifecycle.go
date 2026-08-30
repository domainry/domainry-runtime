package action

import (
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	lifecyclepersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/lifecyclemodule"
)

func LifecycleExecutor(store *database.RuntimeStore, archives lifecyclecontract.ArchiveStore) lifecyclecontract.OwnerLifecycleExecutor {
	return lifecyclepersistence.NewRelationalOwnerExecutor(store, archives, "action", lifecyclepersistence.RelationalCleanupSpec{
		PolicyKey: "execution.idempotency_receipt.v1", Table: "_action_executions", IDColumn: "id",
		TenantColumn: "workspace_id", TimeColumn: "expires_at", StatusColumn: "status",
		IneligibleStatuses: []string{"pending", "processing"},
	})
}
