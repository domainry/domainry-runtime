package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestStructuredMetadataDefinitionAndValidationDelegates(t *testing.T) {
	for _, resourceType := range []string{"action", "connector"} {
		issues, handled := ValidateStructuredMetadataDefinition(resourceType, json.RawMessage(`{`))
		if !handled || len(issues) != 1 {
			t.Fatalf("type=%s issues=%v handled=%v", resourceType, issues, handled)
		}
		issues, handled = ValidateStructuredMetadataDefinition(resourceType, json.RawMessage(`{}`))
		if !handled || len(issues) == 0 {
			t.Fatalf("type=%s issues=%v handled=%v", resourceType, issues, handled)
		}
	}
	if issues, handled := ValidateStructuredMetadataDefinition("object", json.RawMessage(`{}`)); handled || issues != nil {
		t.Fatalf("issues=%v handled=%v", issues, handled)
	}
	if err := validateBusinessActionDefinition(definitionmodel.ActionSchema{}); err == nil {
		t.Fatal("empty action unexpectedly valid")
	}
	if len(validateBusinessActionDefinitionIssues(definitionmodel.ActionSchema{})) == 0 {
		t.Fatal("empty action produced no issues")
	}
	issue := newMetadataDefinitionValidationIssue("code", "field", "step", "operation", map[string]string{"key": "value"})
	if issue.ErrorCode != "code" || firstMetadataDefinitionIssueError([]metadatamodel.MetadataDefinitionValidationIssue{issue}) == nil {
		t.Fatalf("issue=%+v", issue)
	}
	if len(stringSet([]string{" a ", "a", "b"})) != 3 || normalizedDefinitionValue(" value ") != "value" {
		t.Fatal("normalization delegates mismatch")
	}
	if err := validateAutomationConditionGroup(automationmodel.AutomationConditionGroup{}, "conditions", 0); err != nil {
		t.Fatal(err)
	}
	_ = validateAutomationTriggerFilters(automationmodel.AutomationTriggerSchema{}, definitionmodel.ObjectSchema{})
	connector := integrationmodel.ConnectorSchema{Operations: []integrationmodel.ConnectorOperationSchema{{Key: "send"}}}
	if automationConnectorOperation(connector, "send") == nil || automationConnectorOperation(connector, "missing") != nil {
		t.Fatal("connector operation lookup mismatch")
	}
	operation := integrationmodel.ConnectorOperationSchema{Key: "send"}
	if err := validateAutomationOperationInput(operation, nil, definitionmodel.ObjectSchema{}, nil); err != nil {
		t.Fatal(err)
	}
	if valueType, ok := automationMappingValueType("literal", definitionmodel.ObjectSchema{}, nil); !ok || valueType == "" {
		t.Fatalf("valueType=%q ok=%v", valueType, ok)
	}
	if !automationProtocolTypesCompatible("string", "text") || automationProtocolTypesCompatible("object", "number") {
		t.Fatal("protocol type compatibility mismatch")
	}
	if err := validateAutomationInstructionReferences(automationmodel.AutomationInstructionSchema{}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestMetadataApplicationErrorBoundaries(t *testing.T) {
	if err := metadataAuthorizeWorkspaceQuery(""); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("workspace error=%v", err)
	}
	if err := metadataAuthorizeWorkspaceQuery("workspace-1"); err != nil {
		t.Fatal(err)
	}
	if apperror.KindOf(badRequest("bad", "field", "value")) != apperror.KindBadRequest || apperror.KindOf(forbidden("forbidden")) != apperror.KindForbidden || apperror.KindOf(notFound("missing")) != apperror.KindNotFound {
		t.Fatal("metadata error kinds mismatch")
	}
	if apperror.KindOf(metadataInternalError("operation")) != apperror.KindInternal || apperror.KindOf(metadataInternalErrorWithCause("operation", errors.New("cause"))) != apperror.KindInternal {
		t.Fatal("internal error kinds mismatch")
	}
	if wrapMetadataError(nil) != nil {
		t.Fatal("nil error was wrapped")
	}
	appErr := &apperror.AppError{Kind: apperror.KindBadRequest, Code: "existing"}
	if wrapMetadataError(appErr) != appErr {
		t.Fatal("application error was wrapped")
	}
	conflictErr := &metadatamodel.MetadataDefinitionConflictError{ResourceType: "object", ResourceKey: "order", ExpectedHash: "old", CurrentHash: "new"}
	if err := wrapMetadataError(conflictErr); apperror.CodeOf(err) != "backend.metadata.definition_version_conflict" {
		t.Fatalf("conflict error=%v", err)
	}
	schemaMismatch := &metadatamodel.MetadataPhysicalSchemaMismatchError{ObjectKey: "invoice", ColumnKey: "amount", ExpectedType: "TEXT", ActualType: "REAL"}
	if err := wrapMetadataError(schemaMismatch); apperror.KindOf(err) != apperror.KindConflict || apperror.CodeOf(err) != "backend.metadata.physical_schema_incompatible" || apperror.ParamsOf(err)["column_key"] != "amount" {
		t.Fatalf("schema mismatch error=%v", err)
	}
	if err := wrapMetadataError(errors.New("store")); apperror.KindOf(err) != apperror.KindInternal {
		t.Fatalf("store error=%v", err)
	}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-1"}}
	if metadataAuthorizeQuery(principal) == nil || metadataAuthorizeCommand(principal) == nil {
		t.Fatal("unknown principals were authorized")
	}
}

func TestMetadataNormalizeFieldMutationReadsAllRecordPages(t *testing.T) {
	objects := []definitionmodel.ObjectSchema{{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "legacy", Type: "text"}}}}
	payload, err := json.Marshal(definitionmodel.FieldSchema{Key: "legacy", Type: "text", Required: true})
	if err != nil {
		t.Fatal(err)
	}
	request := metadatamodel.MetadataDefinitionUpsertRequest{ObjectKey: "order", Payload: payload}
	calls := 0
	normalized, err := MetadataNormalizeFieldMutation(t.Context(), request, objects, []string{"text"}, func(_ context.Context, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		calls++
		if object.Key != "order" || query.Page != 1 || query.PageSize != 200 || !query.SkipTotal || query.Sort[0].Field != "id" {
			t.Fatalf("object=%+v query=%+v", object, query)
		}
		if calls == 2 && query.AfterID == "" {
			t.Fatalf("second page missing keyset cursor: %+v", query)
		}
		return recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: fmt.Sprintf("record-%d", calls), Data: map[string]any{"legacy": "present"}}}, Total: 2, HasNext: calls == 1}, nil
	})
	if err != nil || calls != 2 || len(normalized.Payload) == 0 {
		t.Fatalf("calls=%d normalized=%+v err=%v", calls, normalized, err)
	}
	edgeErr := errors.New("records failed")
	if _, err := MetadataNormalizeFieldMutation(t.Context(), request, objects, []string{"text"}, func(context.Context, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		return recordmodel.RecordPageResult{}, edgeErr
	}); !errors.Is(err, edgeErr) {
		t.Fatalf("record error=%v", err)
	}
	if _, err := MetadataNormalizeFieldMutation(t.Context(), metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{`)}, objects, []string{"text"}, nil); err == nil {
		t.Fatal("malformed field unexpectedly valid")
	}
	optionalPayload, _ := json.Marshal(definitionmodel.FieldSchema{Key: "optional", Type: "text"})
	if _, err := MetadataNormalizeFieldMutation(t.Context(), metadatamodel.MetadataDefinitionUpsertRequest{ObjectKey: "order", Payload: optionalPayload}, objects, []string{"text"}, func(context.Context, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		t.Fatal("optional field read records")
		return recordmodel.RecordPageResult{}, nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMetadataDefinitionValidationApplicationPaths(t *testing.T) {
	service := NewMetadataApplicationService(MetadataApplicationDependencies{Runtime: localizedLifecycleRuntimeStub{snapshot: metadatamodel.MetadataSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "number"}}}}}}})
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	if _, err := service.ValidateMetadataDefinition(t.Context(), "action", "action-1", metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{}`)}, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization error=%v", err)
	}
	result, err := service.ValidateMetadataDefinition(t.Context(), "action", "action-1", metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{}`)}, admin)
	if err != nil || result.Valid || len(result.Errors) == 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for _, resourceType := range []string{"action", "connector"} {
		normalized, issues, err := service.ValidateMetadataDefinitionRequestPayload(t.Context(), resourceType, "key", metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{}`)})
		if err != nil || normalized != nil || len(issues) == 0 {
			t.Fatalf("type=%s normalized=%s issues=%v err=%v", resourceType, normalized, issues, err)
		}
	}
	if _, _, err := service.ValidateMetadataDefinitionRequestPayload(t.Context(), "dictionary", "key", metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{`)}); err == nil {
		t.Fatal("malformed dictionary unexpectedly valid")
	}
	if _, _, err := service.ValidateMetadataDefinitionRequestPayload(t.Context(), "report", "key", metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{`)}); apperror.CodeOf(err) != "backend.report.definition_invalid" {
		t.Fatalf("report error=%v", err)
	}
	if _, _, err := service.ValidateMetadataDefinitionRequestPayload(t.Context(), "preference", "policy.limit", metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{"key":"policy.limit","name":"Limit","value_type":"integer","value":1.5,"effective_from":"2026-07-01"}`)}); apperror.CodeOf(err) != "backend.preference.value_type_mismatch" || apperror.ParamsOf(err)["preference_key"] != "policy.limit" {
		t.Fatalf("preference error=%v code=%q params=%v", err, apperror.CodeOf(err), apperror.ParamsOf(err))
	}
	if _, issues, err := service.ValidateMetadataDefinitionRequestPayload(t.Context(), "report", "key", metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{}`)}); err != nil || len(issues) == 0 {
		t.Fatalf("report issues=%v err=%v", issues, err)
	}
	if _, _, err := service.ValidateMetadataDefinitionRequestPayload(t.Context(), "field", "key", metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{`)}); err == nil {
		t.Fatal("malformed field request unexpectedly valid")
	}
	viewRequest := metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{"key":"order_list","name":"Orders","object_key":"order","type":"table","config":{"columns":["number"]}}`)}
	if normalized, issues, err := service.ValidateMetadataDefinitionRequestPayload(t.Context(), "view", "order_list", viewRequest); err != nil || len(issues) != 0 || len(normalized) == 0 {
		t.Fatalf("normalized=%s issues=%v err=%v", normalized, issues, err)
	}
	for _, resourceType := range []string{"field", "automation_rule", "connector", "action", "report"} {
		if _, err := service.ValidateMetadataDefinitionPayload(t.Context(), resourceType, metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{`)}); err == nil {
			t.Fatalf("type=%s malformed payload unexpectedly valid", resourceType)
		}
	}
	if normalized, err := service.ValidateMetadataDefinitionPayload(t.Context(), "view", viewRequest); err != nil || len(normalized.Payload) == 0 {
		t.Fatalf("default normalized=%+v err=%v", normalized, err)
	}
}
