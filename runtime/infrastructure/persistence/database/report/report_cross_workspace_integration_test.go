package report

import (
	"path/filepath"
	"testing"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestCrossWorkspaceAggregateIsExplicitAndTenantReportsRemainIsolated(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "cross-workspace.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.DB().ExecContext(t.Context(), `CREATE TABLE sale (workspace_id TEXT NOT NULL, id TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, sold_at TEXT, amount INTEGER, UNIQUE(workspace_id,id))`); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		workspace, id, soldAt string
		amount                int
	}{{"a", "1", "2026-08-01T14:30:00Z", 10}, {"b", "2", "2026-08-01T15:15:00Z", 20}} {
		if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO sale VALUES (?,?,?,?,?,?)`, row.workspace, row.id, row.soldAt, row.soldAt, row.soldAt, row.amount); err != nil {
			t.Fatal(err)
		}
	}
	object := definitionmodel.ObjectSchema{Key: "sale", Fields: []definitionmodel.FieldSchema{{Key: "sold_at", Type: "datetime"}, {Key: "amount", Type: "integer"}}}
	schema := reportmodel.ReportObjectSQLSchema{SQL: `SELECT s.workspace_id AS workspace_id, date_bucket('hour', s.sold_at) AS business_hour, SUM(s.amount) AS total FROM sale s GROUP BY s.workspace_id, date_bucket('hour', s.sold_at) LIMIT 10`, TimeZone: "Asia/Tokyo", SourceObjects: []string{"sale"}, ResultSchema: []reportmodel.ReportResultColumnSchema{{Key: "workspace_id", Type: "text", Kind: "dimension"}, {Key: "business_hour", Type: "datetime", Kind: "dimension"}, {Key: "total", Type: "integer", Kind: "measure"}}}
	plan, err := reportcontract.CompileReportObjectSQL(schema, map[string]definitionmodel.ObjectSchema{"sale": object})
	if err != nil {
		t.Fatal(err)
	}
	request := reportcontract.ReportObjectSQLExecutionRequest{WorkspaceID: "a", Plan: plan, Objects: map[string]definitionmodel.ObjectSchema{"s": object}, Queries: map[string]recordmodel.RecordListQuery{"s": {AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted, SelectFields: plan.Sources[0].Fields}}}
	executor := NewReportSQLStore(store)
	tenant, err := executor.ExecuteReportObjectSQL(t.Context(), request)
	if err != nil || len(tenant.Rows) != 1 || tenant.Rows[0]["workspace_id"] != "a" {
		t.Fatalf("tenant=%#v err=%v", tenant.Rows, err)
	}
	request.CrossWorkspaceAggregate = true
	global, err := executor.ExecuteReportObjectSQL(t.Context(), request)
	if err != nil || len(global.Rows) != 2 || global.Rows[0]["business_hour"] != "2026-08-01T23:00:00+09:00" || global.Rows[1]["business_hour"] != "2026-08-02T00:00:00+09:00" {
		t.Fatalf("global=%#v err=%v", global.Rows, err)
	}
}
