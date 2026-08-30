package notificationpublication

import (
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	ormbuilder "github.com/domainry/domainry-orm/query"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	lifecyclepersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/lifecyclemodule"
)

func LifecycleExecutor(store *database.RuntimeStore, archives lifecyclecontract.ArchiveStore) lifecyclecontract.OwnerLifecycleExecutor {
	return lifecyclepersistence.NewRelationalOwnerExecutor(store, archives, "runtime_handoff", lifecyclepersistence.RelationalCleanupSpec{
		PolicyKey: "runtime.publication_handoff.v1", Table: "runtime_publication_outbox", IDColumn: "id", TenantColumn: "workspace_id", TimeColumn: "updated_at", StatusColumn: "status",
		IneligibleStatuses: []string{"pending", "processing", "retrying"},
		AdditionalPredicate: func(string) ormbuilder.Predicate {
			return ormbuilder.Equal("publication_type", "integration.connector")
		},
	})
}
