package integrationtest

import (
	"encoding/json"
	"testing"
	"time"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
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
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	expectedHash := ""
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = metadataStore(store).PublishDefinition(t.Context(), principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "publish scheduler integration fixture"), "scheduler", key, metadatamodel.MetadataDefinitionUpsertRequest{ExpectedSchemaHash: &expectedHash, Payload: raw}, auditmodel.AuditEvent{ID: "audit_scheduler_" + key, WorkspaceID: principalmodel.InstallationWorkspaceID, Event: "metadata_definition.saved", ObjectKey: "scheduler", RecordID: key, ActorID: "integration-test", CreatedAt: now})
	if err != nil {
		t.Fatalf("publish scheduler definition %s: %v", key, err)
	}
}
