package integrationtest

import (
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	"os"
	"path/filepath"
	"testing"

	. "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	runtimetestkit "github.com/domainry/domainry-runtime/runtime/bootstrap/testkit"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordtimerprojection "github.com/domainry/domainry-runtime/runtime/domain/recordtimer/projection"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	appschemapersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/appschema"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func recordTimerRuntimeSystemScope() principalmodel.SystemScope {
	return principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "test record timer worker operation")
}

func recordTimerInstallationScope() principalmodel.SystemScope {
	return principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "prepare record timer test storage")
}

func openRecordTimerRuntimeTestStore(t *testing.T) *persistence.RuntimeStore {
	t.Helper()
	tempDir := t.TempDir()
	migrationPath := filepath.Join(tempDir, "001_empty.sql")
	if err := os.WriteFile(migrationPath, []byte("-- record timer runtime test migration\n"), 0o644); err != nil {
		t.Fatalf("write migration: %v", err)
	}
	store, err := persistence.OpenContext(t.Context(), config.Config{
		DatabaseDriver: "sqlite",
		DBPath:         filepath.Join(tempDir, "app.db"),
		MigrationSQL:   migrationPath,
	})
	if err != nil {
		t.Fatalf("open record timer test store: %v", err)
	}
	if err := store.EnsureEvidenceSchema(t.Context()); err != nil {
		t.Fatalf("ensure evidence schema: %v", err)
	}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatalf("ensure runtime schema: %v", err)
	}
	return store
}

func metadataStore(store *persistence.RuntimeStore) appschemapersistence.ApplicationSchemaStore {
	return appschemapersistence.NewApplicationSchemaStore(store)
}

func recordTimerRuntimeTestObjects() []definitionmodel.ObjectSchema {
	return recordtimerprojection.RecordTimerSystemObjects()
}

func recordTimerRuntimeObjectByKey(t *testing.T, objects []definitionmodel.ObjectSchema, key string) definitionmodel.ObjectSchema {
	t.Helper()
	for _, object := range objects {
		if object.Key == key {
			return object
		}
	}
	t.Fatalf("object %s not found", key)
	return definitionmodel.ObjectSchema{}
}

func newRecordTimerRuntimeTestService(t *testing.T, store *persistence.RuntimeStore, objects []definitionmodel.ObjectSchema) *RuntimeServices {
	t.Helper()
	return runtimetestkit.NewRuntimeServices(t.Context(), runtimetestkit.RuntimeServicesConfig{
		TemplateID: "record-timer-runtime-test", TemplateVersion: "1", Name: "Record Timer Runtime Test",
		Objects: objects, Integrations: connectormodel.IntegrationSchema{}, Store: store,
	})
}
