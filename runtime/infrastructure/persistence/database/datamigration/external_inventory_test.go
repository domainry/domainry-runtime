package datamigration

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestExternalCatalogInventoryCapturesTablesColumnsIndexesConstraintsViewsAndTriggers(t *testing.T) {
	for _, test := range []struct {
		name, driver, env, schema string
	}{
		{name: "postgres", driver: "pgx", env: "RUNTIME_POSTGRES_TEST_DSN", schema: "public"},
		{name: "mysql", driver: "mysql", env: "RUNTIME_MYSQL_TEST_DSN"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dsn := strings.TrimSpace(os.Getenv(test.env))
			if dsn == "" {
				t.Skipf("%s is not configured", test.env)
			}
			db, err := sql.Open(test.driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			schema := test.schema
			if test.name == "mysql" {
				if err := db.QueryRowContext(t.Context(), "SELECT DATABASE()").Scan(&schema); err != nil || strings.TrimSpace(schema) == "" {
					t.Fatalf("resolve MySQL catalog schema: schema=%q err=%v", schema, err)
				}
			}
			suffix := fmt.Sprintf("%d", time.Now().UnixNano())
			parent, child, view, trigger := "catalog_parent_"+suffix, "catalog_child_"+suffix, "catalog_view_"+suffix, "catalog_trigger_"+suffix
			quote := func(value string) string { return `"` + value + `"` }
			placeholder := "$1"
			if test.name == "mysql" {
				quote = func(value string) string { return "`" + value + "`" }
				placeholder = "?"
			}
			qparent, qchild, qview, qtrigger := quote(parent), quote(child), quote(view), quote(trigger)
			statements := []string{
				"CREATE TABLE " + qparent + " (id VARCHAR(64) PRIMARY KEY)",
				"CREATE TABLE " + qchild + " (id VARCHAR(64) PRIMARY KEY, parent_id VARCHAR(64) NOT NULL, value VARCHAR(255), CONSTRAINT " + quote("fk_"+suffix) + " FOREIGN KEY (parent_id) REFERENCES " + qparent + "(id))",
				"CREATE UNIQUE INDEX " + quote("idx_"+suffix) + " ON " + qchild + " (value)",
				"CREATE VIEW " + qview + " AS SELECT id, value FROM " + qchild,
			}
			if test.name == "postgres" {
				function := quote("catalog_function_" + suffix)
				statements = append(statements,
					"CREATE FUNCTION "+function+"() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END $$",
					"CREATE TRIGGER "+qtrigger+" BEFORE INSERT ON "+qchild+" FOR EACH ROW EXECUTE FUNCTION "+function+"()",
				)
				t.Cleanup(func() { _, _ = db.ExecContext(t.Context(), "DROP FUNCTION IF EXISTS "+function+"()") })
			} else {
				statements = append(statements, "CREATE TRIGGER "+qtrigger+" BEFORE INSERT ON "+qchild+" FOR EACH ROW SET NEW.value = COALESCE(NEW.value, 'default')")
			}
			for _, statement := range statements {
				if _, err := db.ExecContext(t.Context(), statement); err != nil {
					t.Fatal(err)
				}
			}
			t.Cleanup(func() {
				_, _ = db.ExecContext(t.Context(), "DROP VIEW IF EXISTS "+qview)
				_, _ = db.ExecContext(t.Context(), "DROP TABLE IF EXISTS "+qchild)
				_, _ = db.ExecContext(t.Context(), "DROP TABLE IF EXISTS "+qparent)
			})
			if _, err := db.ExecContext(t.Context(), "INSERT INTO "+qparent+" (id) VALUES ("+placeholder+")", "parent"); err != nil {
				t.Fatal(err)
			}
			inventory, err := Inspect(t.Context(), db, Engine(test.name), schema)
			if err != nil {
				t.Fatal(err)
			}
			table, ok := findTable(inventory.Tables, child)
			if !ok || len(table.Columns) != 3 || len(table.Indexes) < 2 || len(table.Constraints) < 3 || len(table.ForeignKeys) != 1 {
				t.Fatalf("incomplete %s table catalog: %+v", test.name, table)
			}
			if !inventoryHasView(inventory.Views, view) || !inventoryHasTrigger(inventory.Triggers, trigger) {
				t.Fatalf("incomplete %s view/trigger catalog: views=%+v triggers=%+v", test.name, inventory.Views, inventory.Triggers)
			}
		})
	}
}

func inventoryHasView(values []ViewInventory, name string) bool {
	for _, value := range values {
		if value.Name == name {
			return true
		}
	}
	return false
}

func inventoryHasTrigger(values []TriggerInventory, name string) bool {
	for _, value := range values {
		if value.Name == name {
			return true
		}
	}
	return false
}
