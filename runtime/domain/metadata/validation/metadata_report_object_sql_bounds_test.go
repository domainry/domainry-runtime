package validation

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

func objectSQLBoundsSnapshot() metadatamodel.ApplicationSchemaSnapshot {
	return metadatamodel.ApplicationSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{{
		Key: "repair_order",
		Fields: []definitionmodel.FieldSchema{
			{Key: "status", Type: "select"},
			{Key: "amount", Type: "currency", Config: map[string]any{"precision": 19, "scale": 2}},
		},
	}}}
}

func objectSQLBoundsReport(sql string, columns ...reportmodel.ReportResultColumnSchema) reportmodel.ReportSchema {
	return reportmodel.ReportSchema{Key: "repair_order_detail", ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{
		SQL: sql, SourceObjects: []string{"repair_order"}, ResultSchema: columns,
	}}
}

func objectSQLBoundsIssueParams(issues []metadatamodel.MetadataDefinitionValidationIssue, code string) (map[string]string, bool) {
	for _, issue := range issues {
		if issue.ErrorCode == code {
			return issue.Params, true
		}
	}
	return nil, false
}

// Regression for the empirical M2 pit IF-3 (run m2-fieldservice-95bda9c73eaf-01,
// evolution round 3): an object_sql detail report without an authored ORDER BY
// and literal LIMIT compiled and validated, then the Runtime applied the
// implicit 1000-row default - exactly the sync/async export threshold - so the
// governed export silently truncated and cost a full model evolution loop.
// The definition contract must reject the SQL and name the missing clause.
func TestObjectSQLContractRequiresExplicitOrderByAndLimit(t *testing.T) {
	report := objectSQLBoundsReport(
		`SELECT r.status AS status FROM repair_order r`,
		reportmodel.ReportResultColumnSchema{Key: "status", Type: "text", Kind: "dimension"},
	)
	issues := MetadataValidateReportDefinitionContract(t.Context(), objectSQLBoundsSnapshot(), report)
	orderParams, orderFound := objectSQLBoundsIssueParams(issues, "backend.report.object_sql_order_by_required")
	if !orderFound {
		t.Fatalf("missing ORDER BY must be rejected, issues=%#v", issues)
	}
	if orderParams["missing_clause"] != "ORDER BY" || orderParams["field"] != "object_sql_v1.sql.order_by" {
		t.Fatalf("ORDER BY issue must name the missing clause and field, got %#v", orderParams)
	}
	limitParams, limitFound := objectSQLBoundsIssueParams(issues, "backend.report.object_sql_limit_required")
	if !limitFound {
		t.Fatalf("missing LIMIT must be rejected, issues=%#v", issues)
	}
	if limitParams["missing_clause"] != "LIMIT" || limitParams["default_limit"] != "1000" || limitParams["field"] != "object_sql_v1.sql.limit" {
		t.Fatalf("LIMIT issue must name the missing clause and the implicit default, got %#v", limitParams)
	}
}

func TestObjectSQLContractAcceptsExplicitOrderByAndLimit(t *testing.T) {
	report := objectSQLBoundsReport(
		`SELECT r.status AS status FROM repair_order r ORDER BY r.id LIMIT 5000`,
		reportmodel.ReportResultColumnSchema{Key: "status", Type: "text", Kind: "dimension"},
	)
	if issues := MetadataValidateReportDefinitionContract(t.Context(), objectSQLBoundsSnapshot(), report); len(issues) != 0 {
		t.Fatalf("explicit ORDER BY + literal LIMIT must validate, issues=%#v", issues)
	}
}

func TestObjectSQLContractGroupedPlanNeedsOnlyExplicitLimit(t *testing.T) {
	grouped := objectSQLBoundsReport(
		`SELECT r.status AS status, SUM(r.amount) AS revenue FROM repair_order r GROUP BY r.status LIMIT 100`,
		reportmodel.ReportResultColumnSchema{Key: "status", Type: "text", Kind: "dimension"},
		reportmodel.ReportResultColumnSchema{Key: "revenue", Type: "currency", Kind: "measure", Precision: 19, Scale: 2},
	)
	if issues := MetadataValidateReportDefinitionContract(t.Context(), objectSQLBoundsSnapshot(), grouped); len(issues) != 0 {
		t.Fatalf("grouped plan with literal LIMIT orders deterministically by its grouping terms, issues=%#v", issues)
	}
	truncating := objectSQLBoundsReport(
		`SELECT r.status AS status, SUM(r.amount) AS revenue FROM repair_order r GROUP BY r.status`,
		reportmodel.ReportResultColumnSchema{Key: "status", Type: "text", Kind: "dimension"},
		reportmodel.ReportResultColumnSchema{Key: "revenue", Type: "currency", Kind: "measure", Precision: 19, Scale: 2},
	)
	issues := MetadataValidateReportDefinitionContract(t.Context(), objectSQLBoundsSnapshot(), truncating)
	if _, found := objectSQLBoundsIssueParams(issues, "backend.report.object_sql_limit_required"); !found {
		t.Fatalf("grouped multi-row plan without literal LIMIT still truncates and must be rejected, issues=%#v", issues)
	}
	if _, found := objectSQLBoundsIssueParams(issues, "backend.report.object_sql_order_by_required"); found {
		t.Fatalf("grouped plan must not require an authored ORDER BY, issues=%#v", issues)
	}
}

func TestObjectSQLContractSingleRowAggregateNeedsNoBounds(t *testing.T) {
	report := objectSQLBoundsReport(
		`SELECT SUM(r.amount) AS revenue FROM repair_order r`,
		reportmodel.ReportResultColumnSchema{Key: "revenue", Type: "currency", Kind: "measure", Precision: 19, Scale: 2},
	)
	if issues := MetadataValidateReportDefinitionContract(t.Context(), objectSQLBoundsSnapshot(), report); len(issues) != 0 {
		t.Fatalf("single-row aggregate plans need no authored ORDER BY or LIMIT, issues=%#v", issues)
	}
}

// The runtime write path keeps accepting already-published definitions: the
// explicit-bounds requirement is a definition-contract (validate-time) rule.
func TestObjectSQLRuntimeIssuesPathDoesNotRequireExplicitBounds(t *testing.T) {
	report := objectSQLBoundsReport(
		`SELECT r.status AS status FROM repair_order r`,
		reportmodel.ReportResultColumnSchema{Key: "status", Type: "text", Kind: "dimension"},
	)
	issues := MetadataValidateReportDefinitionIssues(t.Context(), "workspace-a", objectSQLBoundsSnapshot(), reportRecordRepositoryStub{}, report)
	for _, issue := range issues {
		if issue.ErrorCode == "backend.report.object_sql_order_by_required" || issue.ErrorCode == "backend.report.object_sql_limit_required" {
			t.Fatalf("runtime issues path must not enforce the validate-time explicit-bounds contract, issues=%#v", issues)
		}
	}
}
