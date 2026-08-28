package datamigration

import (
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestVerifyProducesRetirementComparisonAndIntegrityFacts(t *testing.T) {
	source := openVerificationSQLite(t)
	target := openVerificationSQLite(t)
	defer source.Close()
	defer target.Close()

	for _, db := range []*sql.DB{source, target} {
		if _, err := db.ExecContext(t.Context(), `
			CREATE TABLE parents (id TEXT PRIMARY KEY, workspace_id TEXT);
			CREATE TABLE children (id TEXT PRIMARY KEY, workspace_id TEXT, parent_id TEXT, FOREIGN KEY (parent_id) REFERENCES parents(id));
			INSERT INTO parents VALUES ('parent-1', 'workspace-a');
			INSERT INTO children VALUES ('child-1', 'workspace-a', 'parent-1');
		`); err != nil {
			t.Fatal(err)
		}
	}
	inventory := Inventory{Engine: EngineSQLite, Tables: []TableInventory{
		{Name: "parents", PrimaryKey: []string{"id"}, WorkspaceScoped: true, Columns: []ColumnInventory{{Name: "id"}, {Name: "workspace_id"}}},
		{Name: "children", PrimaryKey: []string{"id"}, WorkspaceScoped: true, Columns: []ColumnInventory{{Name: "id"}, {Name: "workspace_id"}, {Name: "parent_id"}}, ForeignKeys: []ForeignInventory{{Columns: []string{"parent_id"}, ReferencedTable: "parents", ReferencedColumns: []string{"id"}}}},
	}}
	plan := Plan{Source: inventory, Tables: []TablePlan{
		{Name: "parents", CheckpointKey: []string{"id"}, WorkspaceScoped: true, Conversions: []ConversionPlan{{Column: "id", Strategy: "identity"}, {Column: "workspace_id", Strategy: "identity"}}},
		{Name: "children", CheckpointKey: []string{"id"}, WorkspaceScoped: true, Conversions: []ConversionPlan{{Column: "id", Strategy: "identity"}, {Column: "workspace_id", Strategy: "identity"}, {Column: "parent_id", Strategy: "identity"}}},
	}}
	copier := Copier{Source: source, Target: target, SourceEngine: EngineSQLite, Plan: plan}
	report, err := copier.Verify(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !report.Current || report.Tables[0].SourceKeys != 1 || report.Tables[0].SourceSampleDigest == "" {
		t.Fatalf("valid comparison report = %+v", report)
	}

	for _, db := range []*sql.DB{source, target} {
		if _, err := db.ExecContext(t.Context(), `INSERT INTO children VALUES ('child-orphan', '', 'missing-parent')`); err != nil {
			t.Fatal(err)
		}
	}
	report, err = copier.Verify(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	children := report.Tables[1]
	if report.Current || children.SourceInvalidWorkspaces != 1 || children.TargetInvalidWorkspaces != 1 || children.SourceInvalidReferences != 1 || children.TargetInvalidReferences != 1 {
		t.Fatalf("invalid comparison report = %+v", report)
	}
}

func TestBuildRetirementEvidenceRequiresOwnerBusinessAndDispositionProof(t *testing.T) {
	now := time.Now().UTC()
	verification := VerificationReport{VerifiedAt: now, Current: true, Tables: []TableVerification{{Name: "records", Current: true}}}
	input := RetirementEvidenceInput{
		Owner: "record", Replacement: "records_v2", Disposition: "migrate", RecordedAt: now,
		BusinessChecks: []RetirementBusinessCheck{{Name: "active_record_count", SourceValue: "42", ReplacementValue: "42", EvidenceRef: "change-123/query-7"}},
	}
	report := BuildRetirementEvidence(verification, input, now)
	if !report.Ready || len(report.Blockers) != 0 {
		t.Fatalf("ready evidence = %+v", report)
	}
	input.Disposition = "discard"
	input.BusinessChecks[0].ReplacementValue = "41"
	report = BuildRetirementEvidence(verification, input, now)
	if report.Ready || len(report.Blockers) != 2 {
		t.Fatalf("unsafe evidence = %+v", report)
	}
}

func TestVerificationIdentifierUsesDialectQuoting(t *testing.T) {
	if got := verificationIdentifier(EngineMySQL, "order`items"); got != "`order``items`" {
		t.Fatalf("mysql identifier = %s", got)
	}
	if got := verificationIdentifier(EnginePostgres, `order"items`); got != `"order""items"` {
		t.Fatalf("postgres identifier = %s", got)
	}
}

func openVerificationSQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.TempDir()+"/verify.db")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	return db
}
