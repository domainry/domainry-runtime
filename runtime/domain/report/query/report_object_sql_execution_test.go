package query

import (
	"context"
	"encoding/json"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	reportobjectsql "github.com/domainry/domainry-runtime/runtime/domain/report/query/objectsql"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

type objectSQLAccessProbe struct {
	objects map[string]definitionmodel.ObjectSchema
	denied  string
}

func (a objectSQLAccessProbe) ReportObjectForAction(_ context.Context, _ principalmodel.Principal, objectKey, _ string) (definitionmodel.ObjectSchema, error) {
	return a.objects[objectKey], nil
}
func (objectSQLAccessProbe) NormalizeReportListQuery(_ context.Context, _ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery, _ principalmodel.Principal) recordmodel.RecordListQuery {
	return query
}
func (objectSQLAccessProbe) CanAccessReportRecord(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool {
	return true
}
func (objectSQLAccessProbe) ProjectReportRecordFields(_ context.Context, _ principalmodel.Principal, _ definitionmodel.ObjectSchema, records []recordmodel.Record) ([]recordmodel.Record, error) {
	return records, nil
}
func (a objectSQLAccessProbe) AuthorizeReportObjectSQLField(_ context.Context, _ principalmodel.Principal, _ definitionmodel.ObjectSchema, fieldKey string) error {
	if fieldKey == a.denied {
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.report.object_sql_field_denied"}
	}
	return nil
}

type objectSQLExecutorProbe struct {
	calls   int
	request reportcontract.ReportObjectSQLExecutionRequest
}

func (e *objectSQLExecutorProbe) ExecuteReportObjectSQL(_ context.Context, request reportcontract.ReportObjectSQLExecutionRequest) (reportcontract.ReportObjectSQLExecutionResult, error) {
	e.calls++
	e.request = request
	return reportcontract.ReportObjectSQLExecutionResult{Rows: []map[string]string{{"total": "1.00"}}}, nil
}

func TestCrossWorkspaceObjectSQLRequiresAuthorizedSuperadminAndSetsExplicitStoreScope(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "sale", Fields: []definitionmodel.FieldSchema{{Key: "amount", Type: "integer"}}}
	report := reportmodel.ReportSchema{Key: "hq", AudienceRoles: []string{"superadmin"}, RequiredPermissions: []string{"reports.hq.read"}, ExecutionScope: &reportmodel.ReportExecutionScopeSchema{Mode: reportmodel.ReportExecutionScopeCrossWorkspaceAggregateV1}, ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{SQL: `SELECT SUM(s.amount) AS total FROM sale s LIMIT 1`, SourceObjects: []string{"sale"}, ResultSchema: []reportmodel.ReportResultColumnSchema{{Key: "total", Type: "integer", Kind: "measure"}}}}
	executor := &objectSQLExecutorProbe{}
	service := NewReportDomainService(ReportDependencies{Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
		return []reportmodel.ReportSchema{report}
	}, Access: objectSQLAccessProbe{objects: map[string]definitionmodel.ObjectSchema{"sale": object}}, ObjectSQL: executor})
	principal := func(role string, permissions ...string) principalmodel.Principal {
		return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "default", UserID: "root"}}, accessfixture.Bundle{Key: role, Permissions: permissions, RecordScope: "all_records"})
	}
	for _, denied := range []principalmodel.Principal{{}, principal("staff", "reports.hq.read"), principal("admin", "reports.hq.read"), principal("superadmin")} {
		if _, err := service.Summary(t.Context(), report.Key, denied); apperror.CodeOf(err) != "backend.report.not_found" {
			t.Fatalf("principal=%#v err=%v", denied, err)
		}
	}
	if _, err := service.Summary(t.Context(), report.Key, principal("superadmin", "reports.hq.read")); err != nil {
		t.Fatal(err)
	}
	if executor.calls != 1 || !executor.request.CrossWorkspaceAggregate {
		t.Fatalf("calls=%d request=%#v", executor.calls, executor.request)
	}
}

func TestObjectSQLRejectsDeniedFieldsFromEverySQLClause(t *testing.T) {
	currency := map[string]any{"precision": 19, "scale": 2}
	objects := map[string]definitionmodel.ObjectSchema{
		"sale":    {Key: "sale", Fields: []definitionmodel.FieldSchema{{Key: "amount", Type: "currency", Config: currency}, {Key: "join_secret", Type: "text"}, {Key: "where_secret", Type: "text"}, {Key: "group_secret", Type: "text"}, {Key: "having_secret", Type: "integer"}, {Key: "order_secret", Type: "integer"}}},
		"payment": {Key: "payment", Fields: []definitionmodel.FieldSchema{{Key: "sale_id", Type: "text"}}},
	}
	report := reportmodel.ReportSchema{Key: "secure", ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{
		SQL:           `SELECT SUM(s.amount) AS total FROM sale s INNER JOIN payment p ON p.sale_id = s.join_secret WHERE s.where_secret = :filter GROUP BY s.group_secret HAVING SUM(s.having_secret) > 0 ORDER BY SUM(s.order_secret) DESC LIMIT 10`,
		SourceObjects: []string{"sale", "payment"}, Parameters: []reportmodel.ReportObjectSQLParameter{{Key: "filter", Type: "text", Required: true}},
		JoinCardinalities: []reportmodel.ReportObjectSQLCardinality{{Alias: "p", Cardinality: "many_to_one"}},
		ResultSchema:      []reportmodel.ReportResultColumnSchema{{Key: "total", Type: "currency", Kind: "measure", Precision: 19, Scale: 2}},
	}}
	for clause, fieldKey := range map[string]string{"select": "amount", "join_left": "join_secret", "join_right": "sale_id", "where": "where_secret", "group_by": "group_secret", "having": "having_secret", "order_by": "order_secret"} {
		t.Run(clause, func(t *testing.T) {
			executor := &objectSQLExecutorProbe{}
			service := NewReportDomainService(ReportDependencies{Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
				return []reportmodel.ReportSchema{report}
			}, Access: objectSQLAccessProbe{objects: objects, denied: fieldKey}, ObjectSQL: executor})
			_, err := service.QueryObjectSQL(t.Context(), report.Key, map[string]any{"filter": "visible"}, principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a"}})
			if apperror.CodeOf(err) != "backend.report.object_sql_field_denied" || executor.calls != 0 {
				t.Fatalf("field=%s calls=%d err=%v", fieldKey, executor.calls, err)
			}
		})
	}
}

func TestObjectSQLParametersAreTypedAndFailClosed(t *testing.T) {
	declared := map[string]reportmodel.ReportObjectSQLParameter{
		"text": {Key: "text", Type: "text", Required: true}, "integer": {Key: "integer", Type: "integer", Required: true}, "decimal": {Key: "decimal", Type: "decimal", Required: true},
		"boolean": {Key: "boolean", Type: "boolean", Required: true}, "date": {Key: "date", Type: "date", Required: true}, "datetime": {Key: "datetime", Type: "datetime", Required: true},
	}
	values, err := reportobjectsql.NormalizeDeclaredParameters(declared, map[string]any{"text": "paid", "integer": json.Number("42"), "decimal": json.Number("10.10"), "boolean": true, "date": "2026-08-15", "datetime": "2026-08-15T08:00:00+08:00"})
	if err != nil || values["integer"] != int64(42) || values["decimal"] != "10.1" || values["datetime"] != "2026-08-15T00:00:00Z" {
		t.Fatalf("values=%#v err=%v", values, err)
	}
	for name, raw := range map[string]map[string]any{"unknown": {"unknown": 1}, "missing": {}, "invalid": {"text": "x", "integer": "not-int", "decimal": "1", "boolean": true, "date": "2026-08-15", "datetime": "2026-08-15T00:00:00Z"}} {
		t.Run(name, func(t *testing.T) {
			if _, err := reportobjectsql.NormalizeDeclaredParameters(declared, raw); err == nil || apperror.CodeOf(err) == "" {
				t.Fatalf("raw=%#v err=%v", raw, err)
			}
		})
	}
}
