package integrations_test

import (
	"testing"

	integrationmodule "github.com/domainry/domainry-integration/module"
	notificationmodulehost "github.com/domainry/domainry-notification-sdk/modulehost"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

func applyIntegrationOwnerMigrations(t *testing.T, store *database.RuntimeStore) {
	t.Helper()
	migrations, err := integrationmodule.SchemaMigrations(store.Driver(), store.DatabaseSchema())
	if err != nil {
		t.Fatal(err)
	}
	owned := make([]notificationmodulehost.SchemaMigration, len(migrations))
	for index, migration := range migrations {
		owned[index] = notificationmodulehost.SchemaMigration{Version: migration.Version, Name: migration.Name, Statements: append([]string(nil), migration.Statements...)}
	}
	if err := store.ApplyOwnedMigrations(t.Context(), "integration", owned); err != nil {
		t.Fatal(err)
	}
}
