package workflow

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type workflowSchemaProviderEdgeStub struct {
	snapshot WorkflowSchemaSnapshot
}

func (s workflowSchemaProviderEdgeStub) WorkflowSchemaSnapshot(context.Context, principalmodel.Principal) WorkflowSchemaSnapshot {
	return s.snapshot
}

func (workflowSchemaProviderEdgeStub) ConnectorAdapterExists(context.Context, string) bool {
	return false
}

type workflowRecordReaderEdgeStub struct {
	records map[string]recordmodel.Record
	errID   string
}

func (s workflowRecordReaderEdgeStub) GetWorkflowRecord(_ context.Context, _ string, _ definitionmodel.ObjectSchema, id string) (recordmodel.Record, bool, error) {
	if id == s.errID {
		return recordmodel.Record{}, false, errors.New("read")
	}
	record, ok := s.records[id]
	return record, ok, nil
}

func (s workflowRecordReaderEdgeStub) ListWorkflowRecords(_ context.Context, _ string, _ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	items := []recordmodel.Record{}
	for _, raw := range query.Filters["id__in"].([]any) {
		id := fmt.Sprint(raw)
		if id == s.errID {
			return recordmodel.RecordPageResult{}, errors.New("read")
		}
		if record, ok := s.records[id]; ok {
			items = append(items, record)
		}
	}
	return recordmodel.RecordPageResult{Items: items}, nil
}

func TestWorkflowContractHelpersCoverSuccessAndFailureOutcomes(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "user-1", WorkspaceID: "workspace-1"}}
	if err := workflowAuthorizeQuery(principal); err != nil {
		t.Fatal(err)
	}
	if err := workflowAuthorizeCommand(principal); err != nil {
		t.Fatal(err)
	}
	if err := workflowAuthorizeQuery(principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("query error=%v", err)
	}
	if err := workflowAuthorizeCommand(principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("command error=%v", err)
	}
	validScope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "workflow test")
	if err := workflowAuthorizeSystemCommand(validScope); err != nil {
		t.Fatal(err)
	}
	if err := workflowAuthorizeSystemCommand(principalmodel.SystemScope{}); apperror.CodeOf(err) != "backend.system_scope_required" {
		t.Fatalf("system error=%v", err)
	}

	cause := errors.New("cause")
	for _, err := range []error{
		badRequest("bad", " key ", "value", "orphan"),
		forbidden("forbidden"),
		notFound("missing"),
		conflict("conflict"),
		internalError("load", cause),
		workflowError(apperror.KindBadRequest, " code ", cause, " ", "ignored"),
	} {
		if err == nil {
			t.Fatal("expected error")
		}
	}
	appErr := badRequest("bad", " key ", "value")
	if serviceErrorCode(appErr) != "bad" || errorParamsOf(appErr)["key"] != "value" {
		t.Fatalf("error projection code=%q params=%v", serviceErrorCode(appErr), errorParamsOf(appErr))
	}
	if serviceErrorCode(nil) != "" || errorParamsOf(cause) != nil || serviceErrorCode(cause) != "cause" {
		t.Fatal("plain error projection mismatch")
	}
	blankCode := &apperror.AppError{Kind: apperror.KindBadRequest, Err: cause}
	if serviceErrorCode(blankCode) == "" {
		t.Fatal("blank application error lost its cause")
	}
	if valueOrDefault(" value ", "fallback") != "value" || valueOrDefault(" ", "fallback") != "fallback" {
		t.Fatal("default normalization mismatch")
	}

	schema := integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: "connector"}}}
	cloned := cloneIntegrationSchema(schema)
	cloned.Connectors[0].Key = "changed"
	if schema.Connectors[0].Key != "connector" {
		t.Fatal("clone aliases source")
	}
	if actionName(definitionmodel.ActionSchema{Label: " Label ", Key: "key"}) != "Label" || actionName(definitionmodel.ActionSchema{Key: " key "}) != "key" {
		t.Fatal("action name mismatch")
	}
	if parsed, ok := parseMetricTime("2026-07-19T10:11:12.123456789Z"); !ok || parsed.IsZero() {
		t.Fatal("nanosecond time not parsed")
	}
	if _, ok := parseMetricTime("invalid"); ok {
		t.Fatal("invalid time parsed")
	}

	for kind, expected := range map[apperror.ErrorKind]string{
		apperror.KindBadRequest: "backend.bad_request",
		apperror.KindForbidden:  "backend.forbidden",
		apperror.KindNotFound:   "backend.not_found",
		apperror.KindConflict:   "backend.conflict",
		apperror.KindInternal:   "backend.internal",
	} {
		if actual := normalizeErrorCode(kind, "simple"); actual != expected {
			t.Fatalf("kind=%s actual=%s", kind, actual)
		}
	}
	if normalizeErrorCode(apperror.KindInternal, " custom.code ") != "custom.code" {
		t.Fatal("qualified error code changed")
	}
	if params := errorParams(" key ", "value", " ", "ignored", "orphan"); !reflect.DeepEqual(params, map[string]string{"key": "value"}) {
		t.Fatalf("params=%v", params)
	}
	if errorParams("orphan") != nil {
		t.Fatal("expected nil params")
	}
	if values := uniqueNonEmptyStrings([]string{" b ", "", "a", "b"}); !reflect.DeepEqual(values, []string{"a", "b"}) {
		t.Fatalf("unique values=%v", values)
	}
	values := []string{"a"}
	values = appendUniqueString(values, " ")
	values = appendUniqueString(values, "a")
	values = appendUniqueString(values, " b ")
	if !reflect.DeepEqual(values, []string{"a", "b"}) || !reflect.DeepEqual(uniqueSortedStrings(values), values) {
		t.Fatalf("appended values=%v", values)
	}
	valueMap := map[string]any{"key": "value"}
	if mapFromAny(valueMap)["key"] != "value" || len(mapFromAny("not-map")) != 0 || stringValue(" value ") != "value" {
		t.Fatal("generic value helper mismatch")
	}
	_ = time.RFC3339
}

func TestWorkflowPrincipalResolverUsesIdentityAccessBundle(t *testing.T) {
	initiator := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "initiator", WorkspaceID: "workspace-1"}, RequestID: "request", CorrelationID: "correlation"}
	unchanged, err := NewWorkflowPrincipalResolver(nil).ResolveWorkflowPrincipal(t.Context(), definitionmodel.WorkflowSchema{Key: "flow"}, initiator)
	if err != nil || unchanged.UserID != initiator.UserID {
		t.Fatalf("unchanged=%+v err=%v", unchanged, err)
	}
	bundle := identitysdk.AccessBundle{}
	resolved := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "workflow:flow", WorkspaceID: "workspace-1", AccessBundle: &bundle}}, accessfixture.Bundle{Key: "configured"})
	resolver := &workflowPrincipalResolverTestStub{resolution: workflowPrincipalResolution(resolved)}
	principal, err := NewWorkflowPrincipalResolver(resolver).ResolveWorkflowPrincipal(t.Context(), definitionmodel.WorkflowSchema{Key: "flow", RunAs: " configured "}, initiator)
	if err != nil || principal.UserID != "workflow:flow" || principal.RoleKey != "configured" || principal.AccessBundle == nil || principal.RequestID != "request" || principal.CorrelationID != "correlation" {
		t.Fatalf("principal=%+v err=%v", principal, err)
	}
	if _, err := NewWorkflowPrincipalResolver(nil).ResolveWorkflowPrincipal(t.Context(), definitionmodel.WorkflowSchema{Key: "flow", RunAs: "configured"}, initiator); err == nil {
		t.Fatal("missing Identity resolver did not fail closed")
	}
	unknown := resolved
	unknown.Known = false
	if _, err := NewWorkflowPrincipalResolver(&workflowPrincipalResolverTestStub{resolution: workflowPrincipalResolution(unknown)}).ResolveWorkflowPrincipal(t.Context(), definitionmodel.WorkflowSchema{Key: "flow", RunAs: "configured"}, initiator); err == nil {
		t.Fatal("unknown Identity principal did not fail closed")
	}
	wrongWorkspace := resolved
	wrongWorkspace.WorkspaceID = "workspace-2"
	if _, err := NewWorkflowPrincipalResolver(&workflowPrincipalResolverTestStub{resolution: workflowPrincipalResolution(wrongWorkspace)}).ResolveWorkflowPrincipal(t.Context(), definitionmodel.WorkflowSchema{Key: "flow", RunAs: "configured"}, initiator); err == nil {
		t.Fatal("cross-workspace principal did not fail closed")
	}
}

func TestWorkflowExecutionVisibilityCoversAuthorizationAndRecordOutcomes(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "user", WorkspaceID: "workspace-1"}}
	object := definitionmodel.ObjectSchema{Key: "order"}
	execution := workflowmodel.WorkflowExecution{ObjectKey: "order", RecordID: "denied", Payload: map[string]any{"object_key": "order", "record_id": "allowed"}}
	reader := workflowRecordReaderEdgeStub{records: map[string]recordmodel.Record{
		"denied":  {ID: "denied"},
		"allowed": {ID: "allowed"},
	}}
	service := NewWorkflowExecutionVisibilityApplicationService(reader, func(_ context.Context, _ principalmodel.Principal, _ definitionmodel.ObjectSchema, record recordmodel.Record) bool {
		return record.ID == "allowed"
	})
	visible, err := service.WorkflowExecutionVisible(t.Context(), principal, object, execution)
	if err != nil || !visible {
		t.Fatalf("visible=%v err=%v", visible, err)
	}
	visible, err = service.WorkflowExecutionVisible(t.Context(), principal, object, workflowmodel.WorkflowExecution{})
	if err != nil || !visible {
		t.Fatalf("unbound visible=%v err=%v", visible, err)
	}
	visible, err = service.WorkflowExecutionVisible(t.Context(), principal, object, workflowmodel.WorkflowExecution{ObjectKey: "order", RecordID: "missing"})
	if err != nil || visible {
		t.Fatalf("missing record visible=%v err=%v", visible, err)
	}
	service = NewWorkflowExecutionVisibilityApplicationService(reader, func(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool {
		return false
	})
	visible, err = service.WorkflowExecutionVisible(t.Context(), principal, object, execution)
	if err != nil || visible {
		t.Fatalf("denied visible=%v err=%v", visible, err)
	}
	if _, err := service.WorkflowExecutionVisible(t.Context(), principalmodel.Principal{}, object, execution); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization error=%v", err)
	}
	service = NewWorkflowExecutionVisibilityApplicationService(workflowRecordReaderEdgeStub{errID: "denied"}, func(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool {
		return true
	})
	if _, err := service.WorkflowExecutionVisible(t.Context(), principal, object, execution); err == nil || err.Error() != "get Workflow execution Record: read" {
		t.Fatalf("read error=%v", err)
	}
}
