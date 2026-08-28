package operations

import (
	"reflect"
	"strings"
	"testing"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/datamigration"
)

func TestDatabaseRetirementStatementBuildersCoverEveryDialect(t *testing.T) {
	object := operationsmodel.DatabaseObjectIdentity{Schema: "runtime", Kind: "table", Name: "legacy"}
	for _, test := range []struct {
		name   string
		engine datamigration.Engine
		want   []string
	}{
		{name: "sqlite", engine: datamigration.EngineSQLite, want: []string{"CREATE TRIGGER IF NOT EXISTS", `"runtime"."legacy"`, "RAISE(ABORT"}},
		{name: "postgres", engine: datamigration.EnginePostgres, want: []string{"CREATE OR REPLACE FUNCTION", "DROP TRIGGER IF EXISTS", "EXECUTE FUNCTION"}},
		{name: "mysql", engine: datamigration.EngineMySQL, want: []string{"CREATE TRIGGER", "SIGNAL SQLSTATE", "`runtime`.`legacy`"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			statements, err := databaseWriteProtectionStatements(test.engine, object, "retire:id.with-punctuation")
			if err != nil || len(statements) != 3 {
				t.Fatalf("statements=%v err=%v", statements, err)
			}
			joined := strings.Join(statements, "\n")
			for _, want := range test.want {
				if !strings.Contains(joined, want) {
					t.Fatalf("statements=%q missing %q", joined, want)
				}
			}
		})
	}
	for _, test := range []struct {
		name   string
		engine datamigration.Engine
		object operationsmodel.DatabaseObjectIdentity
	}{
		{name: "wrong kind", engine: datamigration.EngineSQLite, object: operationsmodel.DatabaseObjectIdentity{Kind: "view", Name: "legacy"}},
		{name: "unsafe table", engine: datamigration.EngineSQLite, object: operationsmodel.DatabaseObjectIdentity{Kind: "table", Name: "legacy;drop"}},
		{name: "unsupported engine", engine: datamigration.Engine("oracle"), object: operationsmodel.DatabaseObjectIdentity{Kind: "table", Name: "legacy"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := databaseWriteProtectionStatements(test.engine, test.object, "retire"); err == nil {
				t.Fatal("invalid write protection was accepted")
			}
		})
	}

	for _, engine := range []datamigration.Engine{datamigration.EngineSQLite, datamigration.EnginePostgres, datamigration.EngineMySQL} {
		quarantine, err := databaseQuarantineStatements(engine, object, "retired_legacy")
		if err != nil || len(quarantine) != 1 {
			t.Fatalf("engine=%s quarantine=%v err=%v", engine, quarantine, err)
		}
		restore, err := databaseRestoreStatements(engine, object, "retired_legacy")
		if err != nil || len(restore) != 1 {
			t.Fatalf("engine=%s restore=%v err=%v", engine, restore, err)
		}
		if engine == datamigration.EngineMySQL {
			if !strings.HasPrefix(quarantine[0], "RENAME TABLE") || !strings.HasPrefix(restore[0], "RENAME TABLE") {
				t.Fatalf("mysql rename statements quarantine=%v restore=%v", quarantine, restore)
			}
		} else if !strings.HasPrefix(quarantine[0], "ALTER TABLE") || !strings.HasPrefix(restore[0], "ALTER TABLE") {
			t.Fatalf("alter rename statements quarantine=%v restore=%v", quarantine, restore)
		}
	}
	if _, err := databaseQuarantineStatements(datamigration.EngineSQLite, object, "unsafe-name"); err == nil {
		t.Fatal("unsafe quarantine name was accepted")
	}
	if _, err := databaseQuarantineStatements(datamigration.EngineSQLite, operationsmodel.DatabaseObjectIdentity{Kind: "view", Name: "legacy"}, "retired_legacy"); err == nil {
		t.Fatal("non-table quarantine was accepted")
	}
	if _, err := databaseQuarantineStatements(datamigration.EngineSQLite, operationsmodel.DatabaseObjectIdentity{Kind: "table", Name: "unsafe-name"}, "retired_legacy"); err == nil {
		t.Fatal("unsafe quarantine object was accepted")
	}
	if _, err := databaseRestoreStatements(datamigration.EngineSQLite, operationsmodel.DatabaseObjectIdentity{Kind: "view", Name: "legacy"}, "retired_legacy"); err == nil {
		t.Fatal("non-table restore was accepted")
	}
	if _, err := databaseRestoreStatements(datamigration.EngineSQLite, operationsmodel.DatabaseObjectIdentity{Kind: "table", Name: "unsafe-name"}, "retired_legacy"); err == nil {
		t.Fatal("unsafe restore object was accepted")
	}
}

func TestDatabaseDropStatementCoversKindsDialectsAndSafety(t *testing.T) {
	tests := []struct {
		name   string
		engine datamigration.Engine
		object operationsmodel.DatabaseObjectIdentity
		want   string
	}{
		{name: "sqlite table", engine: datamigration.EngineSQLite, object: operationsmodel.DatabaseObjectIdentity{Schema: "main", Kind: "table", Name: "legacy"}, want: `DROP TABLE "main"."legacy"`},
		{name: "postgres view", engine: datamigration.EnginePostgres, object: operationsmodel.DatabaseObjectIdentity{Schema: "runtime", Kind: "view", Name: "legacy_view"}, want: `DROP VIEW "runtime"."legacy_view"`},
		{name: "sqlite column", engine: datamigration.EngineSQLite, object: operationsmodel.DatabaseObjectIdentity{Kind: "column", ParentName: "legacy", Name: "obsolete"}, want: `ALTER TABLE "legacy" DROP COLUMN "obsolete"`},
		{name: "mysql column", engine: datamigration.EngineMySQL, object: operationsmodel.DatabaseObjectIdentity{Schema: "runtime", Kind: "column", ParentName: "legacy", Name: "obsolete"}, want: "DROP COLUMN `obsolete`, ALGORITHM=INPLACE, LOCK=NONE"},
		{name: "postgres index", engine: datamigration.EnginePostgres, object: operationsmodel.DatabaseObjectIdentity{Schema: "runtime", Kind: "index", ParentName: "legacy", Name: "legacy_idx"}, want: `DROP INDEX "runtime"."legacy_idx"`},
		{name: "mysql index", engine: datamigration.EngineMySQL, object: operationsmodel.DatabaseObjectIdentity{Schema: "runtime", Kind: "index", ParentName: "legacy", Name: "legacy_idx"}, want: "DROP INDEX `legacy_idx`, ALGORITHM=INPLACE, LOCK=NONE"},
		{name: "postgres trigger", engine: datamigration.EnginePostgres, object: operationsmodel.DatabaseObjectIdentity{Schema: "runtime", Kind: "trigger", ParentName: "legacy", Name: "legacy_trigger"}, want: `DROP TRIGGER "legacy_trigger" ON "runtime"."legacy"`},
		{name: "mysql trigger", engine: datamigration.EngineMySQL, object: operationsmodel.DatabaseObjectIdentity{Schema: "runtime", Kind: "trigger", ParentName: "legacy", Name: "legacy_trigger"}, want: "DROP TRIGGER `runtime`.`legacy_trigger`"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			statement, err := databaseDropStatement(test.engine, test.object)
			if err != nil || !strings.Contains(statement, test.want) {
				t.Fatalf("statement=%q err=%v want=%q", statement, err, test.want)
			}
		})
	}
	for _, object := range []operationsmodel.DatabaseObjectIdentity{
		{Kind: "table", Name: "unsafe;drop"},
		{Kind: "column", ParentName: "unsafe parent", Name: "column"},
		{Schema: "unsafe-schema", Kind: "table", Name: "table"},
		{Kind: "sequence", Name: "legacy_sequence"},
	} {
		if _, err := databaseDropStatement(datamigration.EngineSQLite, object); err == nil {
			t.Fatalf("unsafe/unsupported object was accepted: %+v", object)
		}
	}
}

func TestRetirementDependenciesCoverEveryObjectKind(t *testing.T) {
	inventory := datamigration.Inventory{
		Engine: datamigration.EnginePostgres,
		Tables: []datamigration.TableInventory{
			{Name: "parent", EstimatedBytes: 4096, Columns: []datamigration.ColumnInventory{{Name: "id"}, {Name: "obsolete"}}, Indexes: []datamigration.IndexInventory{{Name: "idx_obsolete", Columns: []string{"obsolete"}}}, ForeignKeys: []datamigration.ForeignInventory{{Columns: []string{"obsolete"}, ReferencedTable: "lookup"}, {Columns: []string{"id"}, ReferencedTable: "parent"}}},
			{Name: "child", ForeignKeys: []datamigration.ForeignInventory{{Columns: []string{"parent_id"}, ReferencedTable: "parent"}}},
		},
		Views:    []datamigration.ViewInventory{{Name: "parent_view", Definition: "SELECT * FROM parent"}, {Name: "unrelated", Definition: "SELECT 1"}},
		Triggers: []datamigration.TriggerInventory{{Name: "parent_trigger", Table: "parent"}},
	}
	base := operationsmodel.DatabaseObjectIdentity{Engine: "postgres", Database: "runtime", Schema: "public"}
	dependencies, bytes, err := retirementDependencies(inventory, withRetirementObject(base, "table", "parent", ""))
	if err != nil || bytes != 4096 || !reflect.DeepEqual(databaseObjectNames(dependencies), []string{"foreign_key:child->parent", "view:parent_view"}) {
		t.Fatalf("table dependencies=%+v bytes=%d err=%v", dependencies, bytes, err)
	}
	dependencies, _, err = retirementDependencies(inventory, withRetirementObject(base, "column", "obsolete", "parent"))
	if err != nil || !reflect.DeepEqual(databaseObjectNames(dependencies), []string{"index:idx_obsolete", "foreign_key:obsolete"}) {
		t.Fatalf("column dependencies=%+v err=%v", dependencies, err)
	}
	for _, object := range []operationsmodel.DatabaseObjectIdentity{
		withRetirementObject(base, "index", "idx_obsolete", "parent"),
		withRetirementObject(base, "view", "parent_view", ""),
		withRetirementObject(base, "trigger", "parent_trigger", "parent"),
	} {
		if dependencies, _, err := retirementDependencies(inventory, object); err != nil || len(dependencies) != 0 {
			t.Fatalf("object=%+v dependencies=%v err=%v", object, dependencies, err)
		}
	}
	for _, object := range []operationsmodel.DatabaseObjectIdentity{
		withRetirementObject(base, "table", "missing", ""),
		withRetirementObject(base, "column", "missing", "parent"),
		withRetirementObject(base, "column", "missing", "absent_parent"),
		withRetirementObject(base, "index", "missing", "parent"),
		withRetirementObject(base, "index", "missing", "absent_parent"),
		withRetirementObject(base, "view", "missing", ""),
		withRetirementObject(base, "trigger", "missing", "parent"),
		withRetirementObject(base, "sequence", "missing", ""),
	} {
		if _, _, err := retirementDependencies(inventory, object); err == nil {
			t.Fatalf("missing/unsupported object was accepted: %+v", object)
		}
	}
}

func TestRetirementNamingAndEffectiveObjectEdges(t *testing.T) {
	name := databaseQuarantineObjectName(strings.Repeat("long_name_", 8), "retirement:id.with-punctuation")
	if len(name) > 63 || strings.ContainsAny(name, ":.-") || !strings.HasPrefix(name, "retired_") {
		t.Fatalf("quarantine name=%q len=%d", name, len(name))
	}
	retirement := operationsmodel.DatabaseRetirement{
		State:    operationsmodel.DatabaseRetirementQuarantined,
		Object:   operationsmodel.DatabaseObjectIdentity{Kind: "column", ParentName: "legacy", Name: "obsolete"},
		Evidence: operationsmodel.DatabaseRetirementEvidence{QuarantineObjectName: "retired_legacy"},
	}
	effective := effectiveDatabaseRetirementObject(retirement)
	if effective.Name != "retired_legacy" || effective.ParentName != "retired_legacy" {
		t.Fatalf("effective column=%+v", effective)
	}
	retirement.Object.Kind = "table"
	retirement.Object.ParentName = ""
	effective = effectiveDatabaseRetirementObject(retirement)
	if effective.Name != "retired_legacy" || effective.ParentName != "" {
		t.Fatalf("effective table=%+v", effective)
	}
	retirement.State = operationsmodel.DatabaseRetirementObservationComplete
	if effective := effectiveDatabaseRetirementObject(retirement); effective.Name != "obsolete" {
		t.Fatalf("non-quarantined effective object=%+v", effective)
	}
	if got := quoteRetirementIdentifier(datamigration.EngineMySQL, "name"); got != "`name`" {
		t.Fatalf("mysql quote=%q", got)
	}
	if got := qualifyRetirementIdentifier(datamigration.EngineSQLite, "", "name"); got != `"name"` {
		t.Fatalf("unqualified identifier=%q", got)
	}
	if retirementIndexExists(nil, "missing") || retirementTriggerExists(nil, "missing") {
		t.Fatal("empty inventories reported existing objects")
	}
	if retirementTriggerExists([]datamigration.TriggerInventory{{Name: "other"}}, "missing") {
		t.Fatal("non-matching trigger reported as present")
	}
	view := operationsmodel.DatabaseRetirement{State: operationsmodel.DatabaseRetirementQuarantined, Object: operationsmodel.DatabaseObjectIdentity{Kind: "view", Name: "legacy"}, Evidence: operationsmodel.DatabaseRetirementEvidence{QuarantineObjectName: "retired_legacy"}}
	if effective := effectiveDatabaseRetirementObject(view); effective.Kind != "view" || effective.Name != "retired_legacy" || effective.ParentName != "" {
		t.Fatalf("effective view=%+v", effective)
	}
}

func TestDatabaseRetirementExecutorRejectsUnavailableAndInvalidTransitions(t *testing.T) {
	executor := NewDatabaseRetirementSQLExecutor(nil, nil, nil)
	if executor.now == nil || executor.newID == nil {
		t.Fatal("constructor did not install default dependencies")
	}
	object := operationsmodel.DatabaseObjectIdentity{Engine: "sqlite", Kind: "table", Name: "legacy"}
	current := operationsmodel.DatabaseRetirement{ID: "retire-legacy", Object: object}
	next := current
	if _, err := executor.ApplyDatabaseRetirementTransition(t.Context(), current, next); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("apply unavailable error=%v", err)
	}
	if _, err := executor.PreviewDatabaseRetirement(t.Context(), current); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("preview unavailable error=%v", err)
	}
	zero := DatabaseRetirementSQLExecutor{}
	result, err := zero.ExecuteDatabaseRetirement(t.Context(), current, operationsmodel.DatabaseDropPlan{})
	if err == nil || !strings.Contains(err.Error(), "unavailable") || !strings.HasPrefix(result.AuditEventID, "database-retirement:retire-legacy:") {
		t.Fatalf("execute result=%+v error=%v", result, err)
	}

	store := openDatabaseRetirementExecutorStore(t)
	executor = NewDatabaseRetirementSQLExecutor(store, nil, nil)
	next.Object.Name = "other"
	if _, err := executor.ApplyDatabaseRetirementTransition(t.Context(), current, next); err == nil || !strings.Contains(err.Error(), "identity mismatch") {
		t.Fatalf("identity mismatch error=%v", err)
	}
	invalidEngine := current
	invalidEngine.Object.Engine = "oracle"
	if _, err := executor.ApplyDatabaseRetirementTransition(t.Context(), invalidEngine, invalidEngine); err == nil {
		t.Fatal("invalid engine transition was accepted")
	}
	postgres := current
	postgres.Object.Engine = "postgres"
	if _, err := executor.ApplyDatabaseRetirementTransition(t.Context(), postgres, postgres); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("engine mismatch error=%v", err)
	}
	ordinary := current
	ordinary.State = operationsmodel.DatabaseRetirementReadsSwitched
	ordinaryNext := ordinary
	ordinaryNext.State = operationsmodel.DatabaseRetirementObservationComplete
	if applied, err := executor.ApplyDatabaseRetirementTransition(t.Context(), ordinary, ordinaryNext); err != nil || applied.State != ordinaryNext.State {
		t.Fatalf("no-op transition=%+v err=%v", applied, err)
	}
}

func TestDatabaseRetirementPreviewRejectsUnsupportedScopesAndMissingObjects(t *testing.T) {
	store := openDatabaseRetirementExecutorStore(t)
	executor := NewDatabaseRetirementSQLExecutor(store, nil, nil)
	for _, object := range []operationsmodel.DatabaseObjectIdentity{
		{Engine: "oracle", Kind: "table", Name: "legacy"},
		{Engine: "postgres", Kind: "table", Name: "legacy"},
		{Engine: "sqlite", Kind: "database", Name: "runtime"},
		{Engine: "sqlite", Kind: "schema", Name: "main"},
		{Engine: "sqlite", Kind: "table", Name: "missing"},
	} {
		if _, err := executor.PreviewDatabaseRetirement(t.Context(), operationsmodel.DatabaseRetirement{ID: "retire", Object: object}); err == nil {
			t.Fatalf("unsupported/missing preview was accepted: %+v", object)
		}
	}
}

func withRetirementObject(base operationsmodel.DatabaseObjectIdentity, kind, name, parent string) operationsmodel.DatabaseObjectIdentity {
	base.Kind, base.Name, base.ParentName = kind, name, parent
	return base
}
