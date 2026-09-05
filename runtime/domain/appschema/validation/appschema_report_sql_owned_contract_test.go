package validation

import (
	"testing"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestReportDefinitionContractBindsSQLToSnapshotJSONMetadata(t *testing.T) {
	snapshot := appschemamodel.ApplicationSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{
		{Key: "sale", Fields: []definitionmodel.FieldSchema{{Key: "store", Type: "text"}}},
		{Key: "payment", Fields: []definitionmodel.FieldSchema{{
			Key: "sale_id", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "sale"}, Config: map[string]any{"cardinality": "many_to_one"},
		}}},
	}}
	report := reportmodel.ReportSchema{Key: "payments", ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{
		SQL: `SELECT s.store AS store, COUNT(DISTINCT p.id) AS payment_count
FROM sale s LEFT JOIN payment p ON p.sale_id = s.id
GROUP BY s.store ORDER BY s.store LIMIT 100`,
	}}
	if issues := ApplicationSchemaValidateReportDefinitionContract(t.Context(), snapshot, report); len(issues) != 0 {
		t.Fatalf("SQL-only structural contract rejected: %#v", issues)
	}

	missing := snapshot
	missing.Objects = missing.Objects[:1]
	issues := ApplicationSchemaValidateReportDefinitionContract(t.Context(), missing, report)
	found := false
	for _, issue := range issues {
		found = found || issue.ErrorCode == "backend.report.source_object_not_found" && issue.FieldPath == "object_sql_v1.sql"
	}
	if !found {
		t.Fatalf("SQL source absent from JSON metadata was not rejected: %#v", issues)
	}
}
