package integration

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type integrationEntrypointWorkflow struct {
	result workflowmodel.WorkflowRunResult
	err    error
}

func (w *integrationEntrypointWorkflow) RunIntegrationWorkflow(context.Context, string, map[string]any, principalmodel.Principal) (workflowmodel.WorkflowRunResult, error) {
	return w.result, w.err
}

type integrationEntrypointRecords struct {
	err     error
	created recordmodel.Record
	updated recordmodel.Record
	deleted string
}

func (r *integrationEntrypointRecords) CreateRecord(_ context.Context, _ string, _ map[string]any, _ principalmodel.Principal) (recordmodel.Record, error) {
	if r.err != nil {
		return recordmodel.Record{}, r.err
	}
	r.created = recordmodel.Record{ID: "created"}
	return r.created, nil
}

func (r *integrationEntrypointRecords) UpdateRecord(_ context.Context, _, recordID string, _ map[string]any, _ principalmodel.Principal) (recordmodel.Record, error) {
	if r.err != nil {
		return recordmodel.Record{}, r.err
	}
	r.updated = recordmodel.Record{ID: recordID}
	return r.updated, nil
}

func (r *integrationEntrypointRecords) DeleteRecord(_ context.Context, _ string, recordID string, _ principalmodel.Principal) error {
	r.deleted = recordID
	return r.err
}

func integrationEntrypointService(repository *externalIdentityConfigRepo, workflows IntegrationWorkflowApplication, records IntegrationAgentRecordApplication, invoke IntegrationActionInvoker) *IntegrationApplicationService {
	return NewIntegrationApplicationService(ApplicationDependencies{
		ConfigRepository: repository,
		Workflows:        workflows,
		Records:          records,
		InvokeAction:     invoke,
		PrincipalResolver: func(_ context.Context, actorID, roleKey, _ string) principalmodel.Principal {
			return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: actorID, WorkspaceID: "workspace"}}, accessfixture.Bundle{Key: roleKey})
		},
	})
}

func integrationEntrypointIdentityRepository() *externalIdentityConfigRepo {
	return &externalIdentityConfigRepo{identities: []integrationmodel.IntegrationExternalIdentity{{
		Key: "mapping", WorkspaceID: "workspace", Provider: "provider", ExternalSubject: "subject", ActorID: "actor", RoleKey: "member", Status: "active",
	}}}
}

func integrationEntrypointResolveRequest() integrationmodel.IntegrationExternalIdentityResolveRequest {
	return integrationmodel.IntegrationExternalIdentityResolveRequest{Provider: "provider", ExternalSubject: "subject"}
}

func TestExecuteIntegrationActionEdges(t *testing.T) {
	repository := integrationEntrypointIdentityRepository()
	actionErr := error(nil)
	var capturedInput map[string]any
	var capturedIdempotencyKey string
	service := integrationEntrypointService(repository, nil, nil, func(_ context.Context, invocation actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error) {
		capturedInput = invocation.Input
		capturedIdempotencyKey = invocation.IdempotencyKey
		if actionErr != nil {
			return actionmodel.ActionInvocationResult{}, actionErr
		}
		return actionmodel.ActionInvocationResult{Record: &actionmodel.ActionResult{ActionKey: invocation.ActionKey, ObjectKey: invocation.ObjectKey, RecordID: invocation.RecordID}}, nil
	})
	caller := integrationManagementPrincipal("integration.entrypoint.invoke")
	request := integrationmodel.IntegrationEntrypointActionRequest{ExternalIdentity: integrationEntrypointResolveRequest(), Data: map[string]any{"value": true}, IdempotencyKey: "integration-event-1"}
	if _, err := service.ExecuteIntegrationAction(t.Context(), "object", "record", "action", request, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("action authorization error=%v", err)
	}
	badRequest := request
	badRequest.ExternalIdentity.ExternalSubject = "missing"
	if _, err := service.ExecuteIntegrationAction(t.Context(), "object", "record", "action", badRequest, caller); apperror.CodeOf(err) != "backend.integration.external_identity.unmapped" {
		t.Fatalf("action resolution error=%v", err)
	}
	actionErr = errIntegrationManagementTest
	if _, err := service.ExecuteIntegrationAction(t.Context(), "object", "record", "action", request, caller); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("action invocation error=%v", err)
	}
	actionErr = nil
	result, err := service.ExecuteIntegrationAction(t.Context(), " object ", " record ", " action ", request, caller)
	if err != nil || result.Action.ActionKey != " action " || !result.ExternalIdentity.Mapped {
		t.Fatalf("action result=%#v err=%v", result, err)
	}
	if capturedInput["value"] != true || capturedInput["external_principal"] != nil {
		t.Fatalf("action input was polluted by identity audit context: %#v", capturedInput)
	}
	if capturedIdempotencyKey != "integration-event-1" {
		t.Fatalf("integration idempotency key was lost: %q", capturedIdempotencyKey)
	}
}

func TestRunIntegrationWorkflowEdges(t *testing.T) {
	repository := integrationEntrypointIdentityRepository()
	workflow := &integrationEntrypointWorkflow{result: workflowmodel.WorkflowRunResult{WorkflowKey: "workflow", Status: "completed"}}
	service := integrationEntrypointService(repository, workflow, nil, nil)
	caller := integrationManagementPrincipal("integration.entrypoint.invoke")
	request := integrationmodel.IntegrationEntrypointWorkflowRequest{ExternalIdentity: integrationEntrypointResolveRequest(), Payload: map[string]any{"value": true}}
	if _, err := service.RunIntegrationWorkflow(t.Context(), "workflow", request, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("workflow authorization error=%v", err)
	}
	badRequest := request
	badRequest.ExternalIdentity.ExternalSubject = "missing"
	if _, err := service.RunIntegrationWorkflow(t.Context(), "workflow", badRequest, caller); apperror.CodeOf(err) != "backend.integration.external_identity.unmapped" {
		t.Fatalf("workflow resolution error=%v", err)
	}
	workflow.err = errIntegrationManagementTest
	if _, err := service.RunIntegrationWorkflow(t.Context(), "workflow", request, caller); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("workflow execution error=%v", err)
	}
	workflow.err = nil
	result, err := service.RunIntegrationWorkflow(t.Context(), " workflow ", request, caller)
	if err != nil || result.Workflow.Status != "completed" || !result.ExternalIdentity.Mapped {
		t.Fatalf("workflow result=%#v err=%v", result, err)
	}
}

func TestExecuteIntegrationAgentRecordToolEdges(t *testing.T) {
	records := &integrationEntrypointRecords{}
	service := NewIntegrationApplicationService(ApplicationDependencies{Records: records})
	principal := integrationManagementPrincipal()
	if result, err := service.executeIntegrationAgentRecordTool(t.Context(), "unknown", nil, principal); err != nil || result != nil {
		t.Fatalf("unknown tool result=%#v err=%v", result, err)
	}
	if result, err := service.executeIntegrationAgentRecordTool(t.Context(), "createRecord", map[string]any{"object_key": "customer", "data": map[string]any{"name": "A"}}, principal); err != nil || result["record_id"] != "created" {
		t.Fatalf("create result=%#v err=%v", result, err)
	}
	if result, err := service.executeIntegrationAgentRecordTool(t.Context(), "updateRecord", map[string]any{"object_key": "customer", "id": "record", "patch": map[string]any{"name": "B"}}, principal); err != nil || result["record_id"] != "record" {
		t.Fatalf("update result=%#v err=%v", result, err)
	}
	if result, err := service.executeIntegrationAgentRecordTool(t.Context(), "deleteRecord", map[string]any{"object_key": "customer", "record_id": "record"}, principal); err != nil || result["operation"] != "delete" || records.deleted != "record" {
		t.Fatalf("delete result=%#v err=%v", result, err)
	}
	for _, tool := range []string{"createRecord", "updateRecord", "deleteRecord"} {
		records.err = errIntegrationManagementTest
		if _, err := service.executeIntegrationAgentRecordTool(t.Context(), tool, map[string]any{"object_key": "customer", "record_id": "record"}, principal); !errors.Is(err, errIntegrationManagementTest) {
			t.Fatalf("%s error=%v", tool, err)
		}
	}
}

func TestInvokeIntegrationAgentToolEdges(t *testing.T) {
	repository := integrationEntrypointIdentityRepository()
	records := &integrationEntrypointRecords{}
	delivery := &independentDeliveryRepository{}
	snapshot := metadatamodel.MetadataSchemaSnapshot{
		Agents:       []agentmodel.AgentSchema{{Key: "agent", Tools: []string{"readRecord", "createRecord", "callConnector"}}},
		Integrations: integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: "connector"}}},
	}
	service := NewIntegrationApplicationService(ApplicationDependencies{
		ConfigRepository:   repository,
		DeliveryRepository: delivery,
		Records:            records,
		Schema:             func(context.Context, principalmodel.Principal) metadatamodel.MetadataSchemaSnapshot { return snapshot },
		PrincipalResolver: func(_ context.Context, actorID, roleKey, _ string) principalmodel.Principal {
			return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: actorID, WorkspaceID: "workspace"}}, accessfixture.Bundle{Key: roleKey})
		},
	})
	caller := integrationManagementPrincipal("integration.entrypoint.invoke")
	caller.RequestID = "request-id"
	request := integrationmodel.IntegrationAgentToolInvocationRequest{ExternalIdentity: integrationEntrypointResolveRequest(), Input: map[string]any{"object_key": "customer", "data": map[string]any{"name": "A"}}}
	if _, err := service.InvokeIntegrationAgentTool(t.Context(), "agent", "readRecord", request, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("agent tool authorization error=%v", err)
	}
	if _, err := service.InvokeIntegrationAgentTool(t.Context(), "agent", "readRecord", request, integrationManagementPrincipal()); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("agent tool permission error=%v", err)
	}
	if _, err := service.InvokeIntegrationAgentTool(t.Context(), " ", "readRecord", request, caller); apperror.CodeOf(err) != "backend.integration.agent_tool.missing_identity" {
		t.Fatalf("missing agent error=%v", err)
	}
	if _, err := service.InvokeIntegrationAgentTool(t.Context(), "agent", " ", request, caller); apperror.CodeOf(err) != "backend.integration.agent_tool.missing_identity" {
		t.Fatalf("missing tool error=%v", err)
	}
	badRequest := request
	badRequest.ExternalIdentity.ExternalSubject = "missing"
	if _, err := service.InvokeIntegrationAgentTool(t.Context(), "agent", "readRecord", badRequest, caller); apperror.CodeOf(err) != "backend.integration.external_identity.unmapped" {
		t.Fatalf("agent identity resolution error=%v", err)
	}
	if _, err := service.InvokeIntegrationAgentTool(t.Context(), "missing", "readRecord", request, caller); apperror.CodeOf(err) != "backend.integration.agent_tool.not_allowed" {
		t.Fatalf("missing agent error=%v", err)
	}
	if _, err := service.InvokeIntegrationAgentTool(t.Context(), "agent", "deleteRecord", request, caller); apperror.CodeOf(err) != "backend.integration.agent_tool.not_allowed" {
		t.Fatalf("disallowed tool error=%v", err)
	}
	guarded := snapshot
	guarded.GuardedWrites = []metadatamodel.MetadataGuardedWriteContract{{ObjectKey: "customer", Operation: "create", ActionKey: "create_customer", Endpoint: "/customers"}}
	service.schema = func(context.Context, principalmodel.Principal) metadatamodel.MetadataSchemaSnapshot { return guarded }
	if _, err := service.InvokeIntegrationAgentTool(t.Context(), "agent", "createRecord", request, caller); apperror.CodeOf(err) != "backend.integration.agent_tool.guarded_action_required" {
		t.Fatalf("guarded write error=%v", err)
	}
	service.schema = func(context.Context, principalmodel.Principal) metadatamodel.MetadataSchemaSnapshot { return snapshot }
	if result, err := service.InvokeIntegrationAgentTool(t.Context(), "agent", "createRecord", request, caller); err != nil || result.Status != "approval_required" || result.ApprovalPlan == nil {
		t.Fatalf("approval result=%#v err=%v", result, err)
	}
	if result, err := service.InvokeIntegrationAgentTool(t.Context(), "agent", "readRecord", request, caller); err != nil || result.Status != "prepared" || result.ApprovalPlan != nil {
		t.Fatalf("prepared result=%#v err=%v", result, err)
	}
	callerWithoutRequestID := caller
	callerWithoutRequestID.RequestID = ""
	if result, err := service.InvokeIntegrationAgentTool(t.Context(), "agent", "readRecord", request, callerWithoutRequestID); err != nil || result.Status != "prepared" {
		t.Fatalf("prepared without request ID result=%#v err=%v", result, err)
	}
	approved := request
	approved.Approved = true
	if result, err := service.InvokeIntegrationAgentTool(t.Context(), "agent", "createRecord", approved, caller); err != nil || result.Status != "executed" {
		t.Fatalf("executed result=%#v err=%v", result, err)
	}
	records.err = errIntegrationManagementTest
	if _, err := service.InvokeIntegrationAgentTool(t.Context(), "agent", "createRecord", approved, caller); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("execution error=%v", err)
	}
	records.err = nil
	delivery.insertInvocationErr = errIntegrationManagementTest
	if _, err := service.InvokeIntegrationAgentTool(t.Context(), "agent", "readRecord", request, caller); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("invocation insert error=%v", err)
	}
	delivery.insertInvocationErr = nil
	external := request
	external.Input = map[string]any{}
	if _, err := service.InvokeIntegrationAgentTool(t.Context(), "agent", "callConnector", external, caller); apperror.CodeOf(err) != "backend.integration.agent_tool.connector_required" {
		t.Fatalf("connector risk error=%v", err)
	}
	external.Input = map[string]any{"connector_key": "connector"}
	if result, err := service.InvokeIntegrationAgentTool(t.Context(), "agent", "callConnector", external, caller); err != nil || result.Status != "approval_required" {
		t.Fatalf("connector approval result=%#v err=%v", result, err)
	}
	external.Approved = true
	if result, err := service.InvokeIntegrationAgentTool(t.Context(), "agent", "callConnector", external, caller); err != nil || result.Status != "prepared" {
		t.Fatalf("approved connector result=%#v err=%v", result, err)
	}
}
