package validation

import (
	"encoding/json"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

func TestConnectorValidationConditionOutcomes(t *testing.T) {
	operation := func(key string) integrationmodel.ConnectorOperationSchema {
		return integrationmodel.ConnectorOperationSchema{Key: key, Method: "POST", ExecutionMode: "sync", SideEffect: "write", TimeoutDefaultSeconds: 1, TimeoutMaxSeconds: 2}
	}
	connector := integrationmodel.ConnectorSchema{
		Key: "api", Type: "http", Provider: "api", SecretRefs: []string{"token"},
		Config: map[string]any{"required_secret_refs": "token"}, Operations: []integrationmodel.ConnectorOperationSchema{operation("write")},
	}
	if issues := ApplicationSchemaValidateConnectorDefinitionIssues(connector); len(issues) != 0 {
		t.Fatalf("declared secret: %#v", issues)
	}

	connector.Operations = []integrationmodel.ConnectorOperationSchema{operation("write"), operation("undo")}
	connector.Operations[0].CompensationOperation = "undo"
	if issues := ApplicationSchemaValidateConnectorDefinitionIssues(connector); len(issues) != 0 {
		t.Fatalf("known compensation: %#v", issues)
	}
	connector.Operations[0].SideEffect = "reserve"
	connector.Operations[0].IdempotencySupported = true
	connector.Operations[0].CompensationOperation = ""
	if issues := ApplicationSchemaValidateConnectorDefinitionIssues(connector); !hasMetadataIssue(issues, "backend.integration.connector.reserve_contract_incomplete") {
		t.Fatalf("missing compensation: %#v", issues)
	}

	for _, timeout := range []struct{ defaultSeconds, maxSeconds int }{{0, 1}, {1, 0}} {
		connector.Operations = []integrationmodel.ConnectorOperationSchema{operation("write")}
		connector.Operations[0].TimeoutDefaultSeconds = timeout.defaultSeconds
		connector.Operations[0].TimeoutMaxSeconds = timeout.maxSeconds
		if issues := ApplicationSchemaValidateConnectorDefinitionIssues(connector); !hasMetadataIssue(issues, "backend.integration.connector.operation_timeout_invalid") {
			t.Fatalf("timeout=%#v issues=%#v", timeout, issues)
		}
	}
}

func TestDefinitionAndErrorConditionOutcomes(t *testing.T) {
	knownNonAdmin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"record.read"}})
	if _, err := ApplicationSchemaValidateDefinitionRequest(t.Context(), "action", "a", json.RawMessage(`{}`), knownNonAdmin, nil); err == nil {
		t.Fatal("known non-admin must be rejected")
	}
	if err := ApplicationSchemaFirstDefinitionIssueError([]appschemamodel.ApplicationDefinitionValidationIssue{{ErrorCode: "code"}}); err == nil {
		t.Fatal("issue without field must still map to an error")
	}
	result := definitionValidationFailure(appschemamodel.ApplicationDefinitionValidationResult{}, badRequest("custom", "", "ignored"))
	if result.Valid || result.Errors[0].FieldPath != "definition" {
		t.Fatalf("result=%#v", result)
	}
	var appErr *apperror.AppError
	if !errors.As(metadataValidationError(apperror.KindBadRequest, "code", "", "ignored"), &appErr) || len(appErr.Params) != 0 {
		t.Fatalf("error=%#v", appErr)
	}
}

func TestFieldMutationConditionOutcomes(t *testing.T) {
	objects := []definitionmodel.ObjectSchema{{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "legacy", Type: "text"}}}, {Key: "customer"}}
	request := func(field definitionmodel.FieldSchema, objectKey string) appschemamodel.ApplicationDefinitionUpsertRequest {
		payload, err := json.Marshal(field)
		if err != nil {
			t.Fatal(err)
		}
		return appschemamodel.ApplicationDefinitionUpsertRequest{ObjectKey: objectKey, Payload: payload}
	}
	_, err := ApplicationSchemaNormalizeFieldMutation(request(definitionmodel.FieldSchema{Key: "x", Type: "text"}, ""), objects, []string{"text"}, nil, 0)
	assertMetadataValidationCode(t, err, "backend.metadata.field_object_not_found")

	optionalSetNull := definitionmodel.FieldSchema{Key: "customer", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "customer"}, Config: map[string]any{"on_delete": "set_null"}}
	if _, err := ApplicationSchemaNormalizeFieldMutation(request(optionalSetNull, "order"), objects, []string{"relation"}, nil, 0); err != nil {
		t.Fatalf("optional set-null: %v", err)
	}
	required := definitionmodel.FieldSchema{Key: "new", Type: "text", Required: true}
	if _, err := ApplicationSchemaNormalizeFieldMutation(request(required, "order"), objects, []string{"text"}, nil, 0); err != nil {
		t.Fatalf("nil record snapshot: %v", err)
	}

	legacy := definitionmodel.FieldSchema{Key: "legacy", Type: "text", Required: true}
	if _, err := ApplicationSchemaNormalizeFieldMutation(request(legacy, "order"), objects, []string{"text"}, []recordmodel.Record{{Data: map[string]any{"legacy": "present"}}}, 1); err != nil {
		t.Fatalf("populated legacy: %v", err)
	}
	_, err = ApplicationSchemaNormalizeFieldMutation(request(legacy, "order"), objects, []string{"text"}, []recordmodel.Record{{Data: map[string]any{"legacy": ""}}}, 1)
	assertMetadataValidationCode(t, err, "backend.metadata.required_field_default_required")
}

func TestReportValidationConditionOutcomes(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	snapshot := appschemamodel.ApplicationSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{object}}
	report := reportmodel.ReportSchema{
		Key: "r", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "customer", Alias: "customer"}, Dimensions: []reportmodel.ReportDatasetDimension{{Key: "missing", Field: reportmodel.ReportDatasetField{SourceAlias: "customer", FieldKey: ""}}}}, RequiredPermissions: []string{"customer.read"},
	}
	issues := ApplicationSchemaValidateReportDefinitionContract(t.Context(), snapshot, report)
	if !hasMetadataIssue(issues, "backend.report.field_reference_invalid") {
		t.Fatalf("field issues=%#v", issues)
	}
}

func TestReportAndRollbackRuntimeFinalConditionOutcomes(t *testing.T) {
	whitespaceSource := reportmodel.ReportSchema{Key: "r", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: " ", Alias: "source"}}}
	if issues := ApplicationSchemaValidateReportDefinitionContract(t.Context(), appschemamodel.ApplicationSchemaSnapshot{}, whitespaceSource); !hasMetadataIssue(issues, "backend.report.dataset_source_invalid") {
		t.Fatalf("whitespace source issues=%#v", issues)
	}
	validator := reportDefinitionValidator{
		issues: []appschemamodel.ApplicationDefinitionValidationIssue{},
	}
	validator.validateAudienceFieldPermission("dataset.dimensions[0]", "customer", "secret", "export")
	if len(validator.issues) != 0 {
		t.Fatalf("definition validation must defer SDK field policy to execution: %#v", validator.issues)
	}
	if !reportObjectHasField(definitionmodel.ObjectSchema{}, "id") {
		t.Fatal("implicit id field not recognized")
	}

}

func assertMetadataValidationCode(t *testing.T, err error, code string) {
	t.Helper()
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) || appErr.Code != code {
		t.Fatalf("error=%#v want=%s", err, code)
	}
}
