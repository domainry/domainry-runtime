package integration

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type integrationEventRecords struct {
	items     []recordmodel.Record
	listErr   error
	createErr error
	created   recordmodel.Record
}

func (r *integrationEventRecords) CreateRecord(_ context.Context, _ string, data map[string]any, _ principalmodel.Principal) (recordmodel.Record, error) {
	if r.createErr != nil {
		return recordmodel.Record{}, r.createErr
	}
	r.created = recordmodel.Record{ID: "task", Data: data}
	return r.created, nil
}

func (r *integrationEventRecords) ListRecords(context.Context, string, recordmodel.RecordListQuery, principalmodel.Principal) (recordmodel.RecordPageResult, error) {
	return recordmodel.RecordPageResult{Items: append([]recordmodel.Record(nil), r.items...)}, r.listErr
}

func integrationEventMappingService(mapping integrationmodel.IntegrationEventMappingSchema, records *integrationEventRecords) *IntegrationApplicationService {
	return NewIntegrationApplicationService(ApplicationDependencies{
		Registry:     NewConnectorRegistry(integrationmodel.IntegrationSchema{EventMappings: []integrationmodel.IntegrationEventMappingSchema{mapping}}),
		EventRecords: records,
		SchemaObjectMap: func(context.Context) map[string]definitionmodel.ObjectSchema {
			return map[string]definitionmodel.ObjectSchema{"activity": {Key: "activity", Fields: []definitionmodel.FieldSchema{{Key: "subject", Type: "text"}, {Key: "owner_id", Type: "text"}, {Key: "status", Type: "text"}}}}
		},
	})
}

func TestExecuteIntegrationEventWorkflowAndActionEdges(t *testing.T) {
	principal := integrationManagementPrincipal()
	event := integrationmodel.IntegrationEvent{ID: "event", WorkspaceID: "workspace", Provider: "provider", EventType: "created", ExternalID: "external", Payload: map[string]any{}}
	empty := integrationEventMappingService(integrationmodel.IntegrationEventMappingSchema{Key: "other", Provider: "other", TargetType: "workflow", WorkflowKey: "workflow"}, &integrationEventRecords{})
	if _, handled, err := empty.ExecuteIntegrationEventMapping(t.Context(), event, principalmodel.Principal{}); handled || apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization handled=%v err=%v", handled, err)
	}
	if _, handled, err := empty.ExecuteIntegrationEventMapping(t.Context(), event, principal); err != nil || handled {
		t.Fatalf("unmatched handled=%v err=%v", handled, err)
	}
	invalid := integrationEventMappingService(integrationmodel.IntegrationEventMappingSchema{Key: "invalid", Provider: "provider", EventType: "created", TargetType: "invalid"}, &integrationEventRecords{})
	if _, handled, err := invalid.ExecuteIntegrationEventMapping(t.Context(), event, principal); !handled || apperror.CodeOf(err) != "backend.integration.event_mapping.invalid_target" {
		t.Fatalf("invalid handled=%v err=%v", handled, err)
	}

	workflow := integrationEventMappingService(integrationmodel.IntegrationEventMappingSchema{Key: "workflow", Provider: "provider", EventType: "created", TargetType: "workflow", WorkflowKey: "workflow"}, &integrationEventRecords{})
	workflow.executeEventWorkflow = func(context.Context, string, integrationmodel.IntegrationEntrypointWorkflowRequest, principalmodel.Principal) (IntegrationWorkflowRunResult, error) {
		return IntegrationWorkflowRunResult{}, errIntegrationManagementTest
	}
	if _, handled, err := workflow.ExecuteIntegrationEventMapping(t.Context(), event, principal); !handled || !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("workflow failure handled=%v err=%v", handled, err)
	}
	workflow.executeEventWorkflow = func(context.Context, string, integrationmodel.IntegrationEntrypointWorkflowRequest, principalmodel.Principal) (IntegrationWorkflowRunResult, error) {
		return IntegrationWorkflowRunResult{Workflow: workflowmodel.WorkflowRunResult{WorkflowKey: "workflow", Status: "completed"}}, nil
	}
	if decision, handled, err := workflow.ExecuteIntegrationEventMapping(t.Context(), event, principal); err != nil || !handled || decision.Status != "processed" {
		t.Fatalf("workflow decision=%#v handled=%v err=%v", decision, handled, err)
	}

	action := integrationEventMappingService(integrationmodel.IntegrationEventMappingSchema{Key: "action", Provider: "provider", EventType: "created", TargetType: "action", ObjectKey: "customer", RecordID: "record", ActionKey: "approve"}, &integrationEventRecords{})
	action.executeEventAction = func(context.Context, string, string, string, integrationmodel.IntegrationEntrypointActionRequest, principalmodel.Principal) (IntegrationActionExecutionResult, error) {
		return IntegrationActionExecutionResult{}, errIntegrationManagementTest
	}
	if _, handled, err := action.ExecuteIntegrationEventMapping(t.Context(), event, principal); !handled || !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("action failure handled=%v err=%v", handled, err)
	}
	action.executeEventAction = func(context.Context, string, string, string, integrationmodel.IntegrationEntrypointActionRequest, principalmodel.Principal) (IntegrationActionExecutionResult, error) {
		return IntegrationActionExecutionResult{Action: actionmodel.ActionResult{ObjectKey: "customer", RecordID: "record", ActionKey: "approve"}}, nil
	}
	if decision, handled, err := action.ExecuteIntegrationEventMapping(t.Context(), event, principal); err != nil || !handled || decision.Status != "processed" {
		t.Fatalf("action decision=%#v handled=%v err=%v", decision, handled, err)
	}
}

func TestExecuteIntegrationOwnerTaskEdges(t *testing.T) {
	records := &integrationEventRecords{}
	mapping := integrationmodel.IntegrationEventMappingSchema{Key: "owner", Provider: "provider", EventType: "created", TargetType: "owner_task"}
	service := integrationEventMappingService(mapping, records)
	event := integrationmodel.IntegrationEvent{ID: "event", WorkspaceID: "workspace", Provider: "provider", EventType: "created", Payload: map[string]any{"title": "Follow up", "email": "person@example.com"}}
	principal := integrationManagementPrincipal()
	service.resolveEventIdentity = func(context.Context, integrationmodel.IntegrationExternalIdentityResolveRequest, principalmodel.Principal) (integrationmodel.IntegrationExternalIdentityResolveResult, principalmodel.Principal, error) {
		return integrationmodel.IntegrationExternalIdentityResolveResult{}, principalmodel.Principal{}, errIntegrationManagementTest
	}
	if _, handled, err := service.ExecuteIntegrationEventMapping(t.Context(), event, principal); !handled || !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("identity failure handled=%v err=%v", handled, err)
	}
	resolvedPrincipal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "owner"}}
	service.resolveEventIdentity = func(context.Context, integrationmodel.IntegrationExternalIdentityResolveRequest, principalmodel.Principal) (integrationmodel.IntegrationExternalIdentityResolveResult, principalmodel.Principal, error) {
		return integrationmodel.IntegrationExternalIdentityResolveResult{Mapped: true}, resolvedPrincipal, nil
	}
	records.createErr = errIntegrationManagementTest
	if _, handled, err := service.ExecuteIntegrationEventMapping(t.Context(), event, principal); !handled || !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("create failure handled=%v err=%v", handled, err)
	}
	records.createErr = nil
	if decision, handled, err := service.ExecuteIntegrationEventMapping(t.Context(), event, principal); err != nil || !handled || decision.Status != "processed" || records.created.ID != "task" {
		t.Fatalf("owner decision=%#v handled=%v task=%#v err=%v", decision, handled, records.created, err)
	}

	missingOwner := integrationEventMappingService(mapping, records)
	if _, _, err := missingOwner.createIntegrationOwnerTask(t.Context(), event, mapping, event.Payload, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.integration.event_mapping.owner_task_missing_owner" {
		t.Fatalf("owner projection error=%v", err)
	}
}

func TestIntegrationFindFirstRecordByFieldEdges(t *testing.T) {
	records := &integrationEventRecords{}
	service := NewIntegrationApplicationService(ApplicationDependencies{EventRecords: records})
	principal := integrationManagementPrincipal()
	if _, ok := service.IntegrationFindFirstRecordByField(t.Context(), "customer", "email", "value", principalmodel.Principal{}); ok {
		t.Fatal("unauthorized record lookup succeeded")
	}
	if _, ok := service.IntegrationFindFirstRecordByField(t.Context(), "customer", "email", " ", principal); ok {
		t.Fatal("blank record lookup succeeded")
	}
	records.listErr = errIntegrationManagementTest
	if _, ok := service.IntegrationFindFirstRecordByField(t.Context(), "customer", "email", "value", principal); ok {
		t.Fatal("failed record lookup succeeded")
	}
	records.listErr = nil
	if _, ok := service.IntegrationFindFirstRecordByField(t.Context(), "customer", "email", "value", principal); ok {
		t.Fatal("empty record lookup succeeded")
	}
	records.items = []recordmodel.Record{{ID: "record"}}
	if record, ok := service.IntegrationFindFirstRecordByField(t.Context(), "customer", "email", " value ", principal); !ok || record.ID != "record" {
		t.Fatalf("record=%#v ok=%v", record, ok)
	}
}
