package auditmodule

import (
	lifecyclecontract "github.com/domainry/domainry-lifecycle/contract"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	lifecyclepersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/lifecyclemodule"
)

func LifecycleExecutor(store *database.RuntimeStore) lifecyclecontract.OwnerLifecycleExecutor {
	return lifecyclepersistence.NewRelationalOwnerExecutor(store, "audit", lifecyclepersistence.RelationalCleanupSpec{
		PolicyKey: "audit.evidence.v1", Table: "_audit_events", IDColumn: "id", TenantColumn: "workspace_id", TimeColumn: "created_at",
	})
}
