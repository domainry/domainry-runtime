package integrationtest

import (
	"os"
	"path/filepath"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	. "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	runtimetestkit "github.com/domainry/domainry-runtime/runtime/bootstrap/testkit"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	schedulerprojection "github.com/domainry/domainry-runtime/runtime/domain/scheduler/projection"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	metadatapersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/metadata"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func schedulerRuntimeSystemScope() principalmodel.SystemScope {
	return principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "test record timer worker operation")
}

func openSchedulerRuntimeTestStore(t *testing.T) *persistence.RuntimeStore {
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

func metadataStore(store *persistence.RuntimeStore) metadatapersistence.MetadataStore {
	return metadatapersistence.NewMetadataStore(store)
}

func schedulerRuntimeTestObjects() []definitionmodel.ObjectSchema {
	return schedulerprojection.SchedulerSystemObjects()
}

func schedulerRuntimeObjectByKey(t *testing.T, objects []definitionmodel.ObjectSchema, key string) definitionmodel.ObjectSchema {
	t.Helper()
	for _, object := range objects {
		if object.Key == key {
			return object
		}
	}
	t.Fatalf("object %s not found", key)
	return definitionmodel.ObjectSchema{}
}

func schedulerRuntimePrincipal() principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{
		Known: true, UserID: "record-timer-worker", WorkspaceID: "default",
	}}, accessfixture.Bundle{
		Key: "admin", Permissions: []string{"workspace.admin", "scheduler.command"}, RecordScope: "all_records",
	})
}

func newSchedulerRuntimeTestService(t *testing.T, store *persistence.RuntimeStore, objects []definitionmodel.ObjectSchema) *RuntimeServices {
	t.Helper()
	return runtimetestkit.NewRuntimeServices(t.Context(), runtimetestkit.RuntimeServicesConfig{
		TemplateID: "record-timer-runtime-test", TemplateVersion: "1", Name: "Record Timer Runtime Test",
		Objects: objects, Integrations: integrationmodel.IntegrationSchema{}, Store: store,
	})
}
