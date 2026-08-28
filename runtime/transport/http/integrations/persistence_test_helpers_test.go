package integrations_test

import (
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	integrationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/integration"
)

func integrationConfigRepository(store *database.RuntimeStore) integrationpersistence.IntegrationConfigStore {
	return integrationpersistence.NewIntegrationConfigStore(store)
}

func integrationEventRepository(store *database.RuntimeStore) integrationpersistence.IntegrationEventStore {
	return integrationpersistence.NewIntegrationEventStore(store)
}
