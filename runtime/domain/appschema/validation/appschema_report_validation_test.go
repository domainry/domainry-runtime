package validation

import (
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"

	"context"
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
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
	if issues := ApplicationSchemaValidateReportDefinitionContract(t.Context(), snapshot, report); len(issues) != 0 {
		t.Fatalf("authoring contract must not require live records: %#v", issues)
	}
	issues := ApplicationSchemaValidateReportDefinitionIssues(t.Context(), "workspace-a", snapshot, reportRecordRepositoryStub{}, report)
	if len(issues) != 1 || issues[0].ErrorCode != "backend.report.evidence_insufficient" || issues[0].FieldPath != "evidence_requirements[0]" {
		t.Fatalf("unexpected runtime evidence issues: %#v", issues)
	}
}

func TestReportValidationAcceptsQualifiedRuntimeEvidence(t *testing.T) {
	snapshot, report := reportValidationFixture()
	repository := reportRecordRepositoryStub{items: []recordmodel.Record{{ID: "customer-1", Data: map[string]any{"name": "Ada"}}}}
	if issues := ApplicationSchemaValidateReportDefinitionIssues(t.Context(), "workspace-a", snapshot, repository, report); len(issues) != 0 {
		t.Fatalf("unexpected issues: %#v", issues)
	}
}

func TestFirstDefinitionIssueErrorPreservesField(t *testing.T) {
	err := ApplicationSchemaFirstDefinitionIssueError([]appschemamodel.ApplicationDefinitionValidationIssue{{ErrorCode: "backend.report.key_required", FieldPath: "key"}})
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) || appErr.Code != "backend.report.key_required" || appErr.Params["field"] != "key" {
		t.Fatalf("unexpected structured error: %#v", err)
	}
}

func TestReportValidationPublishesObjectSQLAndRejectsUnsafeSelection(t *testing.T) {
	snapshot := appschemamodel.ApplicationSchemaSnapshot{
		Objects: []definitionmodel.ObjectSchema{{Key: "sale", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "select"}, {Key: "amount", Type: "currency", Config: map[string]any{"precision": 19, "scale": 2}}}}},
	}
	report := reportmodel.ReportSchema{Key: "sales.sql", RequiredPermissions: []string{"sale.read"}, ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{
		SQL: `SELECT s.status AS status, SUM(s.amount) AS revenue FROM sale s GROUP BY s.status LIMIT 20`, SourceObjects: []string{"sale"},
		ResultSchema: []reportmodel.ReportResultColumnSchema{{Key: "status", Type: "text", Kind: "dimension"}, {Key: "revenue", Type: "currency", Kind: "measure", Precision: 19, Scale: 2}},
	}}
	if issues := ApplicationSchemaValidateReportDefinitionContract(t.Context(), snapshot, report); len(issues) != 0 {
		t.Fatalf("valid object SQL rejected: %#v", issues)
	}
	report.ObjectSQLV1.SQL = `SELECT * FROM sale s`
	report.ObjectSQLV1.ResultSchema = []reportmodel.ReportResultColumnSchema{{Key: "all", Type: "text", Kind: "dimension"}}
	if issues := ApplicationSchemaValidateReportDefinitionContract(t.Context(), snapshot, report); !hasMetadataIssue(issues, "backend.report.object_sql_star_forbidden") {
		t.Fatalf("forbidden SQL issues=%#v", issues)
	}
}

func reportValidationFixture() (appschemamodel.ApplicationSchemaSnapshot, reportmodel.ReportSchema) {
	return appschemamodel.ApplicationSchemaSnapshot{
			Objects: []definitionmodel.ObjectSchema{{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}},
		}, reportmodel.ReportSchema{
			Key: "customer.summary", ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{SQL: "SELECT customer.name AS name FROM customer customer ORDER BY customer.name LIMIT 100", SourceObjects: []string{"customer"}, ResultSchema: []reportmodel.ReportResultColumnSchema{{Key: "name", Type: "text", Kind: "dimension"}}},
			EvidenceRequirements: []reportmodel.ReportEvidenceRequirement{{ObjectKey: "customer", MinimumRecords: 1, RequiredNonEmptyFields: []string{"name"}}},
		}
}
