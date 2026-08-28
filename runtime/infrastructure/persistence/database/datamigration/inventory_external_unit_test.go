package datamigration

import (
	"database/sql/driver"
	"strings"
	"testing"
)

func TestPostgresInventoryCatalogContract(t *testing.T) {
	db := openInventoryScriptDatabase(t, "postgres-success",
		inventoryRows([]string{"table_name"}, []driver.Value{"records"}),
		inventoryRows([]string{"count"}, []driver.Value{int64(2)}),
		inventoryRows([]string{"size"}, []driver.Value{int64(4096)}),
		inventoryRows([]string{"column_name", "data_type", "is_nullable", "column_default"},
			[]driver.Value{"id", "text", "NO", ""},
			[]driver.Value{"workspace_id", "text", "NO", ""},
			[]driver.Value{"payload", "bytea", "YES", ""},
		),
		inventoryRows([]string{"attname"}, []driver.Value{"id"}),
		inventoryRows([]string{"index_name", "unique", "columns"}, []driver.Value{"records_pkey", true, `["id"]`}, []driver.Value{"records_workspace", false, `["workspace_id","id"]`}),
		inventoryRows([]string{"constraint_name", "column_name", "foreign_table_name", "foreign_column_name"}, []driver.Value{"records_parent_fk", "id", "parents", "id"}),
		inventoryRows([]string{"constraint_name", "constraint_type"}, []driver.Value{"records_pkey", "PRIMARY KEY"}, []driver.Value{"records_parent_fk", "FOREIGN KEY"}),
		inventoryRows([]string{"maximum"}, []driver.Value{int64(128)}),
		inventoryRows([]string{"invalid"}, []driver.Value{int64(1)}),
		inventoryRows([]string{"sequence_name", "table_name", "column_name"}, []driver.Value{"records_id_seq", "records", "id"}),
		inventoryRows([]string{"last_value"}, []driver.Value{int64(9)}),
		inventoryRows([]string{"table_name", "view_definition"}, []driver.Value{"active_records", "SELECT * FROM records"}),
		inventoryRows([]string{"trigger_name", "table_name", "timing", "event", "definition"}, []driver.Value{"records_touch", "records", "BEFORE", "UPDATE", "EXECUTE touch()"}),
	)
	inventory, err := Inspect(t.Context(), db, EnginePostgres, " runtime ")
	if err != nil {
		t.Fatal(err)
	}
	if inventory.Engine != EnginePostgres || inventory.Schema != "runtime" || inventory.DatabaseBytes != 4096 || len(inventory.Tables) != 1 || len(inventory.Sequences) != 1 || len(inventory.Views) != 1 || len(inventory.Triggers) != 1 {
		t.Fatalf("PostgreSQL inventory = %#v", inventory)
	}
	table := inventory.Tables[0]
	if table.Rows != 2 || table.EstimatedBytes != 4096 || !table.WorkspaceScoped || table.InvalidWorkspaceRows != 1 || table.MaximumLargeObjectSize != 128 || len(table.Columns) != 3 || table.Columns[0].PrimaryKey != 1 || len(table.Indexes) != 2 || len(table.ForeignKeys) != 1 || len(table.Constraints) != 2 {
		t.Fatalf("PostgreSQL table inventory = %#v", table)
	}
	if inventory.Sequences[0].CurrentValue != 9 || inventory.Sequences[0].OwnedColumn != "id" {
		t.Fatalf("PostgreSQL sequence inventory = %#v", inventory.Sequences)
	}
}

func TestPostgresInventoryDefaultsSchemaAndRejectsUnsafeNames(t *testing.T) {
	db := openInventoryScriptDatabase(t, "postgres-default",
		inventoryRows([]string{"table_name"}),
		inventoryRows([]string{"sequence_name", "table_name", "column_name"}),
		inventoryRows([]string{"table_name", "view_definition"}),
		inventoryRows([]string{"trigger_name", "table_name", "timing", "event", "definition"}),
	)
	inventory, err := Inspect(t.Context(), db, EnginePostgres, "")
	if err != nil {
		t.Fatal(err)
	}
	if inventory.Schema != "public" {
		t.Fatalf("default PostgreSQL schema = %q", inventory.Schema)
	}
	if _, err := Inspect(t.Context(), db, EnginePostgres, "unsafe-name"); err == nil || !strings.Contains(err.Error(), "unsafe PostgreSQL schema") {
		t.Fatalf("unsafe PostgreSQL schema error = %v", err)
	}

	unsafeDB := openInventoryScriptDatabase(t, "postgres-unsafe-table", inventoryRows([]string{"table_name"}, []driver.Value{"unsafe-name"}))
	if _, err := Inspect(t.Context(), unsafeDB, EnginePostgres, "public"); err == nil || !strings.Contains(err.Error(), "unsafe PostgreSQL table") {
		t.Fatalf("unsafe PostgreSQL table error = %v", err)
	}
}

func TestMySQLInventoryCatalogContract(t *testing.T) {
	db := openInventoryScriptDatabase(t, "mysql-success",
		inventoryRows([]string{"table_name", "estimated_bytes"}, []driver.Value{"records", int64(3072)}),
		inventoryRows([]string{"count"}, []driver.Value{int64(3)}),
		inventoryRows([]string{"column_name", "column_type", "is_nullable", "column_default", "column_key"},
			[]driver.Value{"id", "varchar(64)", "NO", "", "PRI"},
			[]driver.Value{"workspace_id", "varchar(64)", "NO", "", ""},
			[]driver.Value{"payload", "longblob", "YES", "", ""},
		),
		inventoryRows([]string{"index_name", "non_unique", "column_name"}, []driver.Value{"PRIMARY", int64(0), "id"}, []driver.Value{"records_workspace", int64(1), "workspace_id"}, []driver.Value{"records_workspace", int64(1), "id"}),
		inventoryRows([]string{"constraint_name", "column_name", "referenced_table_name", "referenced_column_name"}, []driver.Value{"records_parent_fk", "id", "parents", "id"}),
		inventoryRows([]string{"constraint_name", "constraint_type"}, []driver.Value{"PRIMARY", "PRIMARY KEY"}, []driver.Value{"records_parent_fk", "FOREIGN KEY"}),
		inventoryRows([]string{"invalid"}, []driver.Value{int64(2)}),
		inventoryRows([]string{"maximum"}, []driver.Value{int64(256)}),
		inventoryRows([]string{"table_name", "view_definition"}, []driver.Value{"active_records", "select * from records"}),
		inventoryRows([]string{"trigger_name", "table_name", "timing", "event", "definition"}, []driver.Value{"records_touch", "records", "BEFORE", "UPDATE", "SET NEW.id = NEW.id"}),
	)
	inventory, err := Inspect(t.Context(), db, EngineMySQL, " runtime ")
	if err != nil {
		t.Fatal(err)
	}
	if inventory.Engine != EngineMySQL || inventory.Schema != "runtime" || inventory.DatabaseBytes != 3072 || len(inventory.Tables) != 1 || len(inventory.Views) != 1 || len(inventory.Triggers) != 1 {
		t.Fatalf("MySQL inventory = %#v", inventory)
	}
	table := inventory.Tables[0]
	if table.Rows != 3 || table.EstimatedBytes != 3072 || !table.WorkspaceScoped || table.InvalidWorkspaceRows != 2 || table.MaximumLargeObjectSize != 256 || len(table.PrimaryKey) != 1 || len(table.Indexes) != 2 || len(table.ForeignKeys) != 1 || len(table.Constraints) != 2 {
		t.Fatalf("MySQL table inventory = %#v", table)
	}
}

func TestMySQLInventoryResolvesDefaultSchemaAndRejectsUnsafeNames(t *testing.T) {
	db := openInventoryScriptDatabase(t, "mysql-default",
		inventoryRows([]string{"database"}, []driver.Value{"runtime"}),
		inventoryRows([]string{"table_name", "estimated_bytes"}),
		inventoryRows([]string{"table_name", "view_definition"}),
		inventoryRows([]string{"trigger_name", "table_name", "timing", "event", "definition"}),
	)
	inventory, err := Inspect(t.Context(), db, EngineMySQL, "")
	if err != nil {
		t.Fatal(err)
	}
	if inventory.Schema != "runtime" {
		t.Fatalf("resolved MySQL schema = %q", inventory.Schema)
	}
	unsafeSchemaDB := openInventoryScriptDatabase(t, "mysql-unsafe-schema", inventoryRows([]string{"database"}, []driver.Value{"unsafe-name"}))
	if _, err := Inspect(t.Context(), unsafeSchemaDB, EngineMySQL, ""); err == nil || !strings.Contains(err.Error(), "unsafe MySQL schema") {
		t.Fatalf("unsafe MySQL schema error = %v", err)
	}
	unsafeTableDB := openInventoryScriptDatabase(t, "mysql-unsafe-table", inventoryRows([]string{"table_name", "estimated_bytes"}, []driver.Value{"unsafe-name", int64(1)}))
	if _, err := Inspect(t.Context(), unsafeTableDB, EngineMySQL, "runtime"); err == nil || !strings.Contains(err.Error(), "unsafe MySQL table") {
		t.Fatalf("unsafe MySQL table error = %v", err)
	}
}
