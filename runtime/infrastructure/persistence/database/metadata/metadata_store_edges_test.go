package metadata

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestMetadataSnapshotRevisionBranches(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewMetadataStore(baseDB)
	if _, err := base.SnapshotRevision(t.Context(), principalmodel.SystemScope{}); err == nil {
		t.Fatal("expected snapshot scope error")
	}
	for _, step := range []metadataSQLQueryStep{
		{columns: []string{"value"}, rows: [][]driver.Value{{" revision "}}},
		{err: errMetadataSQL},
	} {
		repository := scriptedMetadataStore(t, &metadataSQLState{querySteps: []metadataSQLQueryStep{step}}, base)
		revision, err := repository.SnapshotRevision(t.Context(), metadataInstallScope())
		if step.err == nil && (err != nil || revision != "revision") {
			t.Fatalf("revision=%q err=%v", revision, err)
		}
		if step.err != nil && err == nil {
			t.Fatal("expected snapshot read error")
		}
	}
	refreshSteps := []metadataSQLQueryStep{{columns: []string{"value"}}}
	for range metadataCatalogDefinitionTables() {
		refreshSteps = append(refreshSteps, metadataSQLQueryStep{columns: []string{"resource_key", "schema_hash"}})
	}
	refreshSteps = append(refreshSteps, metadataSQLQueryStep{columns: []string{"value"}, rows: [][]driver.Value{{" refreshed "}}})
	repository := scriptedMetadataStore(t, &metadataSQLState{querySteps: refreshSteps, execSteps: []metadataSQLExecStep{{rows: 1}}}, base)
	revision, err := repository.SnapshotRevision(t.Context(), metadataInstallScope())
	if err != nil || revision != "refreshed" {
		t.Fatalf("refreshed revision=%q err=%v", revision, err)
	}
	if _, err := scriptedMetadataStore(t, &metadataSQLState{querySteps: []metadataSQLQueryStep{{columns: []string{"value"}}}, execSteps: []metadataSQLExecStep{{err: errMetadataSQL}}}, base).SnapshotRevision(t.Context(), metadataInstallScope()); err == nil {
		t.Fatal("expected snapshot refresh error")
	}
	withoutDB := MetadataStore{store: base.store}
	if withoutDB.database() != base.store.DB() {
		t.Fatal("database fallback did not return runtime DB")
	}
}

func TestMetadataTableIntrospectionDriverBranches(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewMetadataStore(baseDB)
	for _, testCase := range []struct {
		step metadataSQLQueryStep
		want bool
		err  bool
	}{
		{step: metadataSQLQueryStep{err: errMetadataSQL}, err: true},
		{step: metadataSQLQueryStep{columns: []string{"cid", "name", "type", "notnull", "default", "pk"}}, err: true},
		{step: metadataSQLQueryStep{columns: []string{"cid", "name", "type", "notnull", "default", "pk"}, rows: [][]driver.Value{{int64(0), "id", "TEXT", int64(0), nil, int64(1)}}}, want: true},
		{step: metadataSQLQueryStep{columns: []string{"cid"}, rows: [][]driver.Value{{int64(0)}}}, err: true},
		{step: metadataSQLQueryStep{columns: []string{"cid", "name", "type", "notnull", "default", "pk"}, rows: [][]driver.Value{{int64(0), "id", "TEXT", int64(0), nil, int64(1)}}, nextErr: errMetadataSQL}, want: true, err: true},
	} {
		repository := scriptedMetadataStore(t, &metadataSQLState{querySteps: []metadataSQLQueryStep{testCase.step}}, base)
		columns, err := repository.tableColumnsForDriver(t.Context(), "account", "sqlite")
		if columns["id"] != testCase.want || (err != nil) != testCase.err {
			t.Fatalf("sqlite columns=%#v err=%v", columns, err)
		}
	}
	for _, driverName := range []string{"mysql", "postgres", "other"} {
		for _, testCase := range []struct {
			step metadataSQLQueryStep
			want bool
			err  bool
		}{
			{step: metadataSQLQueryStep{err: errMetadataSQL}, err: true},
			{step: metadataSQLQueryStep{columns: []string{"column_name"}}, err: true},
			{step: metadataSQLQueryStep{columns: []string{"column_name"}, rows: [][]driver.Value{{"id"}}}, want: true},
			{step: metadataSQLQueryStep{columns: []string{"column_name", "extra"}, rows: [][]driver.Value{{"id", "extra"}}}, err: true},
			{step: metadataSQLQueryStep{columns: []string{"column_name"}, rows: [][]driver.Value{{"id"}}, nextErr: errMetadataSQL}, want: true, err: true},
		} {
			repository := scriptedMetadataStore(t, &metadataSQLState{querySteps: []metadataSQLQueryStep{testCase.step}}, base)
			columns, err := repository.tableColumnsForDriver(t.Context(), "account", driverName)
			if columns["id"] != testCase.want || (err != nil) != testCase.err {
				t.Fatalf("driver=%s columns=%#v err=%v", driverName, columns, err)
			}
		}
	}
	for _, driverName := range []string{"sqlite", "mysql", "postgres"} {
		for _, testCase := range []struct {
			step metadataSQLQueryStep
			want bool
			err  bool
		}{
			{step: metadataSQLQueryStep{err: errMetadataSQL}},
			{step: metadataSQLQueryStep{columns: []string{"name", "extra"}, rows: [][]driver.Value{{"idx", "extra"}}}, err: true},
			{step: metadataSQLQueryStep{columns: []string{"name"}, rows: [][]driver.Value{{"idx"}}}, want: true},
			{step: metadataSQLQueryStep{columns: []string{"name"}, rows: [][]driver.Value{{"idx"}}, nextErr: errMetadataSQL}, want: true, err: true},
		} {
			repository := scriptedMetadataStore(t, &metadataSQLState{querySteps: []metadataSQLQueryStep{testCase.step}}, base)
			indexes, err := repository.tableIndexesForDriver(t.Context(), "account", driverName)
			if indexes["idx"] != testCase.want || (err != nil) != testCase.err {
				t.Fatalf("driver=%s indexes=%#v err=%v", driverName, indexes, err)
			}
		}
	}
	for _, driverName := range []string{"sqlite", "mysql", "postgres"} {
		for _, step := range []metadataSQLExecStep{{rows: 1}, {err: errMetadataSQL}} {
			state := &metadataSQLState{execSteps: []metadataSQLExecStep{step}}
			repository := scriptedMetadataStore(t, state, base)
			err := repository.dropManagedIndexForDriver(t.Context(), "account", "idx", driverName)
			if (err != nil) != (step.err != nil) {
				t.Fatalf("driver=%s drop err=%v", driverName, err)
			}
			want := `DROP INDEX IF EXISTS "idx"`
			if driverName == "mysql" {
				// The scripted store uses SQLite quoting, while the driver branch
				// still proves the MySQL grammar (no unsupported IF EXISTS, with ON).
				want = `DROP INDEX "idx" ON "account"`
			}
			if len(state.execLog) != 1 || state.execLog[0] != want {
				t.Fatalf("driver=%s drop SQL=%q want %q", driverName, state.execLog, want)
			}
		}
	}
}

func TestMetadataTableColumnTypeIntrospectionDriverBranches(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewMetadataStore(baseDB)
	for _, testCase := range []struct {
		step metadataSQLQueryStep
		want string
		err  bool
	}{
		{step: metadataSQLQueryStep{err: errMetadataSQL}, err: true},
		{step: metadataSQLQueryStep{columns: []string{"cid", "name", "type", "notnull", "default", "pk"}}, err: true},
		{step: metadataSQLQueryStep{columns: []string{"cid"}, rows: [][]driver.Value{{int64(0)}}}, err: true},
		{step: metadataSQLQueryStep{columns: []string{"cid", "name", "type", "notnull", "default", "pk"}, rows: [][]driver.Value{{int64(0), "amount", "TEXT", int64(0), nil, int64(0)}}}, want: "TEXT"},
	} {
		repository := scriptedMetadataStore(t, &metadataSQLState{querySteps: []metadataSQLQueryStep{testCase.step}}, base)
		types, err := repository.tableColumnTypesForDriver(t.Context(), "account", "sqlite")
		if types["amount"] != testCase.want || (err != nil) != testCase.err {
			t.Fatalf("sqlite types=%#v err=%v", types, err)
		}
	}
	rows := [][]driver.Value{
		{"plain", "TEXT", nil, nil},
		{"no_scale", "decimal", int64(12), nil},
		{"typed", "TEXT", int64(12), int64(2)},
		{"decimal_amount", "decimal", int64(12), int64(2)},
		{"numeric_amount", "numeric", int64(9), int64(3)},
	}
	for _, driverName := range []string{"mysql", "postgres", "other"} {
		for _, testCase := range []struct {
			step metadataSQLQueryStep
			want string
			err  bool
		}{
			{step: metadataSQLQueryStep{err: errMetadataSQL}, err: true},
			{step: metadataSQLQueryStep{columns: []string{"column_name", "data_type", "numeric_precision", "numeric_scale"}}, err: true},
			{step: metadataSQLQueryStep{columns: []string{"column_name"}, rows: [][]driver.Value{{"bad"}}}, err: true},
			{step: metadataSQLQueryStep{columns: []string{"column_name", "data_type", "numeric_precision", "numeric_scale"}, rows: rows}, want: "decimal(12,2)"},
			{step: metadataSQLQueryStep{columns: []string{"column_name", "data_type", "numeric_precision", "numeric_scale"}, rows: rows, nextErr: errMetadataSQL}, want: "decimal(12,2)", err: true},
		} {
			repository := scriptedMetadataStore(t, &metadataSQLState{querySteps: []metadataSQLQueryStep{testCase.step}}, base)
			types, err := repository.tableColumnTypesForDriver(t.Context(), "account", driverName)
			if types["decimal_amount"] != testCase.want || (err != nil) != testCase.err {
				t.Fatalf("driver=%s types=%#v err=%v", driverName, types, err)
			}
			if testCase.want != "" && types["numeric_amount"] != "numeric(9,3)" {
				t.Fatalf("driver=%s numeric types=%#v", driverName, types)
			}
		}
	}
}

func TestMetadataRecordStringValuesCoversTypedEmptyAndNilValues(t *testing.T) {
	if got := recordStringValues([]string{" one ", " "}); len(got) != 1 || got[0] != "one" {
		t.Fatalf("strings=%#v", got)
	}
	if got := recordStringValues([]any{" one ", " ", nil}); len(got) != 1 || got[0] != "one" {
		t.Fatalf("values=%#v", got)
	}
	if got := recordStringValues("bad"); len(got) != 0 {
		t.Fatalf("default=%#v", got)
	}
}

func TestEnsureObjectStorageReconcilesIndexesDefaultsAndLegacyWorkspace(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = store.Close() })
	repository := NewMetadataStore(store)
	object := definitionmodel.ObjectSchema{
		Key: "account",
		Fields: []definitionmodel.FieldSchema{
			{Key: "name", Type: "text", Config: map[string]any{"indexed": true}, Default: "unknown"},
			{Key: "code", Type: "text", Unique: true, DefaultValue: "unset"},
			{Key: " ", Type: "text"},
			{Key: "disabled", Type: "text", DisabledAt: "now"},
		},
		Validations: []definitionmodel.ValidationSchema{
			{Type: "required", Fields: []string{"name"}},
			{Type: "composite_unique"},
			{Type: "composite_unique", Fields: []string{" ", "code"}},
		},
	}
	if err := repository.ensureObjectStorage(t.Context(), object); err != nil {
		t.Fatal(err)
	}
	object.Fields = []definitionmodel.FieldSchema{{Key: "name", Type: "text"}, {Key: "code", Type: "text", Config: map[string]any{"indexed": true}}}
	if err := repository.ensureObjectStorage(t.Context(), object); err != nil {
		t.Fatal(err)
	}
	if err := repository.ensureObjectStorage(t.Context(), object); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.MigrationPlan(t.Context(), metadataInstallScope(), manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "account", Fields: []definitionmodel.FieldSchema{{Key: ""}}}}}); err != nil {
		t.Fatal(err)
	}
	object.Fields = []definitionmodel.FieldSchema{{Key: "code", Type: "text"}}
	if err := repository.ensureObjectStorage(t.Context(), object); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `CREATE TABLE "legacy" ("id" TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO "legacy" ("id") VALUES ('one')`); err != nil {
		t.Fatal(err)
	}
	if err := repository.ensureObjectStorage(t.Context(), definitionmodel.ObjectSchema{Key: "legacy"}); err != nil {
		t.Fatal(err)
	}
	var workspace string
	if err := store.DB().QueryRowContext(t.Context(), `SELECT "workspace_id" FROM "legacy" WHERE "id" = 'one'`).Scan(&workspace); err != nil || workspace != principalmodel.InstallationWorkspaceID {
		t.Fatalf("workspace=%q err=%v", workspace, err)
	}
	if metadataDBValue(float32(1.5)) != float64(1.5) || metadataDBValue(2) != float64(2) || metadataDBValue(int64(3)) != float64(3) || metadataDBValue("text") != "text" {
		t.Fatal("metadata DB value conversion failed")
	}
}

func TestMetadataMigrationPlanRequestBranches(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = store.Close() })
	repository := NewMetadataStore(store)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := repository.MigrationPlan(cancelled, metadataInstallScope(), manifestmodel.ManifestSchema{}); err == nil {
		t.Fatal("expected cancelled migration plan")
	}
	duringQuery, cancelDuringQuery := context.WithCancel(t.Context())
	state := &metadataSQLState{querySteps: []metadataSQLQueryStep{{err: errMetadataSQL}}, queryHook: cancelDuringQuery}
	if _, err := scriptedMetadataStore(t, state, repository).MigrationPlan(duringQuery, metadataInstallScope(), manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "account"}}}); err == nil {
		t.Fatal("expected migration plan cancellation during introspection")
	}
	steps, err := repository.MigrationPlan(t.Context(), metadataInstallScope(), manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: " "}, {Key: "missing"}}})
	if err != nil || len(steps) != 1 || steps[0].Operation != "create_table" {
		t.Fatalf("steps=%#v err=%v", steps, err)
	}
	if _, err := repository.MigrationPlan(t.Context(), principalmodel.SystemScope{}, manifestmodel.ManifestSchema{}); err == nil {
		t.Fatal("expected migration scope error")
	}
	if !strings.Contains(repository.metadataFieldIndexName("account", "name", true), "uidx") {
		t.Fatal("unique field index prefix missing")
	}
}

func metadataSQLiteColumnStep(names ...string) metadataSQLQueryStep {
	rows := make([][]driver.Value, 0, len(names))
	for index, name := range names {
		rows = append(rows, []driver.Value{int64(index), name, "TEXT", int64(0), nil, int64(0)})
	}
	return metadataSQLQueryStep{columns: []string{"cid", "name", "type", "notnull", "default", "pk"}, rows: rows}
}

func metadataIndexStep(names ...string) metadataSQLQueryStep {
	rows := make([][]driver.Value, 0, len(names))
	for _, name := range names {
		rows = append(rows, []driver.Value{name})
	}
	return metadataSQLQueryStep{columns: []string{"name"}, rows: rows}
}

func TestEnsureObjectStorageFailureBranches(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewMetadataStore(baseDB)
	run := func(t *testing.T, state metadataSQLState, object definitionmodel.ObjectSchema, failIndexCall int) error {
		t.Helper()
		repository := scriptedMetadataStore(t, &state, base)
		calls := 0
		repository.createIndex = func(context.Context, string, string, bool, ...string) error {
			calls++
			if calls == failIndexCall {
				return errMetadataSQL
			}
			return nil
		}
		return repository.ensureObjectStorage(t.Context(), object)
	}
	account := definitionmodel.ObjectSchema{Key: "account"}
	if err := run(t, metadataSQLState{execSteps: []metadataSQLExecStep{{err: errMetadataSQL}}}, account, 0); err == nil {
		t.Fatal("expected create table error")
	}
	if err := run(t, metadataSQLState{execSteps: []metadataSQLExecStep{{rows: 1}}, querySteps: []metadataSQLQueryStep{{err: errMetadataSQL}}}, account, 0); err == nil {
		t.Fatal("expected column introspection error")
	}
	if err := run(t, metadataSQLState{execSteps: []metadataSQLExecStep{{rows: 1}, {err: errMetadataSQL}}, querySteps: []metadataSQLQueryStep{metadataSQLiteColumnStep("id")}}, account, 0); err == nil {
		t.Fatal("expected workspace column error")
	}
	if err := run(t, metadataSQLState{execSteps: []metadataSQLExecStep{{rows: 1}, {rows: 1}, {err: errMetadataSQL}}, querySteps: []metadataSQLQueryStep{metadataSQLiteColumnStep("id")}}, account, 0); err == nil {
		t.Fatal("expected workspace backfill error")
	}
	baseQueries := func(indexes ...string) []metadataSQLQueryStep {
		return []metadataSQLQueryStep{metadataSQLiteColumnStep("workspace_id", "id", "field"), metadataIndexStep(indexes...)}
	}
	if err := run(t, metadataSQLState{execSteps: []metadataSQLExecStep{{rows: 1}}, querySteps: baseQueries()}, account, 1); err == nil {
		t.Fatal("expected workspace identity index error")
	}
	field := definitionmodel.FieldSchema{Key: "new_field", Type: "text"}
	if err := run(t, metadataSQLState{execSteps: []metadataSQLExecStep{{rows: 1}, {err: errMetadataSQL}}, querySteps: []metadataSQLQueryStep{metadataSQLiteColumnStep("workspace_id", "id"), metadataIndexStep()}}, definitionmodel.ObjectSchema{Key: "account", Fields: []definitionmodel.FieldSchema{field}}, 0); err == nil {
		t.Fatal("expected field column error")
	}
	field = definitionmodel.FieldSchema{Key: "field", Type: "text", Default: "value"}
	if err := run(t, metadataSQLState{execSteps: []metadataSQLExecStep{{rows: 1}, {err: errMetadataSQL}}, querySteps: baseQueries()}, definitionmodel.ObjectSchema{Key: "account", Fields: []definitionmodel.FieldSchema{field}}, 0); err == nil {
		t.Fatal("expected field backfill error")
	}
	normalName := base.metadataFieldIndexName("account", "field", false)
	uniqueName := base.metadataFieldIndexName("account", "field", true)
	field = definitionmodel.FieldSchema{Key: "field", Type: "text", Unique: true}
	if err := run(t, metadataSQLState{execSteps: []metadataSQLExecStep{{rows: 1}, {err: errMetadataSQL}}, querySteps: baseQueries(normalName)}, definitionmodel.ObjectSchema{Key: "account", Fields: []definitionmodel.FieldSchema{field}}, 0); err == nil {
		t.Fatal("expected normal index drop error")
	}
	if err := run(t, metadataSQLState{execSteps: []metadataSQLExecStep{{rows: 1}, {rows: 1}}, querySteps: baseQueries(normalName, uniqueName)}, definitionmodel.ObjectSchema{Key: "account", Fields: []definitionmodel.FieldSchema{field}}, 0); err != nil {
		t.Fatalf("successful normal index replacement: %v", err)
	}
	if err := run(t, metadataSQLState{execSteps: []metadataSQLExecStep{{rows: 1}}, querySteps: baseQueries()}, definitionmodel.ObjectSchema{Key: "account", Fields: []definitionmodel.FieldSchema{field}}, 2); err == nil {
		t.Fatal("expected unique field index error")
	}
	field = definitionmodel.FieldSchema{Key: "field", Type: "text", Config: map[string]any{"indexed": true}}
	if err := run(t, metadataSQLState{execSteps: []metadataSQLExecStep{{rows: 1}, {err: errMetadataSQL}}, querySteps: baseQueries(uniqueName)}, definitionmodel.ObjectSchema{Key: "account", Fields: []definitionmodel.FieldSchema{field}}, 0); err == nil {
		t.Fatal("expected unique index drop error")
	}
	if err := run(t, metadataSQLState{execSteps: []metadataSQLExecStep{{rows: 1}}, querySteps: baseQueries()}, definitionmodel.ObjectSchema{Key: "account", Fields: []definitionmodel.FieldSchema{field}}, 2); err == nil {
		t.Fatal("expected field index error")
	}
	field = definitionmodel.FieldSchema{Key: "field", Type: "text"}
	if err := run(t, metadataSQLState{execSteps: []metadataSQLExecStep{{rows: 1}, {err: errMetadataSQL}}, querySteps: baseQueries(normalName)}, definitionmodel.ObjectSchema{Key: "account", Fields: []definitionmodel.FieldSchema{field}}, 0); err == nil {
		t.Fatal("expected managed index drop error")
	}
	validation := definitionmodel.ValidationSchema{Type: "composite_unique", Fields: []string{"field"}}
	if err := run(t, metadataSQLState{execSteps: []metadataSQLExecStep{{rows: 1}}, querySteps: baseQueries()}, definitionmodel.ObjectSchema{Key: "account", Validations: []definitionmodel.ValidationSchema{validation}}, 2); err == nil {
		t.Fatal("expected composite unique index error")
	}
	validation.Fields = []string{" "}
	if err := run(t, metadataSQLState{execSteps: []metadataSQLExecStep{{rows: 1}}, querySteps: baseQueries()}, definitionmodel.ObjectSchema{Key: "account", Validations: []definitionmodel.ValidationSchema{validation}}, 0); err != nil {
		t.Fatalf("blank composite fields should be skipped: %v", err)
	}
	currencyObject := definitionmodel.ObjectSchema{Key: "account", Fields: []definitionmodel.FieldSchema{{Key: "amount", Type: "currency"}}}
	if err := run(t, metadataSQLState{execSteps: []metadataSQLExecStep{{rows: 1}}, querySteps: []metadataSQLQueryStep{metadataSQLiteColumnStep("workspace_id", "id", "amount"), metadataIndexStep(), {err: errMetadataSQL}}}, currencyObject, 0); err == nil {
		t.Fatal("expected currency type introspection error")
	}

	temporal := definitionmodel.ValidationSchema{Key: "schedule", Type: "temporal_exclusion", Config: map[string]any{"start_field": "starts_at", "end_field": "ends_at", "scope_fields": []any{"owner"}}}
	policyColumns := metadataSQLiteColumnStep("workspace_id", "id", "owner", "starts_at", "ends_at")
	if err := run(t, metadataSQLState{execSteps: []metadataSQLExecStep{{rows: 1}}, querySteps: []metadataSQLQueryStep{policyColumns, metadataIndexStep()}}, definitionmodel.ObjectSchema{Key: "account", Fields: []definitionmodel.FieldSchema{{Key: "owner"}, {Key: "starts_at"}, {Key: "ends_at"}}, Validations: []definitionmodel.ValidationSchema{temporal}}, 5); err == nil {
		t.Fatal("expected temporal index error")
	}
	temporalName := base.temporalExclusionIndexName("account", "schedule", []string{"workspace_id", "owner", "starts_at", "ends_at"})
	if err := run(t, metadataSQLState{execSteps: []metadataSQLExecStep{{rows: 1}}, querySteps: []metadataSQLQueryStep{policyColumns, metadataIndexStep(temporalName)}}, definitionmodel.ObjectSchema{Key: "account", Fields: []definitionmodel.FieldSchema{{Key: "owner"}, {Key: "starts_at"}, {Key: "ends_at"}}, Validations: []definitionmodel.ValidationSchema{temporal}}, 0); err != nil {
		t.Fatalf("existing temporal index: %v", err)
	}
	if err := run(t, metadataSQLState{execSteps: []metadataSQLExecStep{{rows: 1}}, querySteps: baseQueries()}, definitionmodel.ObjectSchema{Key: "account", Validations: []definitionmodel.ValidationSchema{{Key: "bad", Type: "temporal_exclusion"}}}, 0); err == nil {
		t.Fatal("expected invalid temporal policy error")
	}

	related := definitionmodel.ValidationSchema{Key: "limit", Type: "related_aggregate_invariant", Config: map[string]any{"relation_field": "parent_id", "aggregate": "count", "limit_field": "limit", "operator": "lte"}}
	relatedFields := []definitionmodel.FieldSchema{{Key: "parent_id", Validation: definitionmodel.FieldValidation{Target: "parent"}}}
	relatedColumns := metadataSQLiteColumnStep("workspace_id", "id", "parent_id")
	if err := run(t, metadataSQLState{execSteps: []metadataSQLExecStep{{rows: 1}}, querySteps: []metadataSQLQueryStep{relatedColumns, metadataIndexStep()}}, definitionmodel.ObjectSchema{Key: "account", Fields: relatedFields, Validations: []definitionmodel.ValidationSchema{related}}, 3); err == nil {
		t.Fatal("expected related aggregate index error")
	}
	relatedName := base.relatedAggregateIndexName("account", "limit", []string{"workspace_id", "parent_id"})
	if err := run(t, metadataSQLState{execSteps: []metadataSQLExecStep{{rows: 1}}, querySteps: []metadataSQLQueryStep{relatedColumns, metadataIndexStep(relatedName)}}, definitionmodel.ObjectSchema{Key: "account", Fields: relatedFields, Validations: []definitionmodel.ValidationSchema{related}}, 0); err != nil {
		t.Fatalf("existing related aggregate index: %v", err)
	}
	if err := run(t, metadataSQLState{execSteps: []metadataSQLExecStep{{rows: 1}}, querySteps: baseQueries()}, definitionmodel.ObjectSchema{Key: "account", Validations: []definitionmodel.ValidationSchema{{Key: "bad", Type: "related_aggregate_invariant"}}}, 0); err == nil {
		t.Fatal("expected invalid related aggregate policy error")
	}
}
