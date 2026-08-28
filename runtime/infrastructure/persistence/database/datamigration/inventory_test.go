package datamigration

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestSQLiteInventoryCapturesWorkspaceSchemaAndCapacityEvidence(t *testing.T) {
	db := openSQLiteInventoryFixture(t)
	inventory, err := Inspect(t.Context(), db, EngineSQLite, "")
	if err != nil {
		t.Fatal(err)
	}
	if inventory.DatabaseBytes <= 0 || len(inventory.Tables) != 2 {
		t.Fatalf("incomplete inventory: %+v", inventory)
	}
	child, ok := findTable(inventory.Tables, "child_records")
	if !ok {
		t.Fatal("child_records missing")
	}
	if child.Rows != 2 || !child.WorkspaceScoped || child.InvalidWorkspaceRows != 1 {
		t.Fatalf("workspace inventory mismatch: %+v", child)
	}
	if len(child.PrimaryKey) != 1 || child.PrimaryKey[0] != "id" || len(child.Indexes) == 0 || len(child.ForeignKeys) != 1 || len(child.Constraints) < 3 {
		t.Fatalf("key/index/foreign inventory mismatch: %+v", child)
	}
	if len(child.LargeObjectColumns) != 1 || child.LargeObjectColumns[0] != "payload" || child.MaximumLargeObjectSize != 4 {
		t.Fatalf("large object inventory mismatch: %+v", child)
	}
}

func TestSQLiteInventoryCapturesViewsAndTriggers(t *testing.T) {
	db := openSQLiteInventoryFixture(t)
	if _, err := db.ExecContext(t.Context(), `CREATE VIEW active_children AS SELECT id FROM child_records WHERE active = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER prevent_parent_delete BEFORE DELETE ON parent_records BEGIN SELECT RAISE(ABORT, 'protected'); END`); err != nil {
		t.Fatal(err)
	}
	inventory, err := Inspect(t.Context(), db, EngineSQLite, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Views) != 1 || inventory.Views[0].Name != "active_children" || len(inventory.Triggers) != 1 || inventory.Triggers[0].Table != "parent_records" {
		t.Fatalf("view/trigger inventory incomplete: views=%+v triggers=%+v", inventory.Views, inventory.Triggers)
	}
}

func TestParseEngineSupportsAllRuntimeDialects(t *testing.T) {
	for input, expected := range map[string]Engine{"sqlite": EngineSQLite, "postgresql": EnginePostgres, "pgx": EnginePostgres, "mysql": EngineMySQL} {
		actual, err := ParseEngine(input)
		if err != nil || actual != expected {
			t.Fatalf("ParseEngine(%q)=%q, %v; want %q", input, actual, err, expected)
		}
	}
}

func TestDryRunPlanBlocksWorkspaceFallbackAndMissingTarget(t *testing.T) {
	db := openSQLiteInventoryFixture(t)
	source, err := Inspect(t.Context(), db, EngineSQLite, "")
	if err != nil {
		t.Fatal(err)
	}
	target := Inventory{Engine: EnginePostgres, Schema: "domainry_runtime", Tables: []TableInventory{{Name: "parent_records", Columns: []ColumnInventory{{Name: "id", Type: "text"}}, PrimaryKey: []string{"id"}}}}
	plan := BuildPlan(source, target)
	joined := strings.Join(plan.Blockers, "\n")
	if !strings.Contains(joined, "missing workspace_id") || !strings.Contains(joined, "target table child_records is missing") {
		t.Fatalf("dry-run blockers missing: %+v", plan)
	}
	if !plan.ForbidsWorkspaceFallback || !plan.RequiresStopWriteCutover || !plan.RequiresIsolatedRollback {
		t.Fatalf("cutover safety contract missing: %+v", plan)
	}
	if len(plan.PreviewGroups) != 5 {
		t.Fatalf("preview phase groups=%+v", plan.PreviewGroups)
	}
	for index, phase := range []string{"create", "alter", "backfill", "disable", "drop"} {
		if plan.PreviewGroups[index].Phase != phase || plan.PreviewGroups[index].Destructive != (phase == "drop") {
			t.Fatalf("preview phase %d=%+v", index, plan.PreviewGroups[index])
		}
	}
}

func TestDryRunPlanReportsExplicitSQLiteConversions(t *testing.T) {
	db := openSQLiteInventoryFixture(t)
	if _, err := db.ExecContext(t.Context(), `UPDATE child_records SET workspace_id = 'workspace-a' WHERE workspace_id = ''`); err != nil {
		t.Fatal(err)
	}
	source, err := Inspect(t.Context(), db, EngineSQLite, "")
	if err != nil {
		t.Fatal(err)
	}
	target := cloneInventory(t, source)
	target.Engine, target.Schema = EnginePostgres, "domainry_runtime"
	for tableIndex := range target.Tables {
		for columnIndex := range target.Tables[tableIndex].Columns {
			column := &target.Tables[tableIndex].Columns[columnIndex]
			switch column.Name {
			case "active":
				column.Type = "boolean"
			case "payload":
				column.Type = "bytea"
			default:
				column.Type = "text"
			}
		}
	}
	plan := BuildPlan(source, target)
	if len(plan.Blockers) != 0 {
		t.Fatalf("valid plan blocked: %+v", plan.Blockers)
	}
	var child TablePlan
	for _, table := range plan.Tables {
		if table.Name == "child_records" {
			child = table
		}
	}
	foundBoolean, foundBlob := false, false
	for _, conversion := range child.Conversions {
		foundBoolean = foundBoolean || conversion.Column == "active" && conversion.Strategy == "sqlite_zero_one_to_boolean" && conversion.RequiresReview
		foundBlob = foundBlob || conversion.Column == "payload" && conversion.Strategy == "blob_to_bytea" && conversion.Reversible
	}
	if !foundBoolean || !foundBlob {
		t.Fatalf("conversion plan incomplete: %+v", child.Conversions)
	}
}

func TestSequenceInventoryAndPlanRequireExplicitTargetMapping(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "sequence.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE sequenced_records (id INTEGER PRIMARY KEY AUTOINCREMENT, workspace_id TEXT NOT NULL); INSERT INTO sequenced_records (workspace_id) VALUES ('workspace-a')`); err != nil {
		t.Fatal(err)
	}
	source, err := Inspect(t.Context(), db, EngineSQLite, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(source.Sequences) != 1 || source.Sequences[0].CurrentValue != 1 || source.Sequences[0].OwnedColumn != "id" {
		t.Fatalf("SQLite sequence inventory missing: %+v", source.Sequences)
	}
	target := cloneInventory(t, source)
	target.Engine, target.Schema, target.Sequences = EnginePostgres, "domainry_runtime", nil
	plan := BuildPlan(source, target)
	if len(plan.Sequences) != 1 || !plan.Sequences[0].Blocked || !strings.Contains(strings.Join(plan.Blockers, "\n"), "no target mapping") {
		t.Fatalf("missing sequence mapping was accepted: %+v", plan)
	}
	target.Sequences = []SequenceInventory{{Name: "sequenced_records_id_seq", OwnedTable: "sequenced_records", OwnedColumn: "id"}}
	plan = BuildPlan(source, target)
	if len(plan.Sequences) != 1 || plan.Sequences[0].Blocked || plan.Sequences[0].TargetName != "sequenced_records_id_seq" {
		t.Fatalf("owned target sequence was not mapped: %+v", plan.Sequences)
	}
}

func cloneInventory(t *testing.T, source Inventory) Inventory {
	t.Helper()
	raw, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	var target Inventory
	if err := json.Unmarshal(raw, &target); err != nil {
		t.Fatal(err)
	}
	return target
}

func openSQLiteInventoryFixture(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "inventory.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	statements := []string{
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE parent_records (id TEXT PRIMARY KEY)`,
		`CREATE TABLE child_records (id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, parent_id TEXT NOT NULL, active INTEGER NOT NULL, payload BLOB, FOREIGN KEY (parent_id) REFERENCES parent_records(id))`,
		`CREATE UNIQUE INDEX uniq_child_workspace ON child_records(workspace_id, id)`,
		`INSERT INTO parent_records (id) VALUES ('parent-a')`,
		`INSERT INTO child_records (id, workspace_id, parent_id, active, payload) VALUES ('child-a', 'workspace-a', 'parent-a', 1, X'01020304'), ('child-invalid', '', 'parent-a', 0, NULL)`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	return db
}
