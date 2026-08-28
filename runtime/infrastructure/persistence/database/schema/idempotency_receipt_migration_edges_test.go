package schema

import (
	"database/sql/driver"
	"testing"
)

func migrationSpecFixture() idempotencyReceiptMigrationSpec {
	return idempotencyReceiptMigrationSpec{table: "receipts", scopeColumns: []string{"object_key"}, backfillColumns: []string{"object_key"}}
}

func scriptedMigrationStore(t *testing.T, state *schemaSQLState) scriptedSchemaStore {
	t.Helper()
	db := openSchemaScriptedDB(state)
	t.Cleanup(func() { _ = db.Close() })
	return scriptedSchemaStore{db: db}
}

func TestBackfillIdempotencyReceiptRowsFailureAndLegacyEdges(t *testing.T) {
	spec := migrationSpecFixture()
	for name, state := range map[string]schemaSQLState{
		"query":    {querySteps: []schemaSQLQueryStep{{err: errSchemaSQL}}},
		"scan":     {querySteps: []schemaSQLQueryStep{{columns: []string{"short"}, rows: [][]driver.Value{{"id"}}}}},
		"terminal": {querySteps: []schemaSQLQueryStep{{columns: []string{"id", "workspace_id", "idempotency_key", "request_fingerprint", "status", "object_key"}, nextErr: errSchemaSQL}}},
		"empty-id": {querySteps: []schemaSQLQueryStep{{columns: []string{"id", "workspace_id", "idempotency_key", "request_fingerprint", "status", "object_key"}, rows: [][]driver.Value{{"", "", "", "", "", ""}}}}},
		"update":   {querySteps: []schemaSQLQueryStep{{columns: []string{"id", "workspace_id", "idempotency_key", "request_fingerprint", "status", "object_key"}, rows: [][]driver.Value{{"receipt", "", "", "", "", ""}}}}, execSteps: []schemaSQLExecStep{{err: errSchemaSQL}}},
	} {
		t.Run(name, func(t *testing.T) {
			store := scriptedMigrationStore(t, &state)
			if err := backfillIdempotencyReceiptRows(t.Context(), store, spec); err == nil {
				t.Fatal("backfill failure swallowed")
			}
		})
	}
	state := schemaSQLState{querySteps: []schemaSQLQueryStep{{columns: []string{"id", "workspace_id", "idempotency_key", "request_fingerprint", "status", "object_key"}, rows: [][]driver.Value{{"receipt", "", "", "", "", ""}}}}}
	if err := backfillIdempotencyReceiptRows(t.Context(), scriptedMigrationStore(t, &state), spec); err != nil {
		t.Fatalf("legacy backfill=%v", err)
	}
}

func TestFindIdempotencyMigrationDuplicatesFailureAndSuccessEdges(t *testing.T) {
	spec := migrationSpecFixture()
	groupColumns := []string{"workspace_id", "object_key", "idempotency_key", "count"}
	scopeRow := []driver.Value{"workspace", "customer", "key", int64(2)}
	for name, state := range map[string]schemaSQLState{
		"query":        {querySteps: []schemaSQLQueryStep{{err: errSchemaSQL}}},
		"scan":         {querySteps: []schemaSQLQueryStep{{columns: []string{"short"}, rows: [][]driver.Value{{"workspace"}}}}},
		"terminal":     {querySteps: []schemaSQLQueryStep{{columns: groupColumns, nextErr: errSchemaSQL}}},
		"ids-query":    {querySteps: []schemaSQLQueryStep{{columns: groupColumns, rows: [][]driver.Value{scopeRow}}, {err: errSchemaSQL}}},
		"ids-scan":     {querySteps: []schemaSQLQueryStep{{columns: groupColumns, rows: [][]driver.Value{scopeRow}}, {columns: []string{"id", "extra"}, rows: [][]driver.Value{{"a", "extra"}}}}},
		"ids-terminal": {querySteps: []schemaSQLQueryStep{{columns: groupColumns, rows: [][]driver.Value{scopeRow}}, {columns: []string{"id"}, nextErr: errSchemaSQL}}},
	} {
		t.Run(name, func(t *testing.T) {
			store := scriptedMigrationStore(t, &state)
			if _, err := findIdempotencyMigrationDuplicates(t.Context(), store, spec); err == nil {
				t.Fatal("duplicate failure swallowed")
			}
		})
	}
	state := schemaSQLState{querySteps: []schemaSQLQueryStep{{columns: groupColumns, rows: [][]driver.Value{scopeRow}}, {columns: []string{"id"}, rows: [][]driver.Value{{"a"}, {"b"}}}}}
	duplicates, err := findIdempotencyMigrationDuplicates(t.Context(), scriptedMigrationStore(t, &state), spec)
	if err != nil || len(duplicates) != 1 || len(duplicates[0].receiptIDs) != 2 {
		t.Fatalf("duplicates=%+v err=%v", duplicates, err)
	}
}

func TestCreateIndexRejectsCorruptIndexRows(t *testing.T) {
	state := schemaSQLState{querySteps: []schemaSQLQueryStep{{columns: []string{"name", "extra"}, rows: [][]driver.Value{{"idx", "extra"}}}}}
	store := scriptedMigrationStore(t, &state)
	if err := CreateIndexIfMissing(t.Context(), store, "records", "idx_records", false, "id"); err == nil {
		t.Fatal("corrupt index row accepted")
	}
}
