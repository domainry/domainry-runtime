package workflow

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
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
	scoped = workflowPrincipalWithPermissions(scoped, "workflow.run."+workflow.Key)
	if result, err := service.RunWorkflow(t.Context(), workflow.Key, map[string]any{"source": "portal"}, scoped); err != nil || result.WorkflowKey != workflow.Key {
		t.Fatalf("scoped run result=%#v err=%v", result, err)
	}
	scoped = workflowPrincipalWithPermissions(scoped, "workflow.run.other")
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
	if _, err := service.RunWorkflow(t.Context(), nonManual.Key, nil, principal); apperror.CodeOf(err) != "backend.workflow.entry_mode_invalid" {
		t.Fatalf("manual gate=%v", err)
	}
	invalidManual := workflow
	invalidManual.Key, invalidManual.Graph = "invalid-manual", nil
	service.registry.Set(invalidManual.Key, invalidManual)
	if _, err := service.RunWorkflow(t.Context(), invalidManual.Key, nil, principal); apperror.CodeOf(err) != "backend.workflow.graph_v2_required" {
		t.Fatalf("invalid execution=%v", err)
	}
	result, err := service.RunWorkflow(t.Context(), workflow.Key, map[string]any{"order": "one"}, principal)
	if err != nil || result.WorkflowKey != workflow.Key || result.Execution.ID == "" || result.Status != "completed" {
		t.Fatalf("result=%#v err=%v", result, err)
	}

	if _, err := service.SimulateWorkflow(t.Context(), workflow.Key, nil, unknown); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("unknown simulation=%v", err)
	}
	if _, err := service.SimulateWorkflow(t.Context(), workflow.Key, nil, denied); apperror.CodeOf(err) != "backend.workflow.simulate_permission_required" {
		t.Fatalf("denied simulation=%v", err)
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

func TestWorkflowDraftSimulationContracts(t *testing.T) {
	service := newWorkflowExecutionService(&workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}})
	principal := workflowExecutionPrincipal()
	unknown, denied := principal, principal
	unknown.Known = false
	denied = workflowPrincipalWithPermissions(denied)
	if _, err := service.SimulateWorkflowCandidate(t.Context(), definitionmodel.WorkflowSchema{}, nil, unknown); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("unknown=%v", err)
	}
	if _, err := service.SimulateWorkflowCandidate(t.Context(), definitionmodel.WorkflowSchema{}, nil, denied); apperror.CodeOf(err) != "backend.workflow.simulate_permission_required" {
		t.Fatalf("denied=%v", err)
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
