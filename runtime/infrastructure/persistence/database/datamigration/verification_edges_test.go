package datamigration

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestVerificationPlanDatabaseConversionAndIntegrityErrors(t *testing.T) {
	source := openCopySQLite(t, filepath.Join(t.TempDir(), "source.db"))
	target := openCopySQLite(t, filepath.Join(t.TempDir(), "target.db"))
	if _, err := source.ExecContext(t.Context(), `CREATE TABLE records (id TEXT PRIMARY KEY, workspace_id TEXT, flag INTEGER); INSERT INTO records VALUES ('1', '', 2)`); err != nil {
		t.Fatal(err)
	}
	if _, err := target.ExecContext(t.Context(), `CREATE TABLE records (id TEXT PRIMARY KEY, workspace_id TEXT, flag INTEGER); INSERT INTO records VALUES ('1', '', 2)`); err != nil {
		t.Fatal(err)
	}
	baseTable := TableInventory{Name: "records", Columns: []ColumnInventory{{Name: "id"}, {Name: "workspace_id"}, {Name: "flag"}}, PrimaryKey: []string{"id"}}
	basePlan := TablePlan{Name: "records", CheckpointKey: []string{"id"}, Conversions: []ConversionPlan{{Column: "id", Strategy: "identity"}, {Column: "workspace_id", Strategy: "identity"}, {Column: "flag", Strategy: "identity"}}}
	copier := Copier{Source: source, Target: target, SourceEngine: EngineSQLite, Plan: Plan{Source: Inventory{Tables: []TableInventory{baseTable}}, Tables: []TablePlan{basePlan}}}

	badKey := copier
	badKey.Plan.Tables = []TablePlan{{Name: "records"}}
	if _, err := badKey.Verify(t.Context()); err == nil || !strings.Contains(err.Error(), "verification ordering key") {
		t.Fatalf("verification key error=%v", err)
	}
	missing := copier
	missing.Plan.Source.Tables = nil
	if _, err := missing.Verify(t.Context()); err == nil || !strings.Contains(err.Error(), "missing from plan") {
		t.Fatalf("missing source error=%v", err)
	}

	closedSource := openCopySQLite(t, filepath.Join(t.TempDir(), "closed-source.db"))
	if err := closedSource.Close(); err != nil {
		t.Fatal(err)
	}
	queryFailure := copier
	queryFailure.Source = closedSource
	if _, err := queryFailure.Verify(t.Context()); err == nil {
		t.Fatal("closed source verification succeeded")
	}
	closedTarget := openCopySQLite(t, filepath.Join(t.TempDir(), "closed-target.db"))
	if err := closedTarget.Close(); err != nil {
		t.Fatal(err)
	}
	queryFailure.Source = source
	queryFailure.Target = closedTarget
	if _, err := queryFailure.Verify(t.Context()); err == nil {
		t.Fatal("closed target verification succeeded")
	}

	conversionFailure := copier
	conversionFailure.Plan.Tables[0].Conversions[2].Strategy = "sqlite_zero_one_to_boolean"
	if _, err := conversionFailure.Verify(t.Context()); err == nil || !strings.Contains(err.Error(), "reversible") {
		t.Fatalf("conversion verification error=%v", err)
	}

	withoutWorkspace := Copier{Source: source, Target: target, SourceEngine: EngineSQLite, Plan: Plan{Source: Inventory{Tables: []TableInventory{{Name: "records", Columns: []ColumnInventory{{Name: "id"}}, PrimaryKey: []string{"id"}}}}, Tables: []TablePlan{{Name: "records", CheckpointKey: []string{"id"}, WorkspaceScoped: true, Conversions: []ConversionPlan{{Column: "id", Strategy: "identity"}}}}}}
	report, err := withoutWorkspace.Verify(t.Context())
	if err != nil || report.Tables[0].SourceInvalidWorkspaces != 1 || report.Tables[0].TargetInvalidWorkspaces != 1 {
		t.Fatalf("missing workspace report=%#v err=%v", report, err)
	}

	invalidForeign := baseTable
	invalidForeign.ForeignKeys = []ForeignInventory{{}}
	if _, err := countInvalidReferences(t.Context(), source, EngineSQLite, "", invalidForeign); err == nil || !strings.Contains(err.Error(), "invalid foreign-key") {
		t.Fatalf("empty foreign error=%v", err)
	}
	invalidForeign.ForeignKeys = []ForeignInventory{{Columns: []string{"id"}, ReferencedColumns: []string{"id", "other"}}}
	if _, err := countInvalidReferences(t.Context(), source, EngineSQLite, "", invalidForeign); err == nil || !strings.Contains(err.Error(), "invalid foreign-key") {
		t.Fatalf("mismatched foreign error=%v", err)
	}
	invalidForeign.ForeignKeys = []ForeignInventory{{Columns: []string{"id"}, ReferencedTable: "missing", ReferencedColumns: []string{"id"}}}
	if _, err := countInvalidReferences(t.Context(), source, EngineSQLite, "", invalidForeign); err == nil {
		t.Fatal("missing referenced table accepted")
	}
}

func TestVerificationDigestBeyondSampleAndDuplicateKeys(t *testing.T) {
	db := openCopySQLite(t, filepath.Join(t.TempDir(), "many.db"))
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE records (id TEXT, workspace_id TEXT)`); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 12; index++ {
		id := strings.Repeat("0", 2-len(strings.TrimSpace(strconvInt(index)))) + strconvInt(index)
		if _, err := db.ExecContext(t.Context(), `INSERT INTO records VALUES (?, 'workspace')`, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO records VALUES ('00', 'workspace')`); err != nil {
		t.Fatal(err)
	}
	table := TableInventory{Name: "records", Columns: []ColumnInventory{{Name: "id"}, {Name: "workspace_id"}}, PrimaryKey: []string{"id"}}
	plan := TablePlan{Name: "records", CheckpointKey: []string{"id"}, WorkspaceScoped: true, Conversions: []ConversionPlan{{Column: "id", Strategy: "identity"}, {Column: "workspace_id", Strategy: "identity"}}}
	copier := Copier{Source: db, Plan: Plan{Source: Inventory{Tables: []TableInventory{table}}}}
	facts, err := copier.digestTable(t.Context(), db, EngineSQLite, "", plan, false)
	if err != nil || facts.rows != 13 || facts.keys != 12 || facts.duplicateRows != 1 || facts.sampleDigest == "" {
		t.Fatalf("facts=%#v err=%v", facts, err)
	}
}
