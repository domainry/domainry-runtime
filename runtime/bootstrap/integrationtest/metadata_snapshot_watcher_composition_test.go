package integrationtest

import (
	"context"
	"encoding/json"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"path/filepath"
	"testing"
	"time"

	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"

	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"

	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"

	metadatapersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/metadata"

	workflowpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workflow"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestMetadataSnapshotWatcherInvalidatesSecondRuntimeFromSharedDatabase(t *testing.T) {
	store, err := persistence.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "watcher.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	manifest := manifestmodel.ManifestSchema{TemplateID: "shared", Version: "1", Name: "Shared", Objects: []definitionmodel.ObjectSchema{{Key: "account", Name: "Account"}}}
	if err := metadatapersistence.NewMetadataStore(store).EnsureManifestMetadata(t.Context(), manifest); err != nil {
		t.Fatal(err)
	}
	repository := metadatapersistence.NewMetadataStore(store)
	second := composition.NewRuntimeServices(t.Context(), composition.RuntimeServicesConfig{
		Manifest:     manifestmodel.ManifestSchema{TemplateID: "shared", Version: "1", Name: "Shared", Objects: manifest.Objects},
		Dependencies: composition.RuntimeServicesDependencies{Metadata: repository, WorkflowDefinitions: workflowpersistence.NewWorkflowDefinitionStore(store)},
	})
	ctx, cancel := context.WithCancel(t.Context())
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "test metadata snapshot watcher")
	done := second.Applications().ApplicationSchema.StartSnapshotWatcher(ctx, 5*time.Millisecond, scope)
	time.Sleep(15 * time.Millisecond)
	current, ok, err := repository.GetDefinition(t.Context(), scope, "object", "account")
	if err != nil || !ok {
		t.Fatalf("current=%#v ok=%v err=%v", current, ok, err)
	}
	if _, err := repository.PublishDefinition(t.Context(), scope, "object", "account", metadatamodel.MetadataDefinitionUpsertRequest{ExpectedSchemaHash: &current.SchemaHash, Payload: json.RawMessage(`{"key":"account","name":"Business Account"}`)}, auditmodel.AuditEvent{ID: "metadata-watcher-update", WorkspaceID: principalmodel.InstallationWorkspaceID, Event: "metadata_definition.saved", CreatedAt: time.Now().UTC().Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		snapshot := second.Schema()
		if len(snapshot.Objects) == 1 && snapshot.Objects[0].Name == "Business Account" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("second runtime did not invalidate schema: %#v", snapshot.Objects)
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("second runtime watcher did not stop")
	}
}
