package datamigration

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"strconv"
	"testing"
)

func TestPostgresInventoryStageErrors(t *testing.T) {
	tests := []struct {
		name    string
		results []inventoryScriptResult
	}{
		{name: "table row scan", results: []inventoryScriptResult{inventoryRows([]string{"table_name", "extra"}, []driver.Value{"records"})}},
		{name: "table rows close", results: []inventoryScriptResult{{columns: []string{"table_name"}, closeErr: errInventoryCatalogTest}}},
		{name: "table inspect", results: []inventoryScriptResult{inventoryRows([]string{"table_name"}, []driver.Value{"records"}), {err: errInventoryCatalogTest}}},
		{name: "sequences", results: []inventoryScriptResult{inventoryRows([]string{"table_name"}), {err: errInventoryCatalogTest}}},
		{name: "views", results: []inventoryScriptResult{inventoryRows([]string{"table_name"}), inventoryRows([]string{"name", "table", "column"}), {err: errInventoryCatalogTest}}},
	}
	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openInventoryScriptDatabase(t, "pg-stage-"+strconv.Itoa(index), tt.results...)
			if _, err := inspectPostgres(t.Context(), db, "public"); err == nil {
				t.Fatal("PostgreSQL stage failure was accepted")
			}
		})
	}
}

func TestMySQLInventoryStageErrors(t *testing.T) {
	tests := []struct {
		name    string
		schema  string
		results []inventoryScriptResult
	}{
		{name: "resolve schema", results: []inventoryScriptResult{{err: errInventoryCatalogTest}}},
		{name: "table row scan", schema: "runtime", results: []inventoryScriptResult{inventoryRows([]string{"table_name"}, []driver.Value{"records"})}},
		{name: "table rows close", schema: "runtime", results: []inventoryScriptResult{{columns: []string{"table_name", "size"}, closeErr: errInventoryCatalogTest}}},
		{name: "table inspect", schema: "runtime", results: []inventoryScriptResult{inventoryRows([]string{"table_name", "size"}, []driver.Value{"records", int64(1)}), {err: errInventoryCatalogTest}}},
		{name: "views", schema: "runtime", results: []inventoryScriptResult{inventoryRows([]string{"table_name", "size"}), {err: errInventoryCatalogTest}}},
	}
	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openInventoryScriptDatabase(t, "mysql-stage-"+strconv.Itoa(index), tt.results...)
			if _, err := inspectMySQL(t.Context(), db, tt.schema); err == nil {
				t.Fatal("MySQL stage failure was accepted")
			}
		})
	}
}

func postgresTablePrefix(columns inventoryScriptResult, rest ...inventoryScriptResult) []inventoryScriptResult {
	results := []inventoryScriptResult{
		inventoryRows([]string{"count"}, []driver.Value{int64(1)}),
		inventoryRows([]string{"size"}, []driver.Value{int64(32)}),
		columns,
	}
	return append(results, rest...)
}

func mysqlTablePrefix(columns inventoryScriptResult, rest ...inventoryScriptResult) []inventoryScriptResult {
	results := []inventoryScriptResult{
		inventoryRows([]string{"count"}, []driver.Value{int64(1)}),
		columns,
	}
	return append(results, rest...)
}

func TestPostgresTableStageErrors(t *testing.T) {
	emptyColumns := inventoryRows([]string{"name", "type", "nullable", "default"})
	emptyPK := inventoryRows([]string{"name"})
	emptyIndexes := inventoryRows([]string{"name", "unique", "columns"})
	emptyForeign := inventoryRows([]string{"name", "column", "table", "referenced"})
	tests := []struct {
		name    string
		results []inventoryScriptResult
	}{
		{name: "columns query", results: postgresTablePrefix(inventoryScriptResult{err: errInventoryCatalogTest})},
		{name: "columns scan", results: postgresTablePrefix(inventoryRows([]string{"name"}, []driver.Value{"id"}))},
		{name: "columns close", results: postgresTablePrefix(inventoryScriptResult{columns: emptyColumns.columns, closeErr: errInventoryCatalogTest})},
		{name: "primary key query", results: postgresTablePrefix(emptyColumns, inventoryScriptResult{err: errInventoryCatalogTest})},
		{name: "primary key scan", results: postgresTablePrefix(emptyColumns, inventoryRows([]string{"name", "extra"}, []driver.Value{"id"}))},
		{name: "primary key close", results: postgresTablePrefix(emptyColumns, inventoryScriptResult{columns: emptyPK.columns, closeErr: errInventoryCatalogTest})},
		{name: "indexes", results: postgresTablePrefix(emptyColumns, emptyPK, inventoryScriptResult{err: errInventoryCatalogTest})},
		{name: "foreign keys", results: postgresTablePrefix(emptyColumns, emptyPK, emptyIndexes, inventoryScriptResult{err: errInventoryCatalogTest})},
		{name: "constraints", results: postgresTablePrefix(emptyColumns, emptyPK, emptyIndexes, emptyForeign, inventoryScriptResult{err: errInventoryCatalogTest})},
		{name: "workspace validation", results: postgresTablePrefix(
			inventoryRows([]string{"name", "type", "nullable", "default"}, []driver.Value{"workspace_id", "text", "NO", ""}),
			emptyPK, emptyIndexes, emptyForeign, inventoryRows([]string{"name", "type"}), inventoryScriptResult{err: errInventoryCatalogTest},
		)},
	}
	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openInventoryScriptDatabase(t, "pg-table-stage-"+strconv.Itoa(index), tt.results...)
			if _, err := inspectPostgresTable(t.Context(), db, "public", "records"); err == nil {
				t.Fatal("PostgreSQL table stage failure was accepted")
			}
		})
	}
}

func TestMySQLTableStageErrors(t *testing.T) {
	emptyColumns := inventoryRows([]string{"name", "type", "nullable", "default", "key"})
	emptyIndexes := inventoryRows([]string{"name", "non_unique", "column"})
	emptyForeign := inventoryRows([]string{"name", "column", "table", "referenced"})
	tests := []struct {
		name    string
		results []inventoryScriptResult
	}{
		{name: "columns query", results: mysqlTablePrefix(inventoryScriptResult{err: errInventoryCatalogTest})},
		{name: "columns scan", results: mysqlTablePrefix(inventoryRows([]string{"name"}, []driver.Value{"id"}))},
		{name: "columns close", results: mysqlTablePrefix(inventoryScriptResult{columns: emptyColumns.columns, closeErr: errInventoryCatalogTest})},
		{name: "indexes", results: mysqlTablePrefix(emptyColumns, inventoryScriptResult{err: errInventoryCatalogTest})},
		{name: "foreign keys", results: mysqlTablePrefix(emptyColumns, emptyIndexes, inventoryScriptResult{err: errInventoryCatalogTest})},
		{name: "constraints", results: mysqlTablePrefix(emptyColumns, emptyIndexes, emptyForeign, inventoryScriptResult{err: errInventoryCatalogTest})},
		{name: "workspace validation", results: mysqlTablePrefix(
			inventoryRows([]string{"name", "type", "nullable", "default", "key"}, []driver.Value{"workspace_id", "varchar(64)", "NO", "", ""}),
			emptyIndexes, emptyForeign, inventoryRows([]string{"name", "type"}), inventoryScriptResult{err: errInventoryCatalogTest},
		)},
	}
	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openInventoryScriptDatabase(t, "mysql-table-stage-"+strconv.Itoa(index), tt.results...)
			if _, err := inspectMySQLTable(t.Context(), db, "runtime", "records"); err == nil {
				t.Fatal("MySQL table stage failure was accepted")
			}
		})
	}
}

func TestCatalogRowsTerminalErrorsPropagate(t *testing.T) {
	terminal := inventoryScriptResult{columns: []string{"name"}, rowsErr: errors.New("row stream failed")}
	db := openInventoryScriptDatabase(t, "terminal-constraints", terminal)
	if _, err := inspectInformationSchemaConstraints(t.Context(), db, EnginePostgres, "public", "records"); err == nil {
		t.Fatal("constraint row stream failure was accepted")
	}
}

func TestExternalViewsTriggersAndSequenceLateFailures(t *testing.T) {
	viewColumns := []string{"name", "definition"}
	triggerColumns := []string{"name", "table", "timing", "event", "definition"}
	tests := []struct {
		name    string
		results []inventoryScriptResult
		call    func(*testing.T, *sql.DB) error
	}{
		{name: "postgres view stream", results: []inventoryScriptResult{{columns: viewColumns, rowsErr: errInventoryCatalogTest}}, call: func(t *testing.T, db *sql.DB) error {
			return inspectPostgresViewsAndTriggers(t.Context(), db, "public", &Inventory{})
		}},
		{name: "postgres trigger query", results: []inventoryScriptResult{inventoryRows(viewColumns), {err: errInventoryCatalogTest}}, call: func(t *testing.T, db *sql.DB) error {
			return inspectPostgresViewsAndTriggers(t.Context(), db, "public", &Inventory{})
		}},
		{name: "postgres trigger scan", results: []inventoryScriptResult{inventoryRows(viewColumns), inventoryRows([]string{"name"}, []driver.Value{"trigger"})}, call: func(t *testing.T, db *sql.DB) error {
			return inspectPostgresViewsAndTriggers(t.Context(), db, "public", &Inventory{})
		}},
		{name: "postgres sequence value", results: []inventoryScriptResult{inventoryRows([]string{"name", "table", "column"}, []driver.Value{"records_id_seq", "records", "id"}), {err: errInventoryCatalogTest}}, call: func(t *testing.T, db *sql.DB) error {
			return inspectPostgresSequences(t.Context(), db, "public", &Inventory{})
		}},
		{name: "mysql view stream", results: []inventoryScriptResult{{columns: viewColumns, rowsErr: errInventoryCatalogTest}}, call: func(t *testing.T, db *sql.DB) error {
			return inspectMySQLViewsAndTriggers(t.Context(), db, "runtime", &Inventory{})
		}},
		{name: "mysql trigger query", results: []inventoryScriptResult{inventoryRows(viewColumns), {err: errInventoryCatalogTest}}, call: func(t *testing.T, db *sql.DB) error {
			return inspectMySQLViewsAndTriggers(t.Context(), db, "runtime", &Inventory{})
		}},
		{name: "mysql trigger scan", results: []inventoryScriptResult{inventoryRows(viewColumns), inventoryRows([]string{"name"}, []driver.Value{"trigger"})}, call: func(t *testing.T, db *sql.DB) error {
			return inspectMySQLViewsAndTriggers(t.Context(), db, "runtime", &Inventory{})
		}},
		{name: "postgres trigger stream", results: []inventoryScriptResult{inventoryRows(viewColumns), {columns: triggerColumns, rowsErr: errInventoryCatalogTest}}, call: func(t *testing.T, db *sql.DB) error {
			return inspectPostgresViewsAndTriggers(t.Context(), db, "public", &Inventory{})
		}},
		{name: "mysql trigger stream", results: []inventoryScriptResult{inventoryRows(viewColumns), {columns: triggerColumns, rowsErr: errInventoryCatalogTest}}, call: func(t *testing.T, db *sql.DB) error {
			return inspectMySQLViewsAndTriggers(t.Context(), db, "runtime", &Inventory{})
		}},
	}
	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openInventoryScriptDatabase(t, "late-stage-"+strconv.Itoa(index), tt.results...)
			if err := tt.call(t, db); err == nil {
				t.Fatal("late catalog failure was accepted")
			}
		})
	}
}
