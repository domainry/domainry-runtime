package auditmodule

import (
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	lifecyclepersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/lifecyclemodule"
)

func LifecycleExecutor(store *database.RuntimeStore, archives lifecyclecontract.ArchiveStore) lifecyclecontract.OwnerLifecycleExecutor {
	return lifecyclepersistence.NewRelationalOwnerExecutor(store, archives, "audit", lifecyclepersistence.RelationalCleanupSpec{
		PolicyKey: "audit.evidence.v1", Table: "_audit_events", IDColumn: "id", TenantColumn: "workspace_id", TimeColumn: "created_at",
	})
}
