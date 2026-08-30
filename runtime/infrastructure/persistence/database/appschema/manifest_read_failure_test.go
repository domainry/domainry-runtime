package appschema

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func metadataCatalogQueryStep() metadataSQLQueryStep {
	return metadataSQLQueryStep{columns: []string{"key", "value"}, rows: [][]driver.Value{{"template_id", "template"}, {"template_version", "1"}}}
}

func metadataInstallScope() principalmodel.SystemScope {
	return principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "metadata failure test")
}

func TestLoadManifestPropagatesEveryReadStageFailure(t *testing.T) {
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
		if _, err := repository.LoadManifest(t.Context(), metadataInstallScope()); err == nil {
			t.Fatalf("stage %d expected error", stage)
		}
	}
	steps := []metadataSQLQueryStep{metadataCatalogQueryStep()}
	for len(steps) < 23 {
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

func TestManifestDefinitionVersionReadAndSortBranches(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewApplicationSchemaStore(baseDB)
	columns := []string{"schema_version", "schema_hash", "payload_json", "created_at"}
	for _, step := range []metadataSQLQueryStep{
		{err: errMetadataSQL},
		{columns: []string{"schema_version"}, rows: [][]driver.Value{{"1"}}},
		{columns: columns, nextErr: errMetadataSQL},
	} {
		repository := scriptedApplicationSchemaStore(t, &metadataSQLState{querySteps: []metadataSQLQueryStep{step}}, base)
		if _, err := repository.ListDefinitionVersions(t.Context(), metadataInstallScope(), "object", "account"); err == nil {
			t.Fatal("expected version read error")
		}
	}
	for _, testCase := range []struct {
		rows  [][]driver.Value
		order []string
	}{
		{rows: [][]driver.Value{{"2", "two", `{}`, "2025-01-01"}, {"10", "ten", `{}`, "2024-01-01"}}, order: []string{"10", "2"}},
		{rows: [][]driver.Value{{"alpha", "a", `{}`, "same"}, {"2", "two", `{}`, "same"}}, order: []string{"alpha", "2"}},
		{rows: [][]driver.Value{{"2", "two", `{}`, "same"}, {"2", "same", `{}`, "same"}}, order: []string{"2", "2"}},
		{rows: [][]driver.Value{{"alpha", "a", `{}`, "2026-01-01"}, {"beta", "b", `{}`, "2026-01-01"}}, order: []string{"beta", "alpha"}},
		{rows: [][]driver.Value{{"old", "old", `{}`, "2023-01-01"}, {"new", "new", `{}`, "2026-01-02"}}, order: []string{"new", "old"}},
	} {
		repository := scriptedApplicationSchemaStore(t, &metadataSQLState{querySteps: []metadataSQLQueryStep{{columns: columns, rows: testCase.rows}}}, base)
		versions, err := repository.ListDefinitionVersions(t.Context(), metadataInstallScope(), "object", "account")
		if err != nil || len(versions) != 2 || versions[0].SchemaVersion != testCase.order[0] || versions[1].SchemaVersion != testCase.order[1] {
			t.Fatalf("versions=%#v err=%v", versions, err)
		}
	}
	if _, err := base.ListDefinitionVersions(t.Context(), principalmodel.SystemScope{}, "object", "account"); err == nil {
		t.Fatal("expected version scope error")
	}
}

func TestApplicationDefinitionReplayBranches(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewApplicationSchemaStore(baseDB)
	definitionStep := func(version, hash string) metadataSQLQueryStep {
		return metadataSQLQueryStep{columns: metadataDefinitionColumns(), rows: [][]driver.Value{metadataDefinitionRow(version, hash, nil)}}
	}
	stringPtr := func(value string) *string { return &value }
	tests := []struct {
		name     string
		steps    []metadataSQLQueryStep
		target   string
		expected *string
		found    bool
		wantErr  bool
	}{
		{name: "read error", steps: []metadataSQLQueryStep{{err: errMetadataSQL}}, target: "hash", wantErr: true},
		{name: "missing", steps: []metadataSQLQueryStep{{columns: metadataDefinitionColumns()}}, target: "hash"},
		{name: "missing expected conflict", steps: []metadataSQLQueryStep{{columns: metadataDefinitionColumns()}}, target: "hash", expected: stringPtr("old"), wantErr: true},
		{name: "target differs", steps: []metadataSQLQueryStep{definitionStep("2", "hash")}, target: "other"},
		{name: "target differs expected conflict", steps: []metadataSQLQueryStep{definitionStep("2", "hash")}, target: "other", expected: stringPtr("old"), wantErr: true},
		{name: "nil expected replay", steps: []metadataSQLQueryStep{definitionStep("2", "hash")}, target: "hash", found: true},
		{name: "matching expected replay", steps: []metadataSQLQueryStep{definitionStep("2", "hash")}, target: "hash", expected: stringPtr(" hash "), found: true},
		{name: "create replay", steps: []metadataSQLQueryStep{definitionStep("1", "hash")}, target: "hash", expected: stringPtr(" "), found: true},
		{name: "empty expected later version conflict", steps: []metadataSQLQueryStep{definitionStep("2", "hash"), {columns: []string{"schema_version", "schema_hash", "payload_json", "created_at"}}}, target: "hash", expected: stringPtr(" "), wantErr: true},
		{name: "version read error", steps: []metadataSQLQueryStep{definitionStep("2", "hash"), {err: errMetadataSQL}}, target: "hash", expected: stringPtr("old"), wantErr: true},
		{name: "current version hash differs in history", steps: []metadataSQLQueryStep{definitionStep("2", "hash"), {columns: []string{"schema_version", "schema_hash", "payload_json", "created_at"}, rows: [][]driver.Value{{"2", "other", `{}`, "2026-01-02"}}}}, target: "hash", expected: stringPtr("old"), wantErr: true},
		{name: "current version is final history row", steps: []metadataSQLQueryStep{definitionStep("2", "hash"), {columns: []string{"schema_version", "schema_hash", "payload_json", "created_at"}, rows: [][]driver.Value{{"2", "hash", `{}`, "2026-01-02"}}}}, target: "hash", expected: stringPtr("old"), wantErr: true},
		{name: "previous version replay", steps: []metadataSQLQueryStep{definitionStep("2", "hash"), {columns: []string{"schema_version", "schema_hash", "payload_json", "created_at"}, rows: [][]driver.Value{{"2", "hash", `{}`, "2026-01-02"}, {"1", "old", `{}`, "2026-01-01"}}}}, target: "hash", expected: stringPtr("old"), found: true},
		{name: "unrelated version conflict", steps: []metadataSQLQueryStep{definitionStep("2", "hash"), {columns: []string{"schema_version", "schema_hash", "payload_json", "created_at"}, rows: [][]driver.Value{{"2", "hash", `{}`, "2026-01-02"}, {"1", "other", `{}`, "2026-01-01"}}}}, target: "hash", expected: stringPtr("old"), wantErr: true},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			repository := scriptedApplicationSchemaStore(t, &metadataSQLState{querySteps: append([]metadataSQLQueryStep(nil), testCase.steps...)}, base)
			_, found, err := repository.metadataDefinitionReplay(t.Context(), metadataInstallScope(), "object", "account", testCase.target, testCase.expected)
			if found != testCase.found || (err != nil) != testCase.wantErr {
				t.Fatalf("found=%v err=%v", found, err)
			}
		})
	}
}
