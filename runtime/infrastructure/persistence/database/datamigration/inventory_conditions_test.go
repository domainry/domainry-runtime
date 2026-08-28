package datamigration

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"path/filepath"
	"testing"
)

func TestInventoryCompoundConditionOutcomes(t *testing.T) {
	current := int64(5)
	updateMaximumLargeObjectSize(&current, sql.NullInt64{Valid: true, Int64: 10}, errors.New("query"))
	updateMaximumLargeObjectSize(&current, sql.NullInt64{}, nil)
	updateMaximumLargeObjectSize(&current, sql.NullInt64{Valid: true, Int64: 4}, nil)
	if current != 5 {
		t.Fatalf("unexpected maximum=%d", current)
	}
	updateMaximumLargeObjectSize(&current, sql.NullInt64{Valid: true, Int64: 10}, nil)
	if current != 10 {
		t.Fatalf("maximum=%d", current)
	}

	constraints := sqliteConstraintInventory(TableInventory{Indexes: []IndexInventory{{Name: "ordinary", Unique: false}}})
	if len(constraints) != 0 {
		t.Fatalf("ordinary index became constraint: %#v", constraints)
	}

	sequenceDB := openInventoryScriptDatabase(t, "sqlite-sequence-condition",
		inventoryRows([]string{"count"}, []driver.Value{int64(1)}),
		inventoryRows([]string{"name", "seq"}, []driver.Value{"records", int64(3)}),
	)
	inventory := Inventory{Tables: []TableInventory{{Name: "records", Columns: []ColumnInventory{{Name: "ordinary"}, {Name: "text_id", Type: "text", PrimaryKey: 1}, {Name: "id", Type: "integer", PrimaryKey: 2}}}}}
	if err := inspectSQLiteSequences(t.Context(), sequenceDB, &inventory); err != nil || len(inventory.Sequences) != 1 || inventory.Sequences[0].OwnedColumn != "id" {
		t.Fatalf("sequences=%#v err=%v", inventory.Sequences, err)
	}

	sqliteDB := openCopySQLite(t, filepath.Join(t.TempDir(), "composite.db"))
	if _, err := sqliteDB.ExecContext(t.Context(), `PRAGMA foreign_keys=ON; CREATE TABLE parents (a TEXT, b TEXT, PRIMARY KEY (a,b)); CREATE TABLE children (id TEXT PRIMARY KEY, a TEXT, b TEXT, FOREIGN KEY (a,b) REFERENCES parents(a,b))`); err != nil {
		t.Fatal(err)
	}
	table := TableInventory{Name: "children"}
	if err := inspectSQLiteForeignKeys(t.Context(), sqliteDB, &table); err != nil || len(table.ForeignKeys) != 1 || len(table.ForeignKeys[0].Columns) != 2 {
		t.Fatalf("SQLite foreign keys=%#v err=%v", table.ForeignKeys, err)
	}
}

func TestPostgresInventoryWithoutWorkspaceAndCompositeForeign(t *testing.T) {
	db := openInventoryScriptDatabase(t, "postgres-no-workspace",
		inventoryRows([]string{"table_name"}, []driver.Value{"records"}),
		inventoryRows([]string{"count"}, []driver.Value{int64(1)}),
		inventoryRows([]string{"size"}, []driver.Value{int64(100)}),
		inventoryRows([]string{"column_name", "data_type", "is_nullable", "column_default"}, []driver.Value{"id", "text", "NO", ""}),
		inventoryRows([]string{"attname"}, []driver.Value{"id"}),
		inventoryRows([]string{"index_name", "unique", "columns"}),
		inventoryRows([]string{"constraint_name", "column_name", "foreign_table_name", "foreign_column_name"},
			[]driver.Value{"records_parent_fk", "a", "parents", "a"},
			[]driver.Value{"records_parent_fk", "b", "parents", "b"},
		),
		inventoryRows([]string{"constraint_name", "constraint_type"}),
		inventoryRows([]string{"sequence_name", "table_name", "column_name"}),
		inventoryRows([]string{"table_name", "view_definition"}),
		inventoryRows([]string{"trigger_name", "table_name", "timing", "event", "definition"}),
	)
	inventory, err := Inspect(t.Context(), db, EnginePostgres, "runtime")
	if err != nil || len(inventory.Tables) != 1 || inventory.Tables[0].WorkspaceScoped || len(inventory.Tables[0].ForeignKeys) != 1 || len(inventory.Tables[0].ForeignKeys[0].Columns) != 2 {
		t.Fatalf("Postgres inventory=%#v err=%v", inventory, err)
	}
}

func TestMySQLInventoryBinaryWithoutWorkspaceAndCompositeForeign(t *testing.T) {
	db := openInventoryScriptDatabase(t, "mysql-binary-no-workspace",
		inventoryRows([]string{"table_name", "estimated_bytes"}, []driver.Value{"records", int64(100)}),
		inventoryRows([]string{"count"}, []driver.Value{int64(1)}),
		inventoryRows([]string{"column_name", "column_type", "is_nullable", "column_default", "column_key"},
			[]driver.Value{"id", "varchar(64)", "NO", "", "PRI"},
			[]driver.Value{"payload", "varbinary(64)", "YES", "", ""},
		),
		inventoryRows([]string{"index_name", "non_unique", "column_name"}),
		inventoryRows([]string{"constraint_name", "column_name", "referenced_table_name", "referenced_column_name"},
			[]driver.Value{"records_parent_fk", "a", "parents", "a"},
			[]driver.Value{"records_parent_fk", "b", "parents", "b"},
		),
		inventoryRows([]string{"constraint_name", "constraint_type"}),
		inventoryRows([]string{"maximum"}, []driver.Value{int64(64)}),
		inventoryRows([]string{"table_name", "view_definition"}),
		inventoryRows([]string{"trigger_name", "table_name", "timing", "event", "definition"}),
	)
	inventory, err := Inspect(t.Context(), db, EngineMySQL, "runtime")
	if err != nil || len(inventory.Tables) != 1 || inventory.Tables[0].WorkspaceScoped || inventory.Tables[0].MaximumLargeObjectSize != 64 || len(inventory.Tables[0].ForeignKeys) != 1 || len(inventory.Tables[0].ForeignKeys[0].Columns) != 2 {
		t.Fatalf("MySQL inventory=%#v err=%v", inventory, err)
	}
}
