package validation

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestFieldMutationNormalizationEdges(t *testing.T) {
	objects := []definitionmodel.ObjectSchema{{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "owner", Type: "relation", Required: true}, {Key: "optional", Type: "text"}}}, {Key: "customer"}}
	request := func(field definitionmodel.FieldSchema) appschemamodel.ApplicationDefinitionUpsertRequest {
		payload, err := json.Marshal(field)
		if err != nil {
			t.Fatal(err)
		}
		return appschemamodel.ApplicationDefinitionUpsertRequest{ObjectKey: "order", Payload: payload}
	}
	assertCode := func(t *testing.T, req appschemamodel.ApplicationDefinitionUpsertRequest, code string, records []recordmodel.Record) {
		t.Helper()
		_, err := ApplicationSchemaNormalizeFieldMutation(req, objects, []string{"text", "relation"}, records, len(records))
		var appErr *apperror.AppError
		if !errors.As(err, &appErr) || appErr.Code != code {
			t.Fatalf("want %s, got %#v", code, err)
		}
	}
	assertCode(t, appschemamodel.ApplicationDefinitionUpsertRequest{Payload: json.RawMessage(`{`)}, "backend.metadata.field_definition_invalid", nil)
	missingObject := request(definitionmodel.FieldSchema{Key: "x", Type: "text"})
	missingObject.ObjectKey = "missing"
	assertCode(t, missingObject, "backend.metadata.field_object_not_found", nil)
	configuredObject := request(definitionmodel.FieldSchema{Key: "x", Type: "text", Config: map[string]any{"_definition_object_key": "order"}})
	configuredObject.ObjectKey = ""
	if _, err := ApplicationSchemaNormalizeFieldMutation(configuredObject, objects, []string{"text"}, nil, 0); err != nil {
		t.Fatalf("config object key: %v", err)
	}
	noConfigObject := request(definitionmodel.FieldSchema{Key: "x", Type: "text"})
	noConfigObject.ObjectKey = ""
	assertCode(t, noConfigObject, "backend.metadata.field_object_not_found", nil)
	base := definitionmodel.FieldSchema{Key: "owner", Type: "relation", Config: map[string]any{}}
	assertCode(t, request(base), "backend.metadata.relation_target_required", nil)
	base.Config["target"] = "missing"
	assertCode(t, request(base), "backend.metadata.relation_target_not_found", nil)
	base.Config["target"] = "customer"
	base.Config["on_delete"] = "bad"
	assertCode(t, request(base), "backend.metadata.relation_on_delete_invalid", nil)
	base.Config["on_delete"] = "set_null"
	base.Required = true
	assertCode(t, request(base), "backend.metadata.relation_set_null_required", nil)
	base.Required = false
	if _, err := ApplicationSchemaNormalizeFieldMutation(request(base), objects, []string{"relation"}, nil, 0); err != nil {
		t.Fatalf("optional set_null: %v", err)
	}
	base.Required = false
	base.Config["on_delete"] = "restrict"
	base.Config["inverse_name"] = "Bad-name"
	assertCode(t, request(base), "backend.metadata.relation_inverse_name_invalid", nil)

	base.Config = map[string]any{"object_key": "customer", "cardinality": "one_to_one"}
	normalized, err := ApplicationSchemaNormalizeFieldMutation(request(base), objects, []string{"relation"}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var got definitionmodel.FieldSchema
	if err := json.Unmarshal(normalized.Payload, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Unique || got.Config["indexed"] != true || got.Config["on_delete"] != "restrict" || got.Validation.Target != "customer" {
		t.Fatalf("unexpected normalization: %#v", got)
	}
	text := definitionmodel.FieldSchema{Key: "new_required", Type: "text", Required: true}
	if _, err := ApplicationSchemaNormalizeFieldMutation(request(text), objects, []string{"text"}, nil, 0); err != nil {
		t.Fatalf("nil record snapshot: %v", err)
	}
	assertCode(t, request(text), "backend.metadata.required_field_default_required", []recordmodel.Record{{Data: map[string]any{}}})
	text.Default = "x"
	if _, err := ApplicationSchemaNormalizeFieldMutation(request(text), objects, []string{"text"}, []recordmodel.Record{{Data: map[string]any{}}}, 1); err != nil {
		t.Fatal(err)
	}
	if fieldRelationTarget(definitionmodel.FieldSchema{Validation: definitionmodel.FieldValidation{Target: " customer "}}) != "customer" {
		t.Fatal("validation target not preferred")
	}
	if fieldRelationTarget(definitionmodel.FieldSchema{Config: map[string]any{"target_object": "customer"}}) != "customer" {
		t.Fatal("target_object not accepted")
	}
	if cleanFieldValue(nil) != "" || defaultFieldValue(" ", "fallback") != "fallback" {
		t.Fatal("clean/default mismatch")
	}
	existingOptional := definitionmodel.FieldSchema{Key: "optional", Type: "text", Required: true}
	if _, err := ApplicationSchemaNormalizeFieldMutation(request(existingOptional), objects, []string{"text"}, []recordmodel.Record{{Data: map[string]any{"optional": "Ada"}}}, 1); err != nil {
		t.Fatalf("populated existing records: %v", err)
	}
	assertCode(t, request(existingOptional), "backend.metadata.required_field_default_required", []recordmodel.Record{{Data: map[string]any{}}})
	configObject := request(definitionmodel.FieldSchema{Key: "label", Type: "text", Config: map[string]any{"_definition_object_key": "order"}})
	configObject.ObjectKey = ""
	if _, err := ApplicationSchemaNormalizeFieldMutation(configObject, objects, []string{"text"}, nil, 0); err != nil {
		t.Fatalf("config object key: %v", err)
	}
	identityRelation := request(definitionmodel.FieldSchema{Key: "actor", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "identity_user"}})
	if _, err := ApplicationSchemaNormalizeFieldMutation(identityRelation, objects, []string{"relation"}, nil, 0); err != nil {
		t.Fatalf("identity relation: %v", err)
	}
	organizationRelation := request(definitionmodel.FieldSchema{Key: "organization_unit", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "identity_organization_unit"}})
	if _, err := ApplicationSchemaNormalizeFieldMutation(organizationRelation, objects, []string{"relation"}, nil, 0); err != nil {
		t.Fatalf("identity organization unit relation: %v", err)
	}
	many := definitionmodel.FieldSchema{Key: "customer", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "customer"}, Config: map[string]any{"indexed": false, "inverse_name": "orders"}}
	if _, err := ApplicationSchemaNormalizeFieldMutation(request(many), objects, []string{"relation"}, nil, 0); err != nil {
		t.Fatalf("many relation: %v", err)
	}
	defaultValue := definitionmodel.FieldSchema{Key: "required_default_value", Type: "text", Required: true, DefaultValue: "x"}
	if _, err := ApplicationSchemaNormalizeFieldMutation(request(defaultValue), objects, []string{"text"}, []recordmodel.Record{{Data: map[string]any{}}}, 1); err != nil {
		t.Fatalf("default value: %v", err)
	}
	existingRequired := definitionmodel.FieldSchema{Key: "owner", Type: "text", Required: true}
	if _, err := ApplicationSchemaNormalizeFieldMutation(request(existingRequired), objects, []string{"text"}, []recordmodel.Record{{Data: map[string]any{}}}, 1); err != nil {
		t.Fatalf("existing required: %v", err)
	}
	optional := definitionmodel.FieldSchema{Key: "optional", Type: "text"}
	if _, err := ApplicationSchemaNormalizeFieldMutation(request(optional), objects, []string{"text"}, []recordmodel.Record{{Data: map[string]any{}}}, 1); err != nil {
		t.Fatalf("optional: %v", err)
	}
}

func TestDefinitionValidationUtilityEdges(t *testing.T) {
	if ApplicationSchemaFirstDefinitionIssueError(nil) != nil {
		t.Fatal("empty issues should pass")
	}
	if err := ApplicationSchemaFirstDefinitionIssueError([]appschemamodel.ApplicationDefinitionValidationIssue{{ErrorCode: "empty-path"}}); err == nil {
		t.Fatal("empty path issue must fail")
	}
	if err := badRequest("odd", "orphan"); err == nil {
		t.Fatal("bad request expected")
	}
	if err := badRequest("blank-key", " ", "ignored"); err == nil {
		t.Fatal("blank-key bad request expected")
	}
	if ApplicationSchemaNormalizedDefinitionValue(nil) != "" || ApplicationSchemaNormalizedDefinitionValue(" x ") != "x" || ApplicationSchemaNormalizedDefinitionValue((*int)(nil)) != "" {
		t.Fatal("normalization mismatch")
	}
	issue := NewApplicationDefinitionValidationIssue("backend.action.unknown", "", "step", "op", nil)
	if issue.CapabilityKey != "action.definition" {
		t.Fatalf("unexpected capability: %#v", issue)
	}
	for code, want := range map[string]string{
		"backend.metadata.relation_target_not_found":     "validation.target",
		"backend.metadata.relation_cardinality_invalid":  "config.cardinality",
		"backend.metadata.relation_on_delete_invalid":    "config.on_delete",
		"backend.metadata.relation_set_null_required":    "config.on_delete",
		"backend.metadata.relation_inverse_name_invalid": "config.inverse_name",
	} {
		if got := ApplicationDefinitionValidationErrorFieldPath(code, nil); got != want {
			t.Fatalf("%s: %q", code, got)
		}
	}
	for _, code := range []string{
		"backend.integration.connector.operation_unknown",
		"backend.integration.connector.unknown",
	} {
		if got := NewApplicationDefinitionValidationIssue(code, "field", "", "", nil).CapabilityKey; got != "" {
			t.Fatalf("%s capability=%q", code, got)
		}
	}
	if got := ApplicationDefinitionValidationErrorFieldPath("other", map[string]string{"field": " custom "}); got != "custom" {
		t.Fatalf("fallback field path=%q", got)
	}
}

func TestDefinitionValidationRequestEdges(t *testing.T) {
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"runtime.appschema.validate_application_definition"}})
	cases := []struct {
		name, resourceType, resourceKey string
		payload                         json.RawMessage
		validator                       ApplicationDefinitionPayloadValidator
		code                            string
	}{
		{"workflow", "workflow", "flow", json.RawMessage(`{}`), nil, "backend.workflow.lifecycle_api_required"},
		{"unknown", "unknown", "key", json.RawMessage(`{}`), nil, "backend.metadata.resource_type_invalid"},
		{"missing key", "action", "", json.RawMessage(`{}`), nil, "backend.metadata.definition_identity_required"},
		{"missing payload", "action", "key", nil, nil, "backend.metadata.definition_identity_required"},
		{"missing validator", "action", "key", json.RawMessage(`{}`), nil, "backend.metadata.definition_validator_required"},
		{"plain error", "action", "key", json.RawMessage(`{}`), func(context.Context, string, string, json.RawMessage) (json.RawMessage, []appschemamodel.ApplicationDefinitionValidationIssue, error) {
			return nil, nil, errors.New("down")
		}, "backend.internal"},
		{"issues", "action", "key", json.RawMessage(`{}`), func(context.Context, string, string, json.RawMessage) (json.RawMessage, []appschemamodel.ApplicationDefinitionValidationIssue, error) {
			return nil, []appschemamodel.ApplicationDefinitionValidationIssue{{ErrorCode: "owner.issue"}}, nil
		}, "owner.issue"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ApplicationSchemaValidateDefinitionRequest(t.Context(), tc.resourceType, tc.resourceKey, tc.payload, admin, tc.validator)
			if err != nil || result.Valid || len(result.Errors) != 1 || result.Errors[0].ErrorCode != tc.code {
				t.Fatalf("result=%#v err=%v", result, err)
			}
		})
	}
	if businessResourceTypeExists("unknown") || !businessResourceTypeExists("field") || businessResourceTypeExists("identity_profile_binding") || businessResourceTypeExists("scheduler") || businessResourceTypeExists("report") {
		t.Fatal("resource type lookup mismatch")
	}
	result := definitionValidationFailure(appschemamodel.ApplicationDefinitionValidationResult{}, badRequest("custom", "field", ""))
	if result.Errors[0].FieldPath != "definition" {
		t.Fatalf("fallback field=%#v", result.Errors)
	}
}

func TestDictionaryValidationJSONEdges(t *testing.T) {
	cases := []struct{ name, payload, code string }{
		{"json", `{`, "backend.dictionary.definition_invalid"},
		{"empty key", `{"key":""}`, "backend.dictionary.key_mismatch"},
		{"mismatch", `{"key":"other"}`, "backend.dictionary.key_mismatch"},
		{"item key", `{"key":"dict","items":[{"value":"one"}]}`, "backend.dictionary.item_key_value_required"},
		{"item value", `{"key":"dict","items":[{"key":"one"}]}`, "backend.dictionary.item_key_value_required"},
		{"duplicate key", `{"key":"dict","items":[{"key":"A","value":"one","locale":"en"},{"key":" a ","value":"two","locale":"EN"}]}`, "backend.dictionary.item_key_exists"},
		{"duplicate value", `{"key":"dict","items":[{"key":"a","value":"One","locale":"en"},{"key":"b","value":" one ","locale":"EN"}]}`, "backend.dictionary.item_value_exists"},
		{"self", `{"key":"dict","items":[{"key":"a","value":"A","parent_key":"a"}]}`, "backend.dictionary.item_parent_self"},
		{"missing parent", `{"key":"dict","items":[{"key":"a","value":"A","parent_key":"missing"}]}`, "backend.dictionary.item_parent_not_found"},
		{"cycle", `{"key":"dict","items":[{"key":"a","value":"A","parent_key":"b"},{"key":"b","value":"B","parent_key":"a"}]}`, "backend.dictionary.item_parent_cycle"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ApplicationSchemaValidateDictionaryDefinition("dict", json.RawMessage(tc.payload))
			var appErr *apperror.AppError
			if !errors.As(err, &appErr) || appErr.Code != tc.code {
				t.Fatalf("error=%#v want=%s", err, tc.code)
			}
		})
	}
	valid := json.RawMessage(`{"key":"dict","items":[{"key":"root","value":"Root"},{"key":"child","value":"Child","parent_key":"root"},{"key":"child","value":"Enfant","locale":"fr","parent_key":"ignored"}]}`)
	if err := ApplicationSchemaValidateDictionaryDefinition(" dict ", valid); err != nil {
		t.Fatalf("valid dictionary: %v", err)
	}
}

func TestDefinitionRequestFailureEdges(t *testing.T) {
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"runtime.appschema.validate_application_definition"}})
	if result, err := ApplicationSchemaValidateDefinitionRequest(t.Context(), "action", "x", json.RawMessage(`{}`), principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, nil); err != nil || result.Valid || result.Errors[0].ErrorCode != "backend.metadata.definition_validator_required" {
		t.Fatalf("authenticated validation result=%+v error=%v", result, err)
	}
	validator := func(context.Context, string, string, json.RawMessage) (json.RawMessage, []appschemamodel.ApplicationDefinitionValidationIssue, error) {
		return nil, []appschemamodel.ApplicationDefinitionValidationIssue{{ErrorCode: "owned"}}, nil
	}
	for _, tc := range []struct {
		name, resourceType, resourceKey string
		payload                         json.RawMessage
		validate                        ApplicationDefinitionPayloadValidator
		code                            string
	}{
		{"workflow", "workflow", "w", json.RawMessage(`{}`), validator, "backend.workflow.lifecycle_api_required"},
		{"invalid type", "unknown", "x", json.RawMessage(`{}`), validator, "backend.metadata.resource_type_invalid"},
		{"empty key", "action", "", json.RawMessage(`{}`), validator, "backend.metadata.definition_identity_required"},
		{"empty payload", "action", "x", nil, validator, "backend.metadata.definition_identity_required"},
		{"nil validator", "action", "x", json.RawMessage(`{}`), nil, "backend.metadata.definition_validator_required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ApplicationSchemaValidateDefinitionRequest(t.Context(), tc.resourceType, tc.resourceKey, tc.payload, admin, tc.validate)
			if err != nil || result.Valid || len(result.Errors) != 1 || result.Errors[0].ErrorCode != tc.code {
				t.Fatalf("result=%#v err=%v", result, err)
			}
		})
	}
	result, err := ApplicationSchemaValidateDefinitionRequest(t.Context(), "action", "x", json.RawMessage(`{}`), admin, validator)
	if err != nil || len(result.Errors) != 1 || result.Errors[0].ErrorCode != "owned" {
		t.Fatalf("owned issues: %#v %v", result, err)
	}
	result, err = ApplicationSchemaValidateDefinitionRequest(t.Context(), "action", "x", json.RawMessage(`{}`), admin,
		func(context.Context, string, string, json.RawMessage) (json.RawMessage, []appschemamodel.ApplicationDefinitionValidationIssue, error) {
			return nil, nil, errors.New("plain")
		})
	if err != nil || result.Errors[0].ErrorCode != "backend.internal" || result.Errors[0].FieldPath != "definition" {
		t.Fatalf("plain error: %#v %v", result, err)
	}
}

type evidenceReaderFunc func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error)

func (f evidenceReaderFunc) ListRecords(ctx context.Context, workspace string, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	return f(ctx, workspace, object, query)
}

func TestReportValidationEdges(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}, {Key: "email", Type: "text"}}}
	snapshot := appschemamodel.ApplicationSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{object}}
	invalid := reportmodel.ReportSchema{
		Dataset:              reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "missing", Alias: ""}, Joins: []reportmodel.ReportDatasetJoin{{ObjectKey: "customer", Alias: "customer", LeftAlias: "unknown", LeftField: "id", RightField: "id", Type: "outer", Cardinality: "many_to_many"}}, Filters: []reportmodel.ReportDatasetFilter{{Field: reportmodel.ReportDatasetField{SourceAlias: "unknown", FieldKey: "x"}, Operator: "magic"}}, Measures: []reportmodel.ReportDatasetMeasure{{Key: "total", Operation: "sum"}}, Sort: []reportmodel.ReportDatasetSort{{Key: "missing", Direction: "sideways"}}, Limit: 10001},
		RequiredPermissions:  []string{"", "unknown", "unknown"},
		EvidenceRequirements: []reportmodel.ReportEvidenceRequirement{{}, {ObjectKey: "other", MinimumRecords: 1}, {ObjectKey: "customer", MinimumRecords: 0}, {ObjectKey: "customer", MinimumRecords: 1}},
	}
	validPermissionReport := reportmodel.ReportSchema{Key: "permission", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "customer", Alias: "customer"}}, RequiredPermissions: []string{"customer.read"}}
	if issues := ApplicationSchemaValidateReportDefinitionContract(t.Context(), snapshot, validPermissionReport); len(issues) != 0 {
		t.Fatalf("known permission: %#v", issues)
	}
	issues := ApplicationSchemaValidateReportDefinitionContract(t.Context(), snapshot, invalid)
	for _, code := range []string{"backend.report.key_required", "backend.report.dataset_source_invalid", "backend.report.source_object_not_found", "backend.report.permission_invalid", "backend.report.permission_not_found", "backend.report.dataset_alias_invalid", "backend.report.join_invalid", "backend.report.join_cardinality_invalid", "backend.report.field_reference_invalid", "backend.report.filter_invalid", "backend.report.measure_invalid", "backend.report.sort_invalid", "backend.report.limit_invalid", "backend.report.evidence_object_invalid", "backend.report.evidence_source_not_declared", "backend.report.evidence_minimum_invalid"} {
		if !hasMetadataIssue(issues, code) {
			t.Errorf("missing %s in %#v", code, issues)
		}
	}
	missingSources := invalid
	missingSources.Dataset = reportmodel.ReportDatasetSchema{}
	if !hasMetadataIssue(ApplicationSchemaValidateReportDefinitionContract(t.Context(), snapshot, missingSources), "backend.report.dataset_source_invalid") {
		t.Fatal("missing dataset source issue")
	}

	report := reportmodel.ReportSchema{Key: "r", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "customer", Alias: "customer"}, Dimensions: []reportmodel.ReportDatasetDimension{{Key: "name", Field: reportmodel.ReportDatasetField{SourceAlias: "customer", FieldKey: "name"}}, {Key: "email", Field: reportmodel.ReportDatasetField{SourceAlias: "customer", FieldKey: "email"}}}}, EvidenceRequirements: []reportmodel.ReportEvidenceRequirement{{ObjectKey: "customer", MinimumRecords: 2, RequiredNonEmptyFields: []string{"", "missing", "missing"}}}}
	issues = ApplicationSchemaValidateReportDefinitionContract(t.Context(), snapshot, report)
	for _, code := range []string{"backend.report.evidence_field_invalid", "backend.report.evidence_field_not_found"} {
		if !hasMetadataIssue(issues, code) {
			t.Errorf("missing %s in %#v", code, issues)
		}
	}
	fieldCombinationReport := reportmodel.ReportSchema{Key: "field-combinations", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "customer", Alias: "customer"}, Dimensions: []reportmodel.ReportDatasetDimension{{Key: "missing", Field: reportmodel.ReportDatasetField{SourceAlias: "customer", FieldKey: ""}}}}}
	if issues := ApplicationSchemaValidateReportDefinitionContract(t.Context(), snapshot, fieldCombinationReport); !hasMetadataIssue(issues, "backend.report.field_reference_invalid") {
		t.Fatalf("field combinations: %#v", issues)
	}
	report.EvidenceRequirements[0].RequiredNonEmptyFields = []string{"name"}
	reader := evidenceReaderFunc(func(_ context.Context, _ string, _ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		if query.Page == 1 {
			return recordmodel.RecordPageResult{Items: []recordmodel.Record{{Data: map[string]any{"name": ""}}, {Data: map[string]any{"name": "Ada"}}}, HasNext: true}, nil
		}
		return recordmodel.RecordPageResult{Items: []recordmodel.Record{{Data: map[string]any{"name": "Grace"}}}}, nil
	})
	if err := ApplicationSchemaValidateReportDefinition(t.Context(), "ws", snapshot, reader, report); err != nil {
		t.Fatalf("qualified multipage evidence: %v", err)
	}
	errReader := evidenceReaderFunc(func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		return recordmodel.RecordPageResult{}, errors.New("down")
	})
	issues = ApplicationSchemaValidateReportDefinitionIssues(t.Context(), "ws", snapshot, errReader, report)
	if !hasMetadataIssue(issues, "backend.report.evidence_unavailable") {
		t.Fatalf("missing unavailable: %#v", issues)
	}

	validator := reportDefinitionValidator{issues: []appschemamodel.ApplicationDefinitionValidationIssue{}}
	validator.issue("code", "path", nil)
	if validator.issues[0].Params["field"] != "path" {
		t.Fatal("issue nil params")
	}
	if reportObjectHasField(object, "absent") || reportRecordSatisfiesEvidence(recordmodel.Record{Data: map[string]any{"name": ""}}, []string{"name"}) || valueOrDefault(" ", "read") != "read" {
		t.Fatal("report helpers mismatch")
	}
	emptyIssues := ApplicationSchemaValidateReportDefinitionContract(t.Context(), snapshot, reportmodel.ReportSchema{})
	if !hasMetadataIssue(emptyIssues, "backend.report.dataset_source_invalid") {
		t.Fatalf("missing source requirement: %#v", emptyIssues)
	}
	missingObjectEvidence := reportmodel.ReportSchema{Key: "r", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "missing", Alias: "missing"}}, EvidenceRequirements: []reportmodel.ReportEvidenceRequirement{{ObjectKey: "missing", MinimumRecords: 1}}}
	if issues := ApplicationSchemaValidateReportDefinitionContract(t.Context(), snapshot, missingObjectEvidence); !hasMetadataIssue(issues, "backend.report.source_object_not_found") {
		t.Fatalf("missing object issues=%#v", issues)
	}
}

func hasMetadataIssue(issues []appschemamodel.ApplicationDefinitionValidationIssue, code string) bool {
	for _, issue := range issues {
		if issue.ErrorCode == code {
			return true
		}
	}
	return false
}
