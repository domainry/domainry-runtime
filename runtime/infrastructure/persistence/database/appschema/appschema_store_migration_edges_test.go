package appschema

import (
	"context"
	"errors"
	"testing"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestMetadataMigrationPlanTracksCreateAddAndStableStorage(t *testing.T) {
	store := openStoreForMetadataTest(t)
	defer store.raw.Close()
	scope := metadataTestInstallationScope()
	manifest := manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{
		{Key: "", Fields: []definitionmodel.FieldSchema{{Key: "ignored"}}},
		{Key: "asset", Fields: []definitionmodel.FieldSchema{
			{Key: "name", Type: "text", Default: "Unnamed"},
			{Key: "quantity", Type: "integer", Config: map[string]any{"indexed": true}, DefaultValue: 3},
			{Key: "disabled", Type: "text", DisabledAt: "2026-07-20T00:00:00Z"},
		}, Validations: []definitionmodel.ValidationSchema{{Type: "composite_unique", Fields: []string{"name", "quantity"}}}},
	}}
	plan, err := store.MigrationPlan(t.Context(), scope, manifest)
	if err != nil || len(plan) != 1 || plan[0].Operation != "create_table" || plan[0].ObjectKey != "asset" {
		t.Fatalf("initial plan=%v err=%v", plan, err)
	}
	if err := store.SyncManifest(t.Context(), scope, manifest); err != nil {
		t.Fatal(err)
	}
	columns, err := store.tableColumns(t.Context(), "asset")
	if err != nil || !columns["workspace_id"] || !columns["id"] || !columns["name"] || !columns["quantity"] || columns["disabled"] {
		t.Fatalf("columns=%v err=%v", columns, err)
	}
	indexes, err := store.tableIndexes(t.Context(), "asset")
	if err != nil || len(indexes) < 3 {
		t.Fatalf("indexes=%v err=%v", indexes, err)
	}
	if _, err := store.raw.DB().ExecContext(t.Context(), `INSERT INTO asset (workspace_id, id, created_at, updated_at, name, quantity) VALUES (?, ?, ?, ?, ?, ?)`, "workspace-a", "asset-1", "now", "now", "same", 3); err != nil {
		t.Fatal(err)
	}
	if _, err := store.raw.DB().ExecContext(t.Context(), `INSERT INTO asset (workspace_id, id, created_at, updated_at, name, quantity) VALUES (?, ?, ?, ?, ?, ?)`, "workspace-a", "asset-2", "now", "now", "same", 3); err == nil {
		t.Fatal("composite_unique index accepted a duplicate tuple")
	}
	if _, err := store.raw.DB().ExecContext(t.Context(), `INSERT INTO asset (workspace_id, id, created_at, updated_at, name, quantity) VALUES (?, ?, ?, ?, ?, ?)`, "workspace-b", "asset-2", "now", "now", "same", 3); err != nil {
		t.Fatalf("workspace-scoped composite_unique rejected independent tenant: %v", err)
	}
	plan, err = store.MigrationPlan(t.Context(), scope, manifest)
	if err != nil || len(plan) != 0 {
		t.Fatalf("stable plan=%v err=%v", plan, err)
	}

	extended := manifest
	extended.Objects[1].Fields = append(extended.Objects[1].Fields,
		definitionmodel.FieldSchema{Key: "price", Type: "number"},
		definitionmodel.FieldSchema{Key: "active", Type: "boolean"},
	)
	plan, err = store.MigrationPlan(t.Context(), scope, extended)
	if err != nil || len(plan) != 2 {
		t.Fatalf("extended plan=%v err=%v", plan, err)
	}
	if plan[0].Operation != "add_column" || plan[0].ColumnKey != "price" || plan[1].ColumnKey != "active" {
		t.Fatalf("add-column plan=%v", plan)
	}
}

func TestMetadataMigrationPlanScopeCancellationAndValueProjection(t *testing.T) {
	store := openStoreForMetadataTest(t)
	defer store.raw.Close()
	if _, err := store.MigrationPlan(t.Context(), principalmodel.SystemScope{}, manifestmodel.ManifestSchema{}); err == nil {
		t.Fatal("zero installation scope accepted")
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := store.MigrationPlan(cancelled, metadataTestInstallationScope(), manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "asset"}}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error=%v", err)
	}
	if got, ok := metadataDBValue(float32(1.5)).(float64); !ok || got != 1.5 {
		t.Fatalf("float32 projection=%T %v", metadataDBValue(float32(1.5)), metadataDBValue(float32(1.5)))
	}
	if got := metadataDBValue(2); got != float64(2) {
		t.Fatalf("int projection=%T %v", got, got)
	}
	if got := metadataDBValue(int64(3)); got != float64(3) {
		t.Fatalf("int64 projection=%T %v", got, got)
	}
	if got := metadataDBValue("value"); got != "value" {
		t.Fatalf("string projection=%T %v", got, got)
	}
}

func TestMetadataPlansAndMigratesFloatingCurrencyPhysicalSchema(t *testing.T) {
	store := openStoreForMetadataTest(t)
	defer store.raw.Close()
	if _, err := store.raw.DB().ExecContext(t.Context(), `CREATE TABLE incompatible_invoice (workspace_id TEXT NOT NULL, id TEXT NOT NULL, amount REAL)`); err != nil {
		t.Fatal(err)
	}
	manifest := manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "incompatible_invoice", Fields: []definitionmodel.FieldSchema{{Key: "amount", Type: "currency", Config: map[string]any{"precision": 12, "scale": 2}}}}}}
	plan, err := store.MigrationPlan(t.Context(), metadataTestInstallationScope(), manifest)
	if err != nil || len(plan) != 1 || plan[0].Operation != "alter_column_exact_decimal" || plan[0].ColumnType != "TEXT" {
		t.Fatalf("exact migration plan=%#v err=%v", plan, err)
	}
	if err := store.SyncManifest(t.Context(), metadataTestInstallationScope(), manifest); err != nil {
		t.Fatalf("sync exact decimal schema: %v", err)
	}
	var physicalType string
	if err := store.raw.DB().QueryRowContext(t.Context(), `SELECT type FROM pragma_table_info('incompatible_invoice') WHERE name = 'amount'`).Scan(&physicalType); err != nil || physicalType != "TEXT" {
		t.Fatalf("exact decimal type=%q err=%v", physicalType, err)
	}
}

func TestMetadataRejectsTextBackedIntegerBeforeAtomicPredicatesRun(t *testing.T) {
	store := openStoreForMetadataTest(t)
	defer store.raw.Close()
	if _, err := store.raw.DB().ExecContext(t.Context(), `CREATE TABLE incompatible_capacity (workspace_id TEXT NOT NULL, id TEXT NOT NULL, remaining_capacity TEXT)`); err != nil {
		t.Fatal(err)
	}
	manifest := manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{
		Key: "incompatible_capacity", Fields: []definitionmodel.FieldSchema{{Key: "remaining_capacity", Type: "integer"}},
	}}}
	plan, err := store.MigrationPlan(t.Context(), metadataTestInstallationScope(), manifest)
	if plan != nil {
		t.Fatalf("incompatible integer schema produced migration plan=%#v", plan)
	}
	var mismatch *appschemamodel.ApplicationSchemaPhysicalSchemaMismatchError
	if !errors.As(err, &mismatch) || mismatch.ExpectedType != "INTEGER" || mismatch.ActualType != "TEXT" {
		t.Fatalf("integer migration mismatch=%#v err=%v", mismatch, err)
	}
	if err := store.SyncManifest(t.Context(), metadataTestInstallationScope(), manifest); !errors.As(err, &mismatch) {
		t.Fatalf("sync did not reject text-backed integer: %v", err)
	}
}

func TestMetadataAcceptsMatchingCurrencyPhysicalSchemaAndReusesTypeSnapshot(t *testing.T) {
	store := openStoreForMetadataTest(t)
	defer store.raw.Close()
	if _, err := store.raw.DB().ExecContext(t.Context(), `CREATE TABLE compatible_invoice (workspace_id TEXT NOT NULL, id TEXT NOT NULL, amount TEXT, fee TEXT)`); err != nil {
		t.Fatal(err)
	}
	object := definitionmodel.ObjectSchema{Key: "compatible_invoice", Fields: []definitionmodel.FieldSchema{{Key: "amount", Type: "currency"}, {Key: "fee", Type: "currency"}}}
	manifest := manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{object}}
	if plan, err := store.MigrationPlan(t.Context(), metadataTestInstallationScope(), manifest); err != nil || len(plan) != 0 {
		t.Fatalf("matching plan=%#v err=%v", plan, err)
	}
	if err := store.SyncManifest(t.Context(), metadataTestInstallationScope(), manifest); err != nil {
		t.Fatalf("matching sync: %v", err)
	}
}

func TestMetadataCompilesTemporalExclusionScopeRangeIndex(t *testing.T) {
	store := openStoreForMetadataTest(t)
	defer store.raw.Close()
	object := definitionmodel.ObjectSchema{Key: "booking_slot", Fields: []definitionmodel.FieldSchema{
		{Key: "owner", Type: "relation"}, {Key: "starts_at", Type: "datetime"}, {Key: "ends_at", Type: "datetime"}, {Key: "status", Type: "select"},
	}, Validations: []definitionmodel.ValidationSchema{{Key: "owner_schedule", Type: "temporal_exclusion", Config: map[string]any{"start_field": "starts_at", "end_field": "ends_at", "scope_fields": []any{"owner"}, "status_field": "status", "excluded_statuses": []any{"cancelled"}}}}}
	if err := store.SyncManifest(t.Context(), metadataTestInstallationScope(), manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{object}}); err != nil {
		t.Fatal(err)
	}
	fields := []string{"workspace_id", "owner", "starts_at", "ends_at"}
	indexName := store.temporalExclusionIndexName(object.Key, "owner_schedule", fields)
	indexes, err := store.tableIndexes(t.Context(), object.Key)
	if err != nil || !indexes[indexName] {
		t.Fatalf("temporal index %q missing from %#v err=%v", indexName, indexes, err)
	}
	indexed := metadataConstraintIndexedFields(object)
	for _, field := range []string{"owner", "starts_at", "ends_at"} {
		if !indexed[field] {
			t.Fatalf("constraint field %q not marked indexed: %#v", field, indexed)
		}
	}
	field := metadataConstraintIndexedField(object.Fields[1], true)
	if got := metadataTestStorageProfile("mysql").FieldColumnType(field, metadataFieldIndexed(field)); got != "VARCHAR(191)" {
		t.Fatalf("mysql temporal range type=%q", got)
	}
}

func TestMetadataCompilesRelatedAggregateLockIndex(t *testing.T) {
	store := openStoreForMetadataTest(t)
	defer store.raw.Close()
	object := definitionmodel.ObjectSchema{Key: "refund_fact", Fields: []definitionmodel.FieldSchema{
		{Key: "payment_id", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "payment_limit"}}, {Key: "amount", Type: "currency"}, {Key: "status", Type: "select"},
	}, Validations: []definitionmodel.ValidationSchema{{Key: "refund_limit", Type: "related_aggregate_invariant", Config: map[string]any{"relation_field": "payment_id", "aggregate": "sum", "value_field": "amount", "limit_field": "paid_amount", "operator": "lte", "status_field": "status", "included_statuses": []any{"approved"}}}}}
	if err := store.SyncManifest(t.Context(), metadataTestInstallationScope(), manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{object}}); err != nil {
		t.Fatal(err)
	}
	fields := []string{"workspace_id", "payment_id", "status"}
	indexName := store.relatedAggregateIndexName(object.Key, "refund_limit", fields)
	indexes, err := store.tableIndexes(t.Context(), object.Key)
	if err != nil || !indexes[indexName] {
		t.Fatalf("aggregate index %q missing from %#v err=%v", indexName, indexes, err)
	}
	indexed := metadataConstraintIndexedFields(object)
	if !indexed["payment_id"] || !indexed["status"] {
		t.Fatalf("aggregate filter fields not marked indexed: %#v", indexed)
	}
}
