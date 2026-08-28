package datamigration

import (
	"database/sql/driver"
	"strconv"
	"testing"
)

func TestSQLiteInventoryStageErrors(t *testing.T) {
	pageCount := inventoryRows([]string{"page_count"}, []driver.Value{int64(1)})
	pageSize := inventoryRows([]string{"page_size"}, []driver.Value{int64(4096)})
	emptyTables := inventoryRows([]string{"name"})
	tests := []struct {
		name    string
		results []inventoryScriptResult
	}{
		{name: "table scan", results: []inventoryScriptResult{pageCount, pageSize, inventoryRows([]string{"name", "extra"}, []driver.Value{"records"})}},
		{name: "table stream", results: []inventoryScriptResult{pageCount, pageSize, {columns: []string{"name"}, rowsErr: errInventoryCatalogTest}}},
		{name: "table inspect", results: []inventoryScriptResult{pageCount, pageSize, inventoryRows([]string{"name"}, []driver.Value{"records"}), {err: errInventoryCatalogTest}}},
		{name: "sequences", results: []inventoryScriptResult{pageCount, pageSize, emptyTables, {err: errInventoryCatalogTest}}},
		{name: "views", results: []inventoryScriptResult{pageCount, pageSize, emptyTables, inventoryRows([]string{"exists"}, []driver.Value{int64(0)}), {err: errInventoryCatalogTest}}},
	}
	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openInventoryScriptDatabase(t, "sqlite-stage-"+strconv.Itoa(index), tt.results...)
			if _, err := inspectSQLite(t.Context(), db); err == nil {
				t.Fatal("SQLite inventory stage failure was accepted")
			}
		})
	}
}

func TestSQLiteViewsAndSequencesErrors(t *testing.T) {
	tests := []struct {
		name      string
		results   []inventoryScriptResult
		sequences bool
	}{
		{name: "views query", results: []inventoryScriptResult{{err: errInventoryCatalogTest}}},
		{name: "views scan", results: []inventoryScriptResult{inventoryRows([]string{"kind"}, []driver.Value{"view"})}},
		{name: "views stream", results: []inventoryScriptResult{{columns: []string{"kind", "name", "table", "definition"}, rowsErr: errInventoryCatalogTest}}},
		{name: "sequence count", sequences: true, results: []inventoryScriptResult{{err: errInventoryCatalogTest}}},
		{name: "sequence query", sequences: true, results: []inventoryScriptResult{inventoryRows([]string{"exists"}, []driver.Value{int64(1)}), {err: errInventoryCatalogTest}}},
		{name: "sequence scan", sequences: true, results: []inventoryScriptResult{inventoryRows([]string{"exists"}, []driver.Value{int64(1)}), inventoryRows([]string{"name"}, []driver.Value{"records"})}},
		{name: "sequence stream", sequences: true, results: []inventoryScriptResult{inventoryRows([]string{"exists"}, []driver.Value{int64(1)}), {columns: []string{"name", "seq"}, rowsErr: errInventoryCatalogTest}}},
	}
	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openInventoryScriptDatabase(t, "sqlite-helper-"+strconv.Itoa(index), tt.results...)
			var err error
			if tt.sequences {
				err = inspectSQLiteSequences(t.Context(), db, &Inventory{})
			} else {
				err = inspectSQLiteViewsAndTriggers(t.Context(), db, &Inventory{})
			}
			if err == nil {
				t.Fatal("SQLite helper failure was accepted")
			}
		})
	}

	db := openInventoryScriptDatabase(t, "sqlite-sequence-unowned",
		inventoryRows([]string{"exists"}, []driver.Value{int64(1)}),
		inventoryRows([]string{"name", "seq"}, []driver.Value{"other_records", int64(4)}),
	)
	inventory := Inventory{Tables: []TableInventory{{Name: "records", Columns: []ColumnInventory{{Name: "id", Type: "INTEGER", PrimaryKey: 1}}}}}
	if err := inspectSQLiteSequences(t.Context(), db, &inventory); err != nil || inventory.Sequences[0].OwnedColumn != "" {
		t.Fatalf("unowned SQLite sequence = %#v err %v", inventory.Sequences, err)
	}
}

func sqliteTablePrefix(columns inventoryScriptResult, rest ...inventoryScriptResult) []inventoryScriptResult {
	results := []inventoryScriptResult{inventoryRows([]string{"count"}, []driver.Value{int64(1)}), columns}
	return append(results, rest...)
}

func TestSQLiteTableStageErrors(t *testing.T) {
	emptyColumns := inventoryRows([]string{"ordinal", "name", "type", "not_null", "default", "primary_key"})
	emptyIndexes := inventoryRows([]string{"sequence", "name", "unique", "origin", "partial"})
	tests := []struct {
		name    string
		results []inventoryScriptResult
	}{
		{name: "count", results: []inventoryScriptResult{{err: errInventoryCatalogTest}}},
		{name: "columns query", results: sqliteTablePrefix(inventoryScriptResult{err: errInventoryCatalogTest})},
		{name: "columns scan", results: sqliteTablePrefix(inventoryRows([]string{"name"}, []driver.Value{"id"}))},
		{name: "columns stream", results: sqliteTablePrefix(inventoryScriptResult{columns: emptyColumns.columns, rowsErr: errInventoryCatalogTest})},
		{name: "workspace validation", results: sqliteTablePrefix(
			inventoryRows(emptyColumns.columns, []driver.Value{int64(0), "workspace_id", "TEXT", int64(1), nil, int64(0)}),
			inventoryScriptResult{err: errInventoryCatalogTest},
		)},
		{name: "indexes", results: sqliteTablePrefix(emptyColumns, inventoryScriptResult{err: errInventoryCatalogTest})},
		{name: "foreign keys", results: sqliteTablePrefix(emptyColumns, emptyIndexes, inventoryScriptResult{err: errInventoryCatalogTest})},
	}
	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openInventoryScriptDatabase(t, "sqlite-table-"+strconv.Itoa(index), tt.results...)
			if _, err := inspectSQLiteTable(t.Context(), db, "records"); err == nil {
				t.Fatal("SQLite table stage failure was accepted")
			}
		})
	}
}

func TestSQLiteIndexAndForeignKeyStageErrors(t *testing.T) {
	indexColumns := []string{"sequence", "name", "unique", "origin", "partial"}
	foreignColumns := []string{"id", "sequence", "table", "from", "to", "on_update", "on_delete", "match"}
	tests := []struct {
		name    string
		foreign bool
		results []inventoryScriptResult
	}{
		{name: "index query", results: []inventoryScriptResult{{err: errInventoryCatalogTest}}},
		{name: "index scan", results: []inventoryScriptResult{inventoryRows([]string{"name"}, []driver.Value{"idx"})}},
		{name: "index stream", results: []inventoryScriptResult{{columns: indexColumns, rowsErr: errInventoryCatalogTest}}},
		{name: "index columns query", results: []inventoryScriptResult{inventoryRows(indexColumns, []driver.Value{int64(0), "records_idx", int64(0), "c", int64(0)}), {err: errInventoryCatalogTest}}},
		{name: "index columns scan", results: []inventoryScriptResult{inventoryRows(indexColumns, []driver.Value{int64(0), "records_idx", int64(0), "c", int64(0)}), inventoryRows([]string{"rank"}, []driver.Value{int64(0)})}},
		{name: "index columns stream", results: []inventoryScriptResult{inventoryRows(indexColumns, []driver.Value{int64(0), "records_idx", int64(0), "c", int64(0)}), {columns: []string{"rank", "column_id", "column"}, rowsErr: errInventoryCatalogTest}}},
		{name: "foreign query", foreign: true, results: []inventoryScriptResult{{err: errInventoryCatalogTest}}},
		{name: "foreign scan", foreign: true, results: []inventoryScriptResult{inventoryRows([]string{"id"}, []driver.Value{int64(1)})}},
		{name: "foreign stream", foreign: true, results: []inventoryScriptResult{{columns: foreignColumns, rowsErr: errInventoryCatalogTest}}},
	}
	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openInventoryScriptDatabase(t, "sqlite-relation-"+strconv.Itoa(index), tt.results...)
			table := TableInventory{Name: "records"}
			var err error
			if tt.foreign {
				err = inspectSQLiteForeignKeys(t.Context(), db, &table)
			} else {
				err = inspectSQLiteIndexes(t.Context(), db, &table)
			}
			if err == nil {
				t.Fatal("SQLite relation stage failure was accepted")
			}
		})
	}
}
