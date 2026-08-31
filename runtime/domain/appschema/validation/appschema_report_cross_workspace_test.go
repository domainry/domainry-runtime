package validation

import (
	"testing"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestCrossWorkspaceReportRequiresSuperadminPermissionAndAggregateOnlyShape(t *testing.T) {
	snapshot := appschemamodel.ApplicationSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{{Key: "sale", Fields: []definitionmodel.FieldSchema{{Key: "sold_at", Type: "datetime"}, {Key: "amount", Type: "integer"}, {Key: "receipt_id", Type: "text"}}}}}
	report := reportmodel.ReportSchema{
		Key: "headquarters", AudienceRoles: []string{"superadmin"}, RequiredPermissions: []string{"sale.read"},
		ExecutionScope: &reportmodel.ReportExecutionScopeSchema{Mode: reportmodel.ReportExecutionScopeCrossWorkspaceAggregateV1},
		ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{
			SQL: `SELECT s.workspace_id AS workspace_id, date_bucket('hour', s.sold_at) AS business_hour, SUM(s.amount) AS total FROM sale s GROUP BY s.workspace_id, date_bucket('hour', s.sold_at) LIMIT 100`, TimeZone: "Asia/Tokyo",
			SourceObjects: []string{"sale"}, ResultSchema: []reportmodel.ReportResultColumnSchema{{Key: "workspace_id", Type: "text", Kind: "dimension"}, {Key: "business_hour", Type: "datetime", Kind: "dimension"}, {Key: "total", Type: "integer", Kind: "measure"}},
		},
	}
	if issues := ApplicationSchemaValidateReportDefinitionContract(t.Context(), snapshot, report); len(issues) != 0 {
		t.Fatalf("valid cross-workspace aggregate rejected: %#v", issues)
	}

	for name, test := range map[string]struct {
		mutate func(*reportmodel.ReportSchema)
		code   string
	}{
		"admin audience": {func(r *reportmodel.ReportSchema) { r.AudienceRoles = []string{"admin"} }, "backend.report.cross_workspace_superadmin_audience_required"},
		"no permission":  {func(r *reportmodel.ReportSchema) { r.RequiredPermissions = nil }, "backend.report.cross_workspace_permission_required"},
		"raw row": {func(r *reportmodel.ReportSchema) {
			r.ObjectSQLV1.SQL = `SELECT s.receipt_id AS receipt_id FROM sale s ORDER BY s.receipt_id LIMIT 100`
			r.ObjectSQLV1.ResultSchema = []reportmodel.ReportResultColumnSchema{{Key: "receipt_id", Type: "text", Kind: "dimension"}}
		}, "backend.report.cross_workspace_raw_projection_forbidden"},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := report
			sql := *report.ObjectSQLV1
			candidate.ObjectSQLV1 = &sql
			test.mutate(&candidate)
			issues := ApplicationSchemaValidateReportDefinitionContract(t.Context(), snapshot, candidate)
			found := false
			for _, issue := range issues {
				found = found || issue.ErrorCode == test.code
			}
			if !found {
				t.Fatalf("wanted %s: %#v", test.code, issues)
			}
		})
	}
}
