package lifecyclemodule

import (
	"github.com/domainry/domainry-lifecycle/contract"
	lifecyclepersistence "github.com/domainry/domainry-lifecycle/persistence"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type RelationalCleanupSpec = lifecyclepersistence.RelationalCleanupSpec
type RelationalReferenceCheck = lifecyclepersistence.RelationalReferenceCheck
type RelationalChildCollection = lifecyclepersistence.RelationalChildCollection
type ArchiveWriter = lifecyclepersistence.ArchiveWriter

func NewRelationalOwnerExecutor(store *database.RuntimeStore, owner string, specs ...RelationalCleanupSpec) contract.OwnerLifecycleExecutor {
	if store == nil {
		return lifecyclepersistence.NewRelationalOwnerExecutor(nil, owner, specs...)
	}
	return lifecyclepersistence.NewRelationalOwnerExecutor(NewHost(store), owner, specs...)
}

func NewArchiveWriter(store *database.RuntimeStore) ArchiveWriter {
	if store == nil {
		return lifecyclepersistence.NewArchiveWriter(nil)
	}
	return lifecyclepersistence.NewArchiveWriter(NewHost(store))
}
