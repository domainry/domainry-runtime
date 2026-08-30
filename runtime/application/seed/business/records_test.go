package businessseed

import (
	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"

	"path/filepath"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"

	changeplanpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/changeplan"

	appschemapersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/appschema"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestManifestBusinessSeedRecordsPersistSourceProvenance(t *testing.T) {
	store, err := persistence.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "seed-provenance.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	manifest := manifestmodel.ManifestSchema{TemplateID: "crm", Version: "2.1.0", Name: "CRM", Objects: []definitionmodel.ObjectSchema{{Key: "customer", Name: "Customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Name: "Name", Type: "text"}}}}, SeedRecords: []businessseedmodel.SeedRecordSchema{{ObjectKey: "customer", SourceKind: "plugin", SourceID: "crm-baseline", Data: map[string]any{"__seed_key": "customer_acme", "name": "Acme"}}}}
	metadataStore := appschemapersistence.NewApplicationSchemaStore(store)
	if err := metadataStore.EnsureManifestMetadata(t.Context(), manifest); err != nil {
		t.Fatal(err)
	}
	if err := metadataStore.SyncManifestStorage(t.Context(), manifest); err != nil {
		t.Fatal(err)
	}
	if err := SyncManifestBusinessSeeds(t.Context(), recordpersistence.NewRecordStore(store), changeplanpersistence.NewBusinessEvidenceStore(store), manifest, ManifestBusinessSeedRowsFromManifest(manifest)); err != nil {
		t.Fatal(err)
	}
	provenance, err := changeplanpersistence.NewBusinessEvidenceStore(store).ListSeedProvenance(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(provenance) != 1 || provenance[0].SeedKey != "customer_acme" || provenance[0].RecordID != "customer_customer_acme" || provenance[0].SourceKind != "plugin" || provenance[0].SourceID != "crm-baseline" || provenance[0].TemplateVersion != manifest.Version || provenance[0].ContentHash == "" {
		t.Fatalf("unexpected seed provenance: %#v", provenance)
	}
	if provenance[0].ContentHash == "Acme" {
		t.Fatal("seed provenance must not expose domain payload")
	}
}
