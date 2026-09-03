package record

import accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestRecordExportProjectionFailureIsReturned(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	failure := errors.New("projection failed")
	service := recordExportEdgeService(object, &exportEdgeRepository{list: func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		return recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "customer-1", Data: map[string]any{"name": "Acme"}}}}, nil
	}})
	service.dependencies.ProjectRecords = func(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, []recordmodel.Record, string) ([]recordmodel.Record, error) {
		return nil, failure
	}
	if _, _, err := exportRecordDirectForTest(t.Context(), service, object.Key, recordExportPrincipal(object.Key), RecordExportOptions{}); !errors.Is(err, failure) {
		t.Fatalf("err=%v", err)
	}
}

func TestRecordExportAssurancePreverifiedAndValidatorFailures(t *testing.T) {
	object := definitionmodel.ObjectSchema{
		Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}, ExportAssurancePolicy: &definitionmodel.ActionAssurancePolicy{},
	}
	repository := &exportEdgeRepository{list: func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		return recordmodel.RecordPageResult{}, nil
	}}
	service := recordExportEdgeService(object, repository)
	principal := recordExportPrincipal(object.Key)
	if _, _, _, err := service.prepareExport(t.Context(), object.Key, principal, RecordExportOptions{}, nil, true); apperror.CodeOf(err) != "backend.export.assurance_required" {
		t.Fatalf("preverified err=%v", err)
	}
	failure := errors.New("assurance denied")
	service.dependencies.ValidateAssurance = func(context.Context, definitionmodel.ObjectSchema, principalmodel.Principal, map[string]any, string) (map[string]string, error) {
		return nil, failure
	}
	if _, _, err := exportRecordDirectForTest(t.Context(), service, object.Key, principal, RecordExportOptions{}); !errors.Is(err, failure) {
		t.Fatalf("validator err=%v", err)
	}
}

func TestRecordExportCurrencyConfigurationFailure(t *testing.T) {
	field := definitionmodel.FieldSchema{Key: "amount", Type: "currency", Config: map[string]any{"scale": -1}}
	if _, err := recordExportFieldValue(accessfixture.Attach(principalmodel.Principal{}, accessfixture.Bundle{}), "invoice", field, "1.00"); apperror.CodeOf(err) != "backend.export.decimal_config_invalid" {
		t.Fatalf("err=%v", err)
	}
}

func TestRecordExportScopedProjectedMissingAndNilFields(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{
		{Key: "missing", Type: "text"}, {Key: "nil_value", Type: "text"},
		{Key: "account_id", Type: "relation", Config: map[string]any{"object_key": "account"}},
	}}
	repository := &exportEdgeRepository{list: func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		return recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "customer-1", Data: map[string]any{"nil_value": "original", "account_id": "account-1"}}}}, nil
	}}
	service := recordExportEdgeService(object, repository)
	service.dependencies.NormalizeQuery = func(definitionmodel.ObjectSchema, recordmodel.RecordListQuery, principalmodel.Principal) recordmodel.RecordListQuery {
		expression := &recordmodel.RecordScopeExpression{}
		return recordmodel.RecordListQuery{ScopeExpression: expression}
	}
	service.dependencies.ProjectRecords = func(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, []recordmodel.Record, string) ([]recordmodel.Record, error) {
		return []recordmodel.Record{{ID: "customer-1", Data: map[string]any{"nil_value": nil}}}, nil
	}
	content, _, err := exportRecordDirectForTest(t.Context(), service, object.Key, recordExportPrincipal(object.Key), RecordExportOptions{})
	if err != nil || !strings.Contains(string(content), "customer-1") {
		t.Fatalf("content=%q err=%v", content, err)
	}
}

func TestRecordExportFieldValueMaskedBoundary(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{}, accessfixture.Bundle{FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: "invoice", FieldKey: "amount", Export: true, Masked: true}}})
	if value, err := recordExportFieldValue(principal, "invoice", definitionmodel.FieldSchema{Key: "amount", Type: "currency"}, "12.00"); err != nil || value == "12.00" {
		t.Fatalf("value=%q err=%v", value, err)
	}
}
