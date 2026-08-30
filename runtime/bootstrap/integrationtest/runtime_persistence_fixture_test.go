package integrationtest

import (
	"testing"

	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

// openRuntimePersistenceFixture gives integration tests an independently owned
// database handle instead of exposing Bootstrap's process-owned store.
func openRuntimePersistenceFixture(t *testing.T, cfg config.Config) *persistence.RuntimeStore {
	t.Helper()
	store, err := persistence.OpenContext(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func publishSchedulerDefinitionFixture(t *testing.T, cfg config.Config, key string, payload map[string]any) {
	t.Helper()
	store := openRuntimePersistenceFixture(t, cfg)
	publishSchedulerDefinitionStoreFixture(t, store, key, payload)
}

func publishSchedulerDefinitionStoreFixture(t *testing.T, store *persistence.RuntimeStore, key string, payload map[string]any) {
	t.Helper()
	repository := metadataStore(store)
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "materialize scheduler integration fixture")
	manifest, err := repository.LoadManifest(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	definition := make(map[string]any, len(payload)+1)
	for field, value := range payload {
		definition[field] = value
	}
	definition["key"] = key
	manifest.SchedulerDefinitions = append(manifest.SchedulerDefinitions, definition)
	if err := repository.SyncManifest(t.Context(), scope, manifestmodel.ManifestSchema(manifest)); err != nil {
		t.Fatalf("materialize scheduler definition %s: %v", key, err)
	}
}
