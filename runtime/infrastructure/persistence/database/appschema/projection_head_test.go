package appschema

import (
	"testing"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestManifestMaterializationMaintainsOneProjectionHeadAndPurgesDeletedDefinitions(t *testing.T) {
	metadataStore := openStoreForMetadataTest(t)
	store := metadataStore.raw
	for _, table := range []string{"_integration_connector_definitions", "_integration_event_mapping_definitions"} {
		if _, err := store.DB().ExecContext(t.Context(), "CREATE TABLE "+table+" (resource_key TEXT PRIMARY KEY, schema_hash TEXT NOT NULL)"); err != nil {
			t.Fatal(err)
		}
	}
	repository := metadataStore.ApplicationSchemaStore
	first := manifestmodel.ManifestSchema{
		SchemaVersion: "manifest-v1", TemplateID: "shop", Version: "1", Name: "Shop", DefaultLocale: "zh-CN",
		Objects:         []definitionmodel.ObjectSchema{{Key: "account", Name: "Account"}, {Key: "customer", Name: "Customer"}},
		AutomationRules: []automationmodel.AutomationRuleSchema{},
	}
	if err := repository.EnsureManifestMetadata(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	var firstSourceHash, firstSchemaHash, status string
	if err := store.DB().QueryRowContext(t.Context(), `SELECT source_hash, schema_hash, status FROM _application_schema_projection WHERE id = 'current'`).Scan(&firstSourceHash, &firstSchemaHash, &status); err != nil {
		t.Fatal(err)
	}
	if firstSourceHash == "" || firstSchemaHash == "" || status != "materialized" {
		t.Fatalf("first projection source=%q schema=%q status=%q", firstSourceHash, firstSchemaHash, status)
	}
	second := first
	second.Version = "2"
	second.Objects = second.Objects[:1]
	if err := repository.SyncManifestMetadata(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	var secondSourceHash string
	if err := store.DB().QueryRowContext(t.Context(), `SELECT source_hash FROM _application_schema_projection WHERE id = 'current'`).Scan(&secondSourceHash); err != nil {
		t.Fatal(err)
	}
	if secondSourceHash == "" || secondSourceHash == firstSourceHash {
		t.Fatalf("projection source hash did not advance: first=%q second=%q", firstSourceHash, secondSourceHash)
	}
	var projectionRows, deletedRows, tombstones int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _application_schema_projection`).Scan(&projectionRows); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _metadata_object_definitions WHERE resource_key = 'customer'`).Scan(&deletedRows); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _metadata_object_definitions WHERE disabled_at IS NOT NULL`).Scan(&tombstones); err != nil {
		t.Fatal(err)
	}
	if projectionRows != 1 || deletedRows != 0 || tombstones != 0 {
		t.Fatalf("projection rows=%d deleted rows=%d tombstones=%d", projectionRows, deletedRows, tombstones)
	}
}
