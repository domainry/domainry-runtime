package validation

import (
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"

	"context"
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

type reportRecordRepositoryStub struct {
	items []recordmodel.Record
}

func (s reportRecordRepositoryStub) ListRecords(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	return recordmodel.RecordPageResult{Items: s.items}, nil
}

func (reportRecordRepositoryStub) GetRecord(context.Context, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
	return recordmodel.Record{}, false, nil
}

func (reportRecordRepositoryStub) InsertRecord(context.Context, definitionmodel.ObjectSchema, recordmodel.Record) error {
	return nil
}

func (reportRecordRepositoryStub) UpdateRecord(context.Context, definitionmodel.ObjectSchema, recordmodel.Record) error {
	return nil
}

func (reportRecordRepositoryStub) UpdateRecordWhere(context.Context, definitionmodel.ObjectSchema, recordmodel.Record, map[string]any) (bool, error) {
	return false, nil
}

func (reportRecordRepositoryStub) DeleteRecord(context.Context, definitionmodel.ObjectSchema, string) error {
	return nil
}

func (reportRecordRepositoryStub) CommitRecordMutation(context.Context, transactionmodel.RecordMutationCommit) error {
	return nil
}

func (reportRecordRepositoryStub) CommitRecordMutationBatch(context.Context, []transactionmodel.RecordMutationCommit) error {
	return nil
}

func (reportRecordRepositoryStub) UniqueExists(context.Context, string, string, string, any) (bool, error) {
	return false, nil
}

func TestReportContractOmitsRuntimeEvidenceAvailability(t *testing.T) {
	snapshot, report := reportValidationFixture()
	if issues := MetadataValidateReportDefinitionContract(t.Context(), snapshot, report); len(issues) != 0 {
		t.Fatalf("authoring contract must not require live records: %#v", issues)
	}
	issues := MetadataValidateReportDefinitionIssues(t.Context(), "workspace-a", snapshot, reportRecordRepositoryStub{}, report)
	if len(issues) != 1 || issues[0].ErrorCode != "backend.report.evidence_insufficient" || issues[0].FieldPath != "evidence_requirements[0]" {
		t.Fatalf("unexpected runtime evidence issues: %#v", issues)
	}
}

func TestReportValidationAcceptsQualifiedRuntimeEvidence(t *testing.T) {
	snapshot, report := reportValidationFixture()
	repository := reportRecordRepositoryStub{items: []recordmodel.Record{{ID: "customer-1", Data: map[string]any{"name": "Ada"}}}}
	if issues := MetadataValidateReportDefinitionIssues(t.Context(), "workspace-a", snapshot, repository, report); len(issues) != 0 {
		t.Fatalf("unexpected issues: %#v", issues)
	}
}

func TestFirstDefinitionIssueErrorPreservesField(t *testing.T) {
	err := MetadataFirstDefinitionIssueError([]metadatamodel.MetadataDefinitionValidationIssue{{ErrorCode: "backend.report.key_required", FieldPath: "key"}})
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) || appErr.Code != "backend.report.key_required" || appErr.Params["field"] != "key" {
		t.Fatalf("unexpected structured error: %#v", err)
	}
}

func TestReportValidationRejectsOneToManyMeasureAmplification(t *testing.T) {
	snapshot := metadatamodel.MetadataSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{
		{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "paid_amount", Type: "currency"}}},
		{Key: "order_line", Fields: []definitionmodel.FieldSchema{{Key: "order_id", Type: "relation"}, {Key: "amount", Type: "currency"}}},
	}}
	report := reportmodel.ReportSchema{Key: "unsafe", Dataset: reportmodel.ReportDatasetSchema{
		Source:   reportmodel.ReportDatasetSource{ObjectKey: "order", Alias: "orders"},
		Joins:    []reportmodel.ReportDatasetJoin{{ObjectKey: "order_line", Alias: "lines", Type: "inner", LeftAlias: "orders", LeftField: "id", RightField: "order_id", Cardinality: "one_to_many"}},
		Measures: []reportmodel.ReportDatasetMeasure{{Key: "paid", Operation: "sum", Field: &reportmodel.ReportDatasetField{SourceAlias: "orders", FieldKey: "paid_amount"}}},
	}}
	issues := MetadataValidateReportDefinitionContract(t.Context(), snapshot, report)
	found := false
	for _, issue := range issues {
		found = found || issue.ErrorCode == "backend.report.join_measure_amplification"
	}
	if !found {
		t.Fatalf("issues=%#v", issues)
	}
}

func TestReportValidationPublishesObjectSQLAndRejectsUnsafeSelection(t *testing.T) {
	snapshot := metadatamodel.MetadataSchemaSnapshot{
		Objects: []definitionmodel.ObjectSchema{{Key: "sale", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "select"}, {Key: "amount", Type: "currency", Config: map[string]any{"precision": 19, "scale": 2}}}}},
	}
	report := reportmodel.ReportSchema{Key: "sales.sql", RequiredPermissions: []string{"sale.read"}, ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{
		SQL: `SELECT s.status AS status, SUM(s.amount) AS revenue FROM sale s GROUP BY s.status LIMIT 20`, SourceObjects: []string{"sale"},
		ResultSchema: []reportmodel.ReportResultColumnSchema{{Key: "status", Type: "text", Kind: "dimension"}, {Key: "revenue", Type: "currency", Kind: "measure", Precision: 19, Scale: 2}},
	}}
	if issues := MetadataValidateReportDefinitionContract(t.Context(), snapshot, report); len(issues) != 0 {
		t.Fatalf("valid object SQL rejected: %#v", issues)
	}
	report.ObjectSQLV1.SQL = `SELECT * FROM sale s`
	report.ObjectSQLV1.ResultSchema = []reportmodel.ReportResultColumnSchema{{Key: "all", Type: "text", Kind: "dimension"}}
	if issues := MetadataValidateReportDefinitionContract(t.Context(), snapshot, report); !hasMetadataIssue(issues, "backend.report.object_sql_star_forbidden") {
		t.Fatalf("forbidden SQL issues=%#v", issues)
	}
}

func TestReportValidationBlocksUnindexedDatasetAccessPath(t *testing.T) {
	snapshot := metadatamodel.MetadataSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{{Key: "event", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "select"}}}}}
	report := reportmodel.ReportSchema{Key: "event.summary", Dataset: reportmodel.ReportDatasetSchema{
		Source:  reportmodel.ReportDatasetSource{ObjectKey: "event", Alias: "events"},
		Filters: []reportmodel.ReportDatasetFilter{{Field: reportmodel.ReportDatasetField{SourceAlias: "events", FieldKey: "status"}, Operator: "eq", Value: "open"}},
	}}
	issues := MetadataValidateReportDefinitionContract(t.Context(), snapshot, report)
	if len(issues) != 1 || issues[0].ErrorCode != "backend.report.required_index_missing" || issues[0].FieldPath != "dataset.filters[0].field" || issues[0].Params["recommended_index"] != "workspace_id,status" {
		t.Fatalf("issues=%#v", issues)
	}
	snapshot.Objects[0].Fields[0].Config = map[string]any{"indexed": true}
	if issues := MetadataValidateReportDefinitionContract(t.Context(), snapshot, report); len(issues) != 0 {
		t.Fatalf("indexed report rejected: %#v", issues)
	}
}

func TestReportValidationAcceptsClosedExportScopeAndRejectsUnsafeFragments(t *testing.T) {
	snapshot := metadatamodel.MetadataSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{
		{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "order_no", Type: "text"}}},
		{Key: "tag_assignment", Fields: []definitionmodel.FieldSchema{{Key: "target_id", Type: "text"}, {Key: "tag_definition_id", Type: "text"}, {Key: "active", Type: "boolean"}}},
		{Key: "tag_definition", Fields: []definitionmodel.FieldSchema{{Key: "stable_key", Type: "text"}}},
	}}
	report := reportmodel.ReportSchema{Key: "orders", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "order", Alias: "orders"}}, ExportScope: &reportmodel.ReportExportScopeSchema{
		Query: &reportmodel.ReportExportQueryScopeSchema{Mode: "any", Predicates: []reportmodel.ReportExportQueryPredicate{{Field: reportmodel.ReportDatasetField{SourceAlias: "orders", FieldKey: "order_no"}, Operator: "contains"}}},
		Tags: &reportmodel.ReportExportTagScopeSchema{
			Join:        reportmodel.ReportDatasetJoin{Alias: "export_tags", ObjectKey: "tag_assignment", Type: "inner", LeftAlias: "orders", LeftField: "id", RightField: "target_id", Cardinality: "one_to_many"},
			FamilyJoin:  &reportmodel.ReportDatasetJoin{Alias: "export_tag_definitions", ObjectKey: "tag_definition", Type: "inner", LeftAlias: "export_tags", LeftField: "tag_definition_id", RightField: "id", Cardinality: "many_to_one"},
			TargetField: reportmodel.ReportDatasetField{SourceAlias: "orders", FieldKey: "id"}, TagField: reportmodel.ReportDatasetField{SourceAlias: "export_tag_definitions", FieldKey: "stable_key"}, AllowedMatchModes: []string{"any", "all"}, DefaultMatchMode: "all",
			FixedFilters: []reportmodel.ReportDatasetFilter{{Field: reportmodel.ReportDatasetField{SourceAlias: "export_tags", FieldKey: "active"}, Operator: "eq", Value: true}},
		},
	}}
	if issues := MetadataValidateReportDefinitionContract(t.Context(), snapshot, report); len(issues) != 0 {
		t.Fatalf("valid export scope issues=%#v", issues)
	}
	report.ExportScope.Query.Predicates[0].Operator = "sql"
	report.ExportScope.Tags.Join.Type = "cross"
	report.ExportScope.Tags.FixedFilters[0].Operator = "expression"
	issues := MetadataValidateReportDefinitionContract(t.Context(), snapshot, report)
	if !hasMetadataIssue(issues, "backend.report.export_query_definition_invalid") || !hasMetadataIssue(issues, "backend.report.export_tag_definition_invalid") {
		t.Fatalf("unsafe export scope issues=%#v", issues)
	}
}

func reportValidationFixture() (metadatamodel.MetadataSchemaSnapshot, reportmodel.ReportSchema) {
	return metadatamodel.MetadataSchemaSnapshot{
			Objects: []definitionmodel.ObjectSchema{{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}},
		}, reportmodel.ReportSchema{
			Key: "customer.summary", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "customer", Alias: "customer"}, Dimensions: []reportmodel.ReportDatasetDimension{{Key: "name", Field: reportmodel.ReportDatasetField{SourceAlias: "customer", FieldKey: "name"}}}},
			EvidenceRequirements: []reportmodel.ReportEvidenceRequirement{{ObjectKey: "customer", MinimumRecords: 1, RequiredNonEmptyFields: []string{"name"}}},
		}
}
