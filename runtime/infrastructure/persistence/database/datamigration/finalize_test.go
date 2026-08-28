package datamigration

import (
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
)

func TestFinalizeCreatesDeferredIndexesAndForeignKeys(t *testing.T) {
	table := finalizeTablePlan()
	copier := finalizeTestCopier(t, table,
		[]inventoryScriptResult{inventoryRows([]string{"exists"}, []driver.Value{false})},
		[]error{nil, nil, nil},
	)

	report, err := copier.Finalize(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.IndexesCreated) != 1 || report.IndexesCreated[0] != "orders.idx_orders_reference" {
		t.Fatalf("indexes=%v", report.IndexesCreated)
	}
	if len(report.ForeignKeysCreated) != 1 || report.ForeignKeysCreated[0] != "orders.migrated_fk_orders_1" {
		t.Fatalf("foreign keys=%v", report.ForeignKeysCreated)
	}
}

func TestFinalizeCreatesNonUniqueDeferredIndex(t *testing.T) {
	table := finalizeTablePlan()
	table.DeferredIndexes[0].Unique = false
	table.DeferredForeignKeys = nil
	copier := finalizeTestCopier(t, table, nil, []error{nil})
	report, err := copier.Finalize(t.Context())
	if err != nil || len(report.IndexesCreated) != 1 {
		t.Fatalf("report=%#v err=%v", report, err)
	}
}

func TestFinalizeSkipsExistingForeignKey(t *testing.T) {
	table := finalizeTablePlan()
	table.DeferredIndexes = nil
	copier := finalizeTestCopier(t, table,
		[]inventoryScriptResult{inventoryRows([]string{"exists"}, []driver.Value{true})},
		nil,
	)
	report, err := copier.Finalize(t.Context())
	if err != nil || len(report.ForeignKeysCreated) != 0 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

func TestFinalizeRequiresSuccessfulCurrentVerification(t *testing.T) {
	invalidPlan := Copier{Plan: Plan{Tables: []TablePlan{{Name: "orders"}}}}
	if _, err := invalidPlan.Finalize(t.Context()); err == nil || !strings.Contains(err.Error(), "verification ordering key") {
		t.Fatalf("verification error=%v", err)
	}

	table := finalizeTablePlan()
	table.DeferredIndexes, table.DeferredForeignKeys = nil, nil
	copier := finalizeTestCopierRows(t, table, "source-row", "target-row", nil, nil)
	if _, err := copier.Finalize(t.Context()); err == nil || !strings.Contains(err.Error(), "cannot finalize") {
		t.Fatalf("mismatch error=%v", err)
	}
}

func TestFinalizeRejectsUnsafeDeferredIdentifiers(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*TablePlan)
		message string
	}{
		{name: "table", mutate: func(table *TablePlan) { table.Name = "orders;drop" }, message: "unsafe deferred table identifier"},
		{name: "index", mutate: func(table *TablePlan) { table.DeferredIndexes[0].Name = "index;drop" }, message: "unsafe deferred index identifier"},
		{name: "index column", mutate: func(table *TablePlan) { table.DeferredIndexes[0].Columns = []string{"reference;drop"} }, message: "unsafe deferred index column identifier"},
		{name: "referenced table", mutate: func(table *TablePlan) {
			table.DeferredIndexes = nil
			table.DeferredForeignKeys[0].ReferencedTable = "accounts;drop"
		}, message: "unsafe deferred referenced table identifier"},
		{name: "foreign column", mutate: func(table *TablePlan) {
			table.DeferredIndexes = nil
			table.DeferredForeignKeys[0].Columns = []string{"account;drop"}
		}, message: "unsafe deferred foreign-key column identifier"},
		{name: "referenced column", mutate: func(table *TablePlan) {
			table.DeferredIndexes = nil
			table.DeferredForeignKeys[0].ReferencedColumns = []string{"id;drop"}
		}, message: "unsafe deferred foreign-key column identifier"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			table := finalizeTablePlan()
			test.mutate(&table)
			copier := finalizeTestCopier(t, table, nil, nil)
			if _, err := copier.Finalize(t.Context()); err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestFinalizePropagatesDeferredDDLFailures(t *testing.T) {
	failure := errors.New("injected ddl failure")
	tests := []struct {
		name          string
		configure     func(*TablePlan)
		targetQueries []inventoryScriptResult
		execErrors    []error
		message       string
	}{
		{name: "create index", configure: func(table *TablePlan) { table.DeferredForeignKeys = nil }, execErrors: []error{failure}, message: "create deferred index"},
		{name: "inspect foreign key", configure: func(table *TablePlan) { table.DeferredIndexes = nil }, targetQueries: []inventoryScriptResult{{err: failure}}, message: "injected ddl failure"},
		{name: "create foreign key", configure: func(table *TablePlan) { table.DeferredIndexes = nil }, targetQueries: []inventoryScriptResult{inventoryRows([]string{"exists"}, []driver.Value{false})}, execErrors: []error{failure}, message: "create deferred foreign key"},
		{name: "validate foreign key", configure: func(table *TablePlan) { table.DeferredIndexes = nil }, targetQueries: []inventoryScriptResult{inventoryRows([]string{"exists"}, []driver.Value{false})}, execErrors: []error{nil, failure}, message: "validate deferred foreign key"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			table := finalizeTablePlan()
			test.configure(&table)
			copier := finalizeTestCopier(t, table, test.targetQueries, test.execErrors)
			if _, err := copier.Finalize(t.Context()); err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestMigratedForeignKeyNameIsStableAndPostgresSafe(t *testing.T) {
	if got := migratedForeignKeyName("orders", 0); got != "migrated_fk_orders_1" {
		t.Fatalf("short name=%q", got)
	}
	long := migratedForeignKeyName(strings.Repeat("orders", 15), 12)
	if len(long) > 63 || !strings.HasPrefix(long, "migrated_fk_") || long != migratedForeignKeyName(strings.Repeat("orders", 15), 12) {
		t.Fatalf("long name=%q length=%d", long, len(long))
	}
	if long == migratedForeignKeyName(strings.Repeat("orders", 15), 13) {
		t.Fatalf("position was not represented in hashed name: %q", long)
	}
}

func finalizeTablePlan() TablePlan {
	return TablePlan{
		Name:          "orders",
		CheckpointKey: []string{"id"},
		Conversions:   []ConversionPlan{{Column: "id", Strategy: "identity"}},
		DeferredIndexes: []IndexInventory{{
			Name: "idx_orders_reference", Unique: true, Columns: []string{"reference"},
		}},
		DeferredForeignKeys: []ForeignInventory{{
			Columns: []string{"account_id"}, ReferencedTable: "accounts", ReferencedColumns: []string{"id"},
		}},
	}
}

func finalizeTestCopier(t *testing.T, table TablePlan, targetQueries []inventoryScriptResult, execErrors []error) Copier {
	t.Helper()
	return finalizeTestCopierRows(t, table, "row-1", "row-1", targetQueries, execErrors)
}

func finalizeTestCopierRows(t *testing.T, table TablePlan, sourceID, targetID string, targetQueries []inventoryScriptResult, execErrors []error) Copier {
	t.Helper()
	source := openInventoryScriptDatabase(t, strings.ReplaceAll(t.Name(), "/", "_")+"_source",
		inventoryRows([]string{"id"}, []driver.Value{sourceID}),
	)
	queries := append([]inventoryScriptResult{inventoryRows([]string{"id"}, []driver.Value{targetID})}, targetQueries...)
	target := openInventoryScriptDatabaseWithExec(t, strings.ReplaceAll(t.Name(), "/", "_")+"_target", queries, execErrors)
	return Copier{
		Source: source, Target: target, SourceEngine: EnginePostgres, TargetSchema: "runtime",
		Plan: Plan{
			Source: Inventory{Engine: EnginePostgres, Schema: "source", Tables: []TableInventory{{Name: table.Name, Columns: []ColumnInventory{{Name: "id", Type: "text"}}}}},
			Tables: []TablePlan{table},
		},
	}
}
