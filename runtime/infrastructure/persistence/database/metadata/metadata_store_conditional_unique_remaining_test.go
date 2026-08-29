package metadata

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
)

func metadataConditionalUniqueObject(value string) definitionmodel.ObjectSchema {
	return definitionmodel.ObjectSchema{
		Key: "booking",
		Fields: []definitionmodel.FieldSchema{
			{Key: "member_id", Type: "relation"},
			{Key: "status", Type: "select"},
		},
		Validations: []definitionmodel.ValidationSchema{{
			Key:    "one_active",
			Type:   recordvalidation.ConditionalUniqueValidationType,
			Fields: []string{"member_id"},
			Config: map[string]any{
				"condition_field":  "status",
				"condition_values": []any{value},
			},
		}},
	}
}

func metadataInformationSchemaColumnStep(names ...string) metadataSQLQueryStep {
	names = appendRecordSystemColumns(names)
	rows := make([][]driver.Value, 0, len(names))
	for _, name := range names {
		rows = append(rows, []driver.Value{name})
	}
	return metadataSQLQueryStep{columns: []string{"column_name"}, rows: rows}
}

func appendRecordSystemColumns(names []string) []string {
	existing := make(map[string]bool, len(names))
	for _, name := range names {
		existing[name] = true
	}
	for _, name := range []string{"deleted", "ext_info", "create_user_id", "update_user_id"} {
		if !existing[name] {
			names = append(names, name)
		}
	}
	return names
}

func TestEnsureObjectStorageConditionalUniqueRemainingBranches(t *testing.T) {
	runtimeStore := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = runtimeStore.Close() })
	base := NewMetadataStore(runtimeStore)
	run := func(t *testing.T, dialect string, state metadataSQLState, object definitionmodel.ObjectSchema) error {
		t.Helper()
		if err := runtimeStore.SetDialectForTesting(dialect); err != nil {
			t.Fatal(err)
		}
		repository := scriptedMetadataStore(t, &state, base)
		repository.storage = metadataTestStorageProfile(dialect)
		repository.createIndex = func(context.Context, string, string, bool, ...string) error {
			return nil
		}
		return repository.ensureObjectStorage(t.Context(), object)
	}

	sqliteQueries := func(indexes ...string) []metadataSQLQueryStep {
		return []metadataSQLQueryStep{
			metadataSQLiteColumnStep(appendRecordSystemColumns([]string{"workspace_id", "id", "member_id", "status"})...),
			metadataIndexStep(indexes...),
		}
	}
	invalid := metadataConditionalUniqueObject("active")
	invalid.Validations[0].Fields = []string{"missing"}
	if err := run(t, "sqlite", metadataSQLState{
		execSteps:  []metadataSQLExecStep{{rows: 1}},
		querySteps: sqliteQueries(),
	}, invalid); err == nil || !strings.Contains(err.Error(), "invalid field") {
		t.Fatalf("expected invalid conditional unique policy, got %v", err)
	}

	object := metadataConditionalUniqueObject("active")
	policies, err := recordvalidation.RecordConditionalUniquePolicies(object)
	if err != nil {
		t.Fatal(err)
	}
	desiredIndex := base.conditionalUniqueIndexName(object.Key, policies[0])
	if err := run(t, "sqlite", metadataSQLState{
		execSteps:  []metadataSQLExecStep{{rows: 1}},
		querySteps: sqliteQueries(desiredIndex),
	}, object); err != nil {
		t.Fatalf("existing desired conditional index: %v", err)
	}

	staleIndex := "uidx_conditional_stale"
	if err := run(t, "sqlite", metadataSQLState{
		execSteps:  []metadataSQLExecStep{{rows: 1}, {err: errMetadataSQL}},
		querySteps: sqliteQueries(staleIndex),
	}, definitionmodel.ObjectSchema{Key: "booking"}); err == nil || !strings.Contains(err.Error(), "drop index") {
		t.Fatalf("expected stale index drop failure, got %v", err)
	}

	mysqlBaseQueries := func(extra ...metadataSQLQueryStep) []metadataSQLQueryStep {
		steps := []metadataSQLQueryStep{
			metadataInformationSchemaColumnStep("workspace_id", "id"),
			metadataIndexStep(staleIndex),
		}
		return append(steps, extra...)
	}
	if err := run(t, "mysql", metadataSQLState{
		execSteps:  []metadataSQLExecStep{{rows: 1}, {rows: 1}},
		querySteps: mysqlBaseQueries(metadataSQLQueryStep{err: errMetadataSQL}),
	}, definitionmodel.ObjectSchema{Key: "booking"}); err == nil {
		t.Fatal("expected stale guard column introspection failure")
	}
	if err := run(t, "mysql", metadataSQLState{
		execSteps:  []metadataSQLExecStep{{rows: 1}, {rows: 1}},
		querySteps: mysqlBaseQueries(metadataInformationSchemaColumnStep("workspace_id", "id")),
	}, definitionmodel.ObjectSchema{Key: "booking"}); err != nil {
		t.Fatalf("stale index without guard column: %v", err)
	}
	guard := metadataTestStorageProfile("mysql").ConditionalUniqueGuard(staleIndex)
	if err := run(t, "mysql", metadataSQLState{
		execSteps:  []metadataSQLExecStep{{rows: 1}, {rows: 1}, {err: errMetadataSQL}},
		querySteps: mysqlBaseQueries(metadataInformationSchemaColumnStep("workspace_id", "id", guard)),
	}, definitionmodel.ObjectSchema{Key: "booking"}); err == nil || !strings.Contains(err.Error(), "drop stale conditional unique guard") {
		t.Fatalf("expected stale guard drop failure, got %v", err)
	}
	if err := run(t, "mysql", metadataSQLState{
		execSteps:  []metadataSQLExecStep{{rows: 1}, {rows: 1}, {rows: 1}},
		querySteps: mysqlBaseQueries(metadataInformationSchemaColumnStep("workspace_id", "id", guard)),
	}, definitionmodel.ObjectSchema{Key: "booking"}); err != nil {
		t.Fatalf("stale guard drop success: %v", err)
	}

	if err := run(t, "sqlite", metadataSQLState{
		execSteps:  []metadataSQLExecStep{{rows: 1}, {err: errMetadataSQL}},
		querySteps: sqliteQueries(),
	}, object); err == nil || !strings.Contains(err.Error(), "create conditional unique index") {
		t.Fatalf("expected conditional index creation failure, got %v", err)
	}
}

func TestCreateConditionalUniqueIndexRemainingProfileStrategies(t *testing.T) {
	runtimeStore := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = runtimeStore.Close() })
	base := NewMetadataStore(runtimeStore)
	policy := recordvalidation.RecordConditionalUniquePolicy{
		Key:             "one_active",
		Fields:          []string{"member_id"},
		ConditionField:  "status",
		ConditionValues: []string{"active"},
	}
	indexName := "uidx_conditional_booking_active"
	guard := metadataTestStorageProfile("mysql").ConditionalUniqueGuard(indexName)

	runMySQL := func(t *testing.T, state metadataSQLState, createIndexErr error) error {
		t.Helper()
		if err := runtimeStore.SetDialectForTesting("mysql"); err != nil {
			t.Fatal(err)
		}
		repository := scriptedMetadataStore(t, &state, base)
		repository.storage = metadataTestStorageProfile("mysql")
		repository.createIndex = func(context.Context, string, string, bool, ...string) error {
			return createIndexErr
		}
		return repository.createConditionalUniqueIndex(t.Context(), "booking", indexName, policy)
	}
	if err := runMySQL(t, metadataSQLState{
		querySteps: []metadataSQLQueryStep{{err: errMetadataSQL}},
	}, nil); err == nil {
		t.Fatal("expected mysql column introspection failure")
	}
	if err := runMySQL(t, metadataSQLState{
		querySteps: []metadataSQLQueryStep{metadataInformationSchemaColumnStep("workspace_id", "id")},
		execSteps:  []metadataSQLExecStep{{err: errMetadataSQL}},
	}, nil); err == nil || !strings.Contains(err.Error(), "add conditional unique guard") {
		t.Fatalf("expected mysql guard creation failure, got %v", err)
	}
	if err := runMySQL(t, metadataSQLState{
		querySteps: []metadataSQLQueryStep{metadataInformationSchemaColumnStep("workspace_id", "id")},
		execSteps:  []metadataSQLExecStep{{rows: 1}},
	}, nil); err != nil {
		t.Fatalf("mysql guard creation success: %v", err)
	}
	if err := runMySQL(t, metadataSQLState{
		querySteps: []metadataSQLQueryStep{metadataInformationSchemaColumnStep("workspace_id", "id", guard)},
	}, errMetadataSQL); err == nil {
		t.Fatal("expected mysql conditional unique index failure")
	}
	if err := runMySQL(t, metadataSQLState{
		querySteps: []metadataSQLQueryStep{metadataInformationSchemaColumnStep("workspace_id", "id", guard)},
	}, nil); err != nil {
		t.Fatalf("existing mysql guard should be reused: %v", err)
	}

	if err := runtimeStore.SetDialectForTesting("postgres"); err != nil {
		t.Fatal(err)
	}
	for _, step := range []metadataSQLExecStep{{rows: 1}, {err: errMetadataSQL}} {
		repository := scriptedMetadataStore(t, &metadataSQLState{execSteps: []metadataSQLExecStep{step}}, base)
		err := repository.createConditionalUniqueIndex(t.Context(), "booking", indexName, policy)
		if (err != nil) != (step.err != nil) {
			t.Fatalf("postgres partial unique index err=%v", err)
		}
	}
}
