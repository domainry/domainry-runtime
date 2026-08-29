package schema

import (
	"database/sql/driver"
	"strings"
	"testing"

	mysqlevidence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/mysql/evidence"
	sqliteevidence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite/evidence"
)

func TestMySQLLargeEvidenceColumnNormalizationRepairsExistingTextColumns(t *testing.T) {
	state := &schemaSQLState{querySteps: []schemaSQLQueryStep{
		{
			columns: []string{"TABLE_NAME", "COLUMN_NAME", "DATA_TYPE"},
			rows: [][]driver.Value{
				{"report_export_artifacts", "content_base64", "text"},
				{"business_audit_export_artifacts", "content_base64", "longtext"},
				{"record_batch_job_chunks", "content", "text"},
			},
		},
		{
			columns: []string{"COLUMN_NAME", "COLUMN_TYPE", "CHARACTER_SET_NAME", "COLLATION_NAME"},
			rows: [][]driver.Value{
				{"id", "varchar(191)", "ascii", "ascii_bin"},
				{"created_at", "varchar(191)", "ascii", "ascii_bin"},
			},
		},
	}}
	database := openSchemaScriptedDB(state)
	t.Cleanup(func() { _ = database.Close() })
	store := scriptedSchemaStore{db: database, driver: "mysql"}

	if err := mysqlevidence.NewProfile().Normalize(t.Context(), database, store.RuntimeRenderer()); err != nil {
		t.Fatal(err)
	}
	if len(state.execQueries) != 2 {
		t.Fatalf("normalization statements=%v", state.execQueries)
	}
	joined := strings.Join(state.execQueries, "\n")
	for _, fragment := range []string{
		`ALTER TABLE "report_export_artifacts" MODIFY COLUMN "content_base64" LONGTEXT NOT NULL`,
		`ALTER TABLE "record_batch_job_chunks" MODIFY COLUMN "content" LONGTEXT NOT NULL`,
	} {
		if !strings.Contains(joined, fragment) {
			t.Fatalf("normalization DDL missing %q: %s", fragment, joined)
		}
	}
	if strings.Contains(joined, "business_audit_export_artifacts") {
		t.Fatalf("existing LONGTEXT column was rewritten: %s", joined)
	}
}

func TestLargeEvidenceColumnNormalizationIsMySQLOnly(t *testing.T) {
	state := &schemaSQLState{}
	database := openSchemaScriptedDB(state)
	t.Cleanup(func() { _ = database.Close() })
	store := scriptedSchemaStore{db: database, driver: "sqlite"}
	if err := sqliteevidence.NewProfile().Normalize(t.Context(), database, store.RuntimeRenderer()); err != nil {
		t.Fatal(err)
	}
	if len(state.execQueries) != 0 || len(state.querySteps) != 0 {
		t.Fatalf("non-MySQL schema was inspected or mutated: %#v", state)
	}
}
