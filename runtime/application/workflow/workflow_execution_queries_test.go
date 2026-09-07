package workflow

import (
	"context"
	"reflect"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func TestWorkflowInspectExecutionContracts(t *testing.T) {
	worker := &workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{"execution-1": {ID: "execution-1"}}}
	service := newWorkflowExecutionService(worker)
	principal := workflowExecutionPrincipal()
	missingWorkspace := principal
	missingWorkspace.WorkspaceID = ""
	if _, err := service.InspectWorkflowExecution(t.Context(), "execution-1", missingWorkspace); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("workspace error=%v", err)
	}
	worker.getErr = errWorkflowExecutionStore
	if _, err := service.InspectWorkflowExecution(t.Context(), "execution-1", principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("store error=%v", err)
	}
	worker.getErr = nil
	if _, err := service.InspectWorkflowExecution(t.Context(), "missing", principal); apperror.CodeOf(err) != "backend.workflow.execution_not_found" {
		t.Fatalf("not found=%v", err)
	}
	if execution, err := service.InspectWorkflowExecution(t.Context(), " execution-1 ", principal); err != nil || execution.ID != "execution-1" {
		t.Fatalf("execution=%#v err=%v", execution, err)
	}
}

func TestWorkflowRunAndSimulationContracts(t *testing.T) {
	worker := &workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}
	workflow := workflowExecutionSchema()
	service := newWorkflowExecutionService(worker, workflow)
	principal := workflowExecutionPrincipal()

	unknown := principal
	unknown.Known = false
	if _, err := service.RunWorkflow(t.Context(), workflow.Key, nil, unknown); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("unknown run=%v", err)
	}
	denied := principal
	denied = workflowPrincipalWithPermissions(denied)
	if _, err := service.RunWorkflow(t.Context(), workflow.Key, nil, denied); apperror.CodeOf(err) != "backend.workflow.run_permission_required" {
		t.Fatalf("denied run=%v", err)
	}
	scoped := principal
	scoped = workflowPrincipalWithPermissions(scoped, "workflow."+workflow.Key+".run")
	if result, err := service.RunWorkflow(t.Context(), workflow.Key, map[string]any{"source": "portal"}, scoped); err != nil || result.WorkflowKey != workflow.Key {
		t.Fatalf("scoped run result=%#v err=%v", result, err)
	}
	scoped = workflowPrincipalWithPermissions(scoped, "workflow.other.run")
	if _, err := service.RunWorkflow(t.Context(), workflow.Key, nil, scoped); apperror.CodeOf(err) != "backend.workflow.run_permission_required" {
		t.Fatalf("wrong scoped run=%v", err)
	}
	if _, err := service.RunWorkflow(t.Context(), "missing", nil, principal); apperror.CodeOf(err) != "backend.workflow.not_found" {
		t.Fatalf("missing run=%v", err)
	}
	nonManual := workflow
	nonManual.Key = "event-only"
	nonManual.TriggerContract = &definitionmodel.WorkflowTriggerContract{Type: "event"}
	service.registry.Set(nonManual.Key, nonManual)
	nonManualPrincipal := workflowPrincipalWithPermissions(principal, "workflow."+nonManual.Key+".run")
	if _, err := service.RunWorkflow(t.Context(), nonManual.Key, nil, nonManualPrincipal); apperror.CodeOf(err) != "backend.workflow.entry_mode_invalid" {
		t.Fatalf("manual gate=%v", err)
	}
	invalidManual := workflow
	invalidManual.Key, invalidManual.Graph = "invalid-manual", nil
	service.registry.Set(invalidManual.Key, invalidManual)
	invalidPrincipal := workflowPrincipalWithPermissions(principal, "workflow."+invalidManual.Key+".run")
	if _, err := service.RunWorkflow(t.Context(), invalidManual.Key, nil, invalidPrincipal); apperror.CodeOf(err) != "backend.workflow.graph_v2_required" {
		t.Fatalf("invalid execution=%v", err)
	}
	result, err := service.RunWorkflow(t.Context(), workflow.Key, map[string]any{"order": "one"}, principal)
	if err != nil || result.WorkflowKey != workflow.Key || result.Execution.ID == "" || result.Status != "completed" {
		t.Fatalf("result=%#v err=%v", result, err)
	}

	if _, err := service.SimulateWorkflow(t.Context(), workflow.Key, nil, unknown); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("unknown simulation=%v", err)
	}
	if _, err := service.SimulateWorkflow(t.Context(), "missing", nil, principal); apperror.CodeOf(err) != "backend.workflow.not_found" {
		t.Fatalf("missing simulation=%v", err)
	}
	invalidSimulation := workflow
	invalidSimulation.Key, invalidSimulation.Graph = "invalid-simulation", nil
	service.registry.Set(invalidSimulation.Key, invalidSimulation)
	if _, err := service.SimulateWorkflow(t.Context(), invalidSimulation.Key, nil, principal); err == nil {
		t.Fatal("expected simulation graph error")
	}
	disabled := workflow
	disabled.Key, disabled.Enabled = "disabled", false
	service.registry.Set(disabled.Key, disabled)
	if simulation, err := service.SimulateWorkflow(t.Context(), disabled.Key, nil, principal); err != nil || simulation.Message != "Workflow is disabled" || simulation.WouldExecute {
		t.Fatalf("disabled=%#v err=%v", simulation, err)
	}
	if simulation, err := service.SimulateWorkflow(t.Context(), workflow.Key, nil, principal); err != nil || !simulation.WouldExecute || len(simulation.Nodes) != 1 {
		t.Fatalf("simulation=%#v err=%v", simulation, err)
	}
}

func TestManualWorkflowCallerKeyIsRequiredAndClaimed(t *testing.T) {
	workflow := workflowExecutionSchema()
	workflow.IdempotencyKeys = []string{"order"}
	worker := &workflowExecutionWorkerStub{
		executions: map[string]workflowmodel.WorkflowExecution{},
		claim: workflowmodel.WorkflowExecutionClaimResult{
			Decision: idempotency.DecisionAcquired,
			Receipt: workflowmodel.WorkflowExecutionReceipt{
				ID: "receipt-1", WorkspaceID: "workspace-1", WorkflowKey: workflow.Key,
				LeaseOwner: "owner-1", FencingToken: 1,
			},
		},
	}
	service := newWorkflowExecutionService(worker, workflow)
	principal := workflowExecutionPrincipal()

	if _, err := service.RunWorkflowWithKey(t.Context(), workflow.Key, nil, " ", principal); apperror.CodeOf(err) != idempotency.ErrorCodeMissingKey {
		t.Fatalf("missing caller key error=%v", err)
	}
	payload := map[string]any{"order": "one"}
	result, err := service.RunWorkflowWithKey(t.Context(), workflow.Key, payload, " caller-operation-1 ", principal)
	if err != nil {
		t.Fatal(err)
	}
	if len(worker.claimRequests) != 1 || worker.claimRequests[0].Receipt.IdempotencyKey != "caller-operation-1" {
		t.Fatalf("caller claim=%#v", worker.claimRequests)
	}
	if result.Execution.IdempotencyKey != "caller-operation-1" {
		t.Fatalf("execution idempotency key=%q", result.Execution.IdempotencyKey)
	}

	legacyWorker := &workflowExecutionWorkerStub{
		executions: map[string]workflowmodel.WorkflowExecution{},
		claim: workflowmodel.WorkflowExecutionClaimResult{
			Decision: idempotency.DecisionAcquired,
			Receipt: workflowmodel.WorkflowExecutionReceipt{
				ID: "receipt-2", WorkspaceID: "workspace-1", WorkflowKey: workflow.Key,
				LeaseOwner: "owner-2", FencingToken: 1,
			},
		},
	}
	legacyService := newWorkflowExecutionService(legacyWorker, workflow)
	legacy, err := legacyService.RunWorkflow(t.Context(), workflow.Key, payload, principal)
	if err != nil {
		t.Fatal(err)
	}
	authoredKey := workflowpolicy.WorkflowIdempotencyKey(workflow, payload)
	if len(legacyWorker.claimRequests) != 1 || legacyWorker.claimRequests[0].Receipt.IdempotencyKey != authoredKey || legacy.Execution.IdempotencyKey != authoredKey {
		t.Fatalf("legacy authored key changed: claims=%#v execution_key=%q want=%q", legacyWorker.claimRequests, legacy.Execution.IdempotencyKey, authoredKey)
	}
}

func TestManualWorkflowPayloadUsesAuthenticatedInitiatorWithoutMutatingCaller(t *testing.T) {
	tests := []struct {
		name          string
		payload       map[string]any
		clearIdentity bool
		wantUser      string
		wantRole      string
	}{
		{name: "forged values are overwritten", payload: map[string]any{"source": "portal", "initiating_user_id": "forged-user", "initiating_role_key": "forged-role"}, wantUser: "operator", wantRole: "operator"},
		{name: "missing values are injected", payload: map[string]any{"source": "portal"}, wantUser: "operator", wantRole: "operator"},
		{name: "forged values are removed when trusted identity is empty", payload: map[string]any{"source": "portal", "initiating_user_id": "forged-user", "initiating_role_key": "forged-role"}, clearIdentity: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			worker := &workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}
			workflow := workflowExecutionSchema()
			service := newWorkflowExecutionService(worker, workflow)
			principal := workflowExecutionPrincipal()
			principal.RequestID = "request-must-not-be-injected"
			if test.clearIdentity {
				principal.UserID, principal.RoleKey = "", ""
			}
			original := workflowpolicy.WorkflowCloneMap(test.payload)

			result, err := service.RunWorkflow(t.Context(), workflow.Key, test.payload, principal)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(test.payload, original) {
				t.Fatalf("caller payload mutated: got=%#v want=%#v", test.payload, original)
			}
			for name, payload := range map[string]map[string]any{"result": result.Payload, "execution": result.Execution.Payload} {
				if got := workflowpolicy.WorkflowPayloadString(payload, "initiating_user_id"); got != test.wantUser {
					t.Fatalf("%s initiating user=%q want=%q payload=%#v", name, got, test.wantUser, payload)
				}
				if got := workflowpolicy.WorkflowPayloadString(payload, "initiating_role_key"); got != test.wantRole {
					t.Fatalf("%s initiating role=%q want=%q payload=%#v", name, got, test.wantRole, payload)
				}
				if test.clearIdentity {
					if _, ok := payload["initiating_user_id"]; ok {
						t.Fatalf("%s retained untrusted initiating_user_id: %#v", name, payload)
					}
					if _, ok := payload["initiating_role_key"]; ok {
						t.Fatalf("%s retained untrusted initiating_role_key: %#v", name, payload)
					}
				}
				if _, ok := payload["request_id"]; ok {
					t.Fatalf("%s unexpectedly injected request_id: %#v", name, payload)
				}
			}
		})
	}
}

func TestManualWorkflowRunAsKeepsServicePrincipalAndTrustedHumanInitiator(t *testing.T) {
	workflow := workflowExecutionSchema()
	workflow.RunAs = "service-role"
	workflow.Graph = &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{
		{ID: "start", Type: "trigger", Name: "Start"},
		{ID: "capture", Type: "action", Name: "Capture", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{
			ActionKey: "capture", Input: map[string]any{
				"principal_user": "$principal.user_id", "principal_role": "$principal.role_key",
				"initiating_user": "$workflow.initiating_user_id", "initiating_role": "$workflow.initiating_role_key",
			},
		}}},
	}, Edges: []definitionmodel.WorkflowGraphEdge{{ID: "start-capture", Source: "start", Target: "capture", Branch: "success"}}}
	worker := &workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}
	processes := &workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{}}
	servicePrincipal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{
		Known: true, WorkspaceID: "workspace-1", UserID: "workflow-service",
	}}, accessfixture.Bundle{Key: "service-role"})
	resolver := &workflowPrincipalResolverTestStub{resolution: workflowPrincipalResolution(servicePrincipal)}
	var invocation WorkflowBusinessActionInvocation
	service := NewWorkflowApplicationService(WorkflowDependencies{
		Workers: worker, Processes: processes, WorkflowRegistry: &workflowRegistryStub{items: map[string]definitionmodel.WorkflowSchema{workflow.Key: workflow}}, Principals: resolver,
		InvokeAction: func(_ context.Context, received WorkflowBusinessActionInvocation) (WorkflowBusinessActionInvocationResult, error) {
			invocation = received
			return WorkflowBusinessActionInvocationResult{InvocationID: "capture-1", Status: "completed"}, nil
		},
	})
	principal := workflowExecutionPrincipal()
	payload := map[string]any{"initiating_user_id": "forged-user", "initiating_role_key": "forged-role"}

	result, err := service.RunWorkflow(t.Context(), workflow.Key, payload, principal)
	if err != nil {
		t.Fatal(err)
	}
	if invocation.Principal.UserID != "workflow-service" || invocation.Principal.RoleKey != "service-role" {
		t.Fatalf("run_as principal=%+v", invocation.Principal)
	}
	wantInput := map[string]any{
		"principal_user": "workflow-service", "principal_role": "service-role",
		"initiating_user": "operator", "initiating_role": "operator",
	}
	if !reflect.DeepEqual(invocation.Input, wantInput) {
		t.Fatalf("rendered action input=%#v want=%#v", invocation.Input, wantInput)
	}
	process := processes.processes[result.Execution.ProcessID]
	if process.InitiatorID != "operator" || process.InitiatorRoleKey != "operator" ||
		workflowpolicy.WorkflowPayloadString(process.Variables, "initiating_user_id") != "operator" ||
		workflowpolicy.WorkflowPayloadString(process.Variables, "initiating_role_key") != "operator" {
		t.Fatalf("trusted process initiator was not preserved: %+v", process)
	}
	if payload["initiating_user_id"] != "forged-user" || payload["initiating_role_key"] != "forged-role" {
		t.Fatalf("caller payload mutated: %#v", payload)
	}
}

func TestWorkflowDraftSimulationContracts(t *testing.T) {
	service := newWorkflowExecutionService(&workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}})
	principal := workflowExecutionPrincipal()
	unknown := principal
	unknown.Known = false
	if _, err := service.SimulateWorkflowCandidate(t.Context(), definitionmodel.WorkflowSchema{}, nil, unknown); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("unknown=%v", err)
	}
	if _, err := service.SimulateWorkflowCandidate(t.Context(), definitionmodel.WorkflowSchema{}, nil, principal); apperror.CodeOf(err) != "backend.workflow.key_required" {
		t.Fatalf("key=%v", err)
	}
	invalid := workflowExecutionSchema()
	invalid.Graph = nil
	if _, err := service.SimulateWorkflowCandidate(t.Context(), invalid, nil, principal); err == nil {
		t.Fatal("expected graph validation error")
	}
	disabled := workflowExecutionSchema()
	disabled.Enabled = false
	if result, err := service.SimulateWorkflowCandidate(t.Context(), disabled, nil, principal); err != nil || result.Message != "Workflow is disabled" {
		t.Fatalf("disabled=%#v err=%v", result, err)
	}
	actionFailure := workflowExecutionSchema()
	actionFailure.Key = "action-failure"
	actionFailure.Graph.Nodes = append(actionFailure.Graph.Nodes, definitionmodel.WorkflowGraphNode{
		ID: "action", Type: "action", Name: "Missing action",
		Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "missing"}},
	})
	actionFailure.Graph.Edges = []definitionmodel.WorkflowGraphEdge{{ID: "start-action", Source: "start", Target: "action", Branch: "success"}}
	if _, err := service.SimulateWorkflowCandidate(t.Context(), actionFailure, nil, principal); apperror.CodeOf(err) != "backend.workflow.action_not_found" {
		t.Fatalf("action simulation=%v", err)
	}
	if result, err := service.SimulateWorkflowCandidate(t.Context(), workflowExecutionSchema(), nil, principal); err != nil || !result.WouldExecute || len(result.Nodes) != 1 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}
