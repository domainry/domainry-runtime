package appschema

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func metadataCatalogQueryStep() metadataSQLQueryStep {
	return metadataSQLQueryStep{
		columns: []string{"template_id", "artifact_version", "default_locale", "name", "contract_version", "schema_hash", "source_hash"},
		rows:    [][]driver.Value{{"template", "1", "en", "Application", "1", "schema-hash", "source-hash"}},
	}
}

func metadataInstallScope() principalmodel.SystemScope {
	return principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "metadata failure test")
}

func TestLoadManifestPropagatesEveryReadStageFailure(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewApplicationSchemaStore(baseDB)
	for stage := 0; stage < 10; stage++ {
		steps := []metadataSQLQueryStep{metadataCatalogQueryStep()}
		steps = append(steps, metadataSQLQueryStep{columns: []string{"payload"}, rows: [][]driver.Value{{`{"key":"account","name":"Account"}`}}})
		for len(steps) < 10 {
			steps = append(steps, metadataSQLQueryStep{columns: []string{"payload"}})
		}
		steps[stage] = metadataSQLQueryStep{err: errMetadataSQL}
		repository := scriptedApplicationSchemaStore(t, &metadataSQLState{querySteps: steps}, base)
		if _, err := repository.LoadManifest(t.Context(), metadataInstallScope()); err == nil {
			t.Fatalf("stage %d expected error", stage)
		}
	}
	steps := []metadataSQLQueryStep{metadataCatalogQueryStep()}
	for len(steps) < 10 {
		steps = append(steps, metadataSQLQueryStep{columns: []string{"payload"}})
	}
	if _, err := scriptedApplicationSchemaStore(t, &metadataSQLState{querySteps: steps}, base).LoadManifest(t.Context(), metadataInstallScope()); err == nil {
		t.Fatal("expected no objects error")
	}
	if _, err := base.LoadManifest(t.Context(), principalmodel.SystemScope{}); err == nil {
		t.Fatal("expected scope error")
	}
}

func TestManifestReadPrimitiveFailures(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewApplicationSchemaStore(baseDB)
	for _, step := range []metadataSQLQueryStep{
		{err: errMetadataSQL},
		{columns: []string{"key"}, rows: [][]driver.Value{{"template_id"}}},
		{columns: []string{"key", "value"}, nextErr: errMetadataSQL},
		{columns: []string{"key", "value"}, rows: [][]driver.Value{{"template_id", ""}, {"template_version", "1"}}},
		{columns: []string{"key", "value"}, rows: [][]driver.Value{{"template_id", "template"}, {"template_version", ""}}},
	} {
		if _, err := scriptedApplicationSchemaStore(t, &metadataSQLState{querySteps: []metadataSQLQueryStep{step}}, base).loadCatalog(t.Context()); err == nil {
			t.Fatal("expected catalog error")
		}
	}

	for _, step := range []metadataSQLQueryStep{
		{err: errMetadataSQL},
		{columns: []string{"payload", "extra"}, rows: [][]driver.Value{{"{}", "x"}}},
		{columns: []string{"payload"}, rows: [][]driver.Value{{"{"}}},
		{columns: []string{"payload"}, nextErr: errMetadataSQL},
	} {
		repository := scriptedApplicationSchemaStore(t, &metadataSQLState{querySteps: []metadataSQLQueryStep{step}}, base)
		if _, err := loadMetadataSliceContext[map[string]any](t.Context(), repository.database(), repository.store, "objects"); err == nil {
			t.Fatal("expected slice error")
		}
	}
}

func TestManifestDefinitionReadFailures(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewApplicationSchemaStore(baseDB)
	for _, step := range []metadataSQLQueryStep{
		{err: errMetadataSQL},
		{columns: []string{"resource_key"}, rows: [][]driver.Value{{"key"}}},
		{columns: metadataDefinitionColumns(), rows: [][]driver.Value{metadataDefinitionRow("1", "hash", nil)}, nextErr: errMetadataSQL},
	} {
		repository := scriptedApplicationSchemaStore(t, &metadataSQLState{querySteps: []metadataSQLQueryStep{step}}, base)
		if _, err := repository.ListDefinitions(t.Context(), metadataInstallScope(), "object"); err == nil {
			t.Fatal("expected list error")
		}
	}
	if _, err := base.ListDefinitions(t.Context(), principalmodel.SystemScope{}, "object"); err == nil {
		t.Fatal("expected scope error")
	}
	if _, err := base.ListDefinitions(t.Context(), metadataInstallScope(), "missing"); err == nil {
		t.Fatal("expected type error")
	}
}

func metadataDefinitionColumns() []string {
	return []string{"resource_key", "object_key", "name", "payload_json", "schema_version", "schema_hash", "source_kind", "source_id", "disabled_at", "created_at", "updated_at"}
}

func metadataDefinitionRow(version, hash string, disabled driver.Value) []driver.Value {
	return []driver.Value{"account", "account", "Account", `{"key":"account"}`, version, hash, "generated", "template", disabled, "2026-01-01", "2026-01-02"}
}

func TestManifestDefinitionGetAndScanBranches(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewApplicationSchemaStore(baseDB)
	for _, testCase := range []struct {
		name     string
		step     metadataSQLQueryStep
		found    bool
		disabled string
		err      bool
	}{
		{name: "missing", step: metadataSQLQueryStep{columns: metadataDefinitionColumns()}},
		{name: "query error", step: metadataSQLQueryStep{err: errMetadataSQL}, err: true},
		{name: "scan error", step: metadataSQLQueryStep{columns: []string{"resource_key"}, rows: [][]driver.Value{{"account"}}}, err: true},
		{name: "enabled", step: metadataSQLQueryStep{columns: metadataDefinitionColumns(), rows: [][]driver.Value{metadataDefinitionRow("1", "hash", nil)}}, found: true},
		{name: "disabled", step: metadataSQLQueryStep{columns: metadataDefinitionColumns(), rows: [][]driver.Value{metadataDefinitionRow("1", "hash", "2026-01-03")}}, found: true, disabled: "2026-01-03"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repository := scriptedApplicationSchemaStore(t, &metadataSQLState{querySteps: []metadataSQLQueryStep{testCase.step}}, base)
			definition, found, err := repository.GetDefinition(t.Context(), metadataInstallScope(), "object", "account")
			if (err != nil) != testCase.err || found != testCase.found || definition.DisabledAt != testCase.disabled {
				t.Fatalf("definition=%#v found=%v err=%v", definition, found, err)
			}
		})
	}
	if _, _, err := base.GetDefinition(t.Context(), principalmodel.SystemScope{}, "object", "account"); err == nil {
		t.Fatal("expected scope error")
	}
	if _, _, err := base.GetDefinition(t.Context(), metadataInstallScope(), "missing", "account"); err == nil {
		t.Fatal("expected type error")
	}
	if _, err := scanApplicationDefinition(metadataScannerFunc(func(...any) error { return sql.ErrConnDone }), "object"); !errors.Is(err, sql.ErrConnDone) {
		t.Fatalf("scan error=%v", err)
	}
}

type metadataScannerFunc func(...any) error

func (f metadataScannerFunc) Scan(values ...any) error { return f(values...) }
