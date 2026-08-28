package datamigration

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"strconv"
	"strings"
	"testing"
)

var errInventoryCatalogTest = errors.New("catalog unavailable")

func TestExternalInventoryQueryErrorsPropagate(t *testing.T) {
	tests := []struct {
		name string
		call func(*testing.T, *inventoryScript) error
	}{
		{name: "postgres inventory", call: func(t *testing.T, script *inventoryScript) error {
			_, err := inspectPostgres(t.Context(), openInventoryScriptWithValue(t, "pg-inventory-error", script), "public")
			return err
		}},
		{name: "postgres views", call: func(t *testing.T, script *inventoryScript) error {
			return inspectPostgresViewsAndTriggers(t.Context(), openInventoryScriptWithValue(t, "pg-views-error", script), "public", &Inventory{})
		}},
		{name: "postgres sequences", call: func(t *testing.T, script *inventoryScript) error {
			return inspectPostgresSequences(t.Context(), openInventoryScriptWithValue(t, "pg-sequences-error", script), "public", &Inventory{})
		}},
		{name: "postgres table", call: func(t *testing.T, script *inventoryScript) error {
			_, err := inspectPostgresTable(t.Context(), openInventoryScriptWithValue(t, "pg-table-error", script), "public", "records")
			return err
		}},
		{name: "postgres indexes", call: func(t *testing.T, script *inventoryScript) error {
			return inspectPostgresIndexes(t.Context(), openInventoryScriptWithValue(t, "pg-indexes-error", script), "public", &TableInventory{Name: "records"})
		}},
		{name: "postgres foreign keys", call: func(t *testing.T, script *inventoryScript) error {
			return inspectPostgresForeignKeys(t.Context(), openInventoryScriptWithValue(t, "pg-foreign-error", script), "public", &TableInventory{Name: "records"})
		}},
		{name: "mysql inventory", call: func(t *testing.T, script *inventoryScript) error {
			_, err := inspectMySQL(t.Context(), openInventoryScriptWithValue(t, "mysql-inventory-error", script), "runtime")
			return err
		}},
		{name: "mysql table", call: func(t *testing.T, script *inventoryScript) error {
			_, err := inspectMySQLTable(t.Context(), openInventoryScriptWithValue(t, "mysql-table-error", script), "runtime", "records")
			return err
		}},
		{name: "mysql indexes", call: func(t *testing.T, script *inventoryScript) error {
			return inspectMySQLIndexes(t.Context(), openInventoryScriptWithValue(t, "mysql-index-error", script), "runtime", &TableInventory{Name: "records"})
		}},
		{name: "mysql foreign keys", call: func(t *testing.T, script *inventoryScript) error {
			return inspectMySQLForeignKeys(t.Context(), openInventoryScriptWithValue(t, "mysql-foreign-error", script), "runtime", &TableInventory{Name: "records"})
		}},
		{name: "mysql views", call: func(t *testing.T, script *inventoryScript) error {
			return inspectMySQLViewsAndTriggers(t.Context(), openInventoryScriptWithValue(t, "mysql-views-error", script), "runtime", &Inventory{})
		}},
		{name: "constraints", call: func(t *testing.T, script *inventoryScript) error {
			_, err := inspectInformationSchemaConstraints(t.Context(), openInventoryScriptWithValue(t, "constraints-error", script), EnginePostgres, "public", "records")
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call(t, &inventoryScript{results: []inventoryScriptResult{{err: errInventoryCatalogTest}}})
			if err == nil || !strings.Contains(err.Error(), errInventoryCatalogTest.Error()) {
				t.Fatalf("catalog error = %v", err)
			}
		})
	}
}

func openInventoryScriptWithValue(t *testing.T, name string, script *inventoryScript) *sql.DB {
	t.Helper()
	inventoryDriverOnce.Do(func() { sql.Register("runtime-inventory-script", inventoryScriptDriver{}) })
	inventoryScriptsMu.Lock()
	inventoryScripts[name] = script
	inventoryScriptsMu.Unlock()
	db, err := sql.Open("runtime-inventory-script", name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		inventoryScriptsMu.Lock()
		delete(inventoryScripts, name)
		inventoryScriptsMu.Unlock()
	})
	return db
}

func TestExternalInventoryRejectsMalformedCatalogRows(t *testing.T) {
	tests := []struct {
		name string
		call func(*testing.T, *sql.DB) error
	}{
		{name: "postgres views", call: func(t *testing.T, db *sql.DB) error {
			return inspectPostgresViewsAndTriggers(t.Context(), db, "public", &Inventory{})
		}},
		{name: "postgres sequences", call: func(t *testing.T, db *sql.DB) error {
			return inspectPostgresSequences(t.Context(), db, "public", &Inventory{})
		}},
		{name: "postgres indexes", call: func(t *testing.T, db *sql.DB) error {
			return inspectPostgresIndexes(t.Context(), db, "public", &TableInventory{Name: "records"})
		}},
		{name: "postgres foreign keys", call: func(t *testing.T, db *sql.DB) error {
			return inspectPostgresForeignKeys(t.Context(), db, "public", &TableInventory{Name: "records"})
		}},
		{name: "mysql indexes", call: func(t *testing.T, db *sql.DB) error {
			return inspectMySQLIndexes(t.Context(), db, "runtime", &TableInventory{Name: "records"})
		}},
		{name: "mysql foreign keys", call: func(t *testing.T, db *sql.DB) error {
			return inspectMySQLForeignKeys(t.Context(), db, "runtime", &TableInventory{Name: "records"})
		}},
		{name: "mysql views", call: func(t *testing.T, db *sql.DB) error {
			return inspectMySQLViewsAndTriggers(t.Context(), db, "runtime", &Inventory{})
		}},
		{name: "constraints", call: func(t *testing.T, db *sql.DB) error {
			_, err := inspectInformationSchemaConstraints(t.Context(), db, EngineMySQL, "runtime", "records")
			return err
		}},
	}
	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openInventoryScriptDatabase(t, "malformed-"+strconv.Itoa(index), inventoryRows([]string{"only"}, []driver.Value{"value"}))
			if err := tt.call(t, db); err == nil {
				t.Fatal("malformed catalog row was accepted")
			}
		})
	}
}

func TestPostgresInventoryRejectsUnsafeSequenceAndInvalidIndexJSON(t *testing.T) {
	sequenceDB := openInventoryScriptDatabase(t, "unsafe-sequence", inventoryRows([]string{"name", "table", "column"}, []driver.Value{"unsafe-name", "records", "id"}))
	if err := inspectPostgresSequences(t.Context(), sequenceDB, "public", &Inventory{}); err == nil || !strings.Contains(err.Error(), "unsafe PostgreSQL sequence") {
		t.Fatalf("unsafe sequence error = %v", err)
	}
	indexDB := openInventoryScriptDatabase(t, "invalid-index-json", inventoryRows([]string{"name", "unique", "columns"}, []driver.Value{"records_idx", true, "not-json"}))
	if err := inspectPostgresIndexes(t.Context(), indexDB, "public", &TableInventory{Name: "records"}); err == nil || !strings.Contains(err.Error(), "decode PostgreSQL index columns") {
		t.Fatalf("invalid index JSON error = %v", err)
	}
}
