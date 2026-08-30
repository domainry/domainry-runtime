package appschema

import (
	"context"
	"database/sql/driver"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestLegacyManifestLoadEveryReadStageFailure(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewApplicationSchemaStore(baseDB)
	for stage := 0; stage <= 22; stage++ {
		steps := []metadataSQLQueryStep{metadataCatalogQueryStep()}
		steps = append(steps, metadataSQLQueryStep{columns: []string{"payload"}, rows: [][]driver.Value{{`{"key":"account","name":"Account"}`}}})
		for len(steps) < 23 {
			steps = append(steps, metadataSQLQueryStep{columns: []string{"payload"}})
		}
		steps[stage] = metadataSQLQueryStep{err: errMetadataSQL}
		repository := scriptedApplicationSchemaStore(t, &metadataSQLState{querySteps: steps}, base)
		if _, err := repository.LoadManifestMetadata(t.Context()); err == nil {
			t.Fatalf("stage %d expected error", stage)
		}
	}
	steps := []metadataSQLQueryStep{metadataCatalogQueryStep()}
	for len(steps) < 23 {
		steps = append(steps, metadataSQLQueryStep{columns: []string{"payload"}})
	}
	if _, err := scriptedApplicationSchemaStore(t, &metadataSQLState{querySteps: steps}, base).LoadManifestMetadata(t.Context()); err == nil {
		t.Fatal("expected missing objects error")
	}
	steps = []metadataSQLQueryStep{metadataCatalogQueryStep(), {columns: []string{"payload"}, rows: [][]driver.Value{{`{"key":"account","name":"Account"}`}}}, {columns: []string{"payload"}}, {columns: []string{"payload"}, rows: [][]driver.Value{{`{"object_key":"account","type":"required"}`}}}}
	for len(steps) < 23 {
		steps = append(steps, metadataSQLQueryStep{columns: []string{"payload"}})
	}
	manifest, err := scriptedApplicationSchemaStore(t, &metadataSQLState{querySteps: steps}, base).LoadManifestMetadata(t.Context())
	if err != nil || len(manifest.Objects) != 1 || manifest.TemplateID != "template" {
		t.Fatalf("manifest=%#v err=%v", manifest, err)
	}
}

func TestLegacyManifestLoadPrimitiveFailures(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewApplicationSchemaStore(baseDB)
	for _, step := range []metadataSQLQueryStep{
		{err: errMetadataSQL},
		{columns: []string{"key"}, rows: [][]driver.Value{{"template_id"}}},
		{columns: []string{"key", "value"}, rows: [][]driver.Value{{"template_id", "template"}}, nextErr: errMetadataSQL},
		{columns: []string{"key", "value"}, rows: [][]driver.Value{{"template_id", ""}, {"template_version", "1"}}},
		{columns: []string{"key", "value"}, rows: [][]driver.Value{{"template_id", "template"}, {"template_version", ""}}},
	} {
		if _, err := scriptedApplicationSchemaStore(t, &metadataSQLState{querySteps: []metadataSQLQueryStep{step}}, base).loadMetadataCatalog(t.Context()); err == nil {
			t.Fatal("expected catalog error")
		}
	}
	for _, step := range []metadataSQLQueryStep{
		{err: errMetadataSQL},
		{columns: []string{"payload", "extra"}, rows: [][]driver.Value{{"{}", "extra"}}},
		{columns: []string{"payload"}, rows: [][]driver.Value{{"{"}}},
		{columns: []string{"payload"}, rows: [][]driver.Value{{"{}"}}, nextErr: errMetadataSQL},
	} {
		repository := scriptedApplicationSchemaStore(t, &metadataSQLState{querySteps: []metadataSQLQueryStep{step}}, base)
		if _, err := loadMetadataSlice[map[string]any](t.Context(), repository, "objects"); err == nil {
			t.Fatal("expected slice error")
		}
	}
}

func TestLegacyMetadataMigrationPlanBranches(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = store.Close() })
	repository := NewApplicationSchemaStore(store)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := repository.ApplicationSchemaMigrationPlan(cancelled, manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "missing"}}}); err == nil {
		t.Fatal("expected cancelled migration plan")
	}
	steps, err := repository.ApplicationSchemaMigrationPlan(t.Context(), manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "missing", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}}})
	if err != nil || len(steps) != 1 || steps[0].Operation != "create_table" {
		t.Fatalf("create steps=%#v err=%v", steps, err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `CREATE TABLE "account" ("id" TEXT)`); err != nil {
		t.Fatal(err)
	}
	steps, err = repository.ApplicationSchemaMigrationPlan(t.Context(), manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "account", Fields: []definitionmodel.FieldSchema{{Key: ""}, {Key: "id"}, {Key: "amount", Type: "number"}}}}})
	if err != nil || len(steps) != 1 || steps[0].Operation != "add_column" || steps[0].ColumnKey != "amount" {
		t.Fatalf("column steps=%#v err=%v", steps, err)
	}
}
