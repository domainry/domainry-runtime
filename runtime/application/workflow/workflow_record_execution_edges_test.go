package workflow

import (
	"context"
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type workflowCommittedIntentWorkerEdgeStub struct {
	workflowExecutionWorkerStub
	updateWhereResult bool
	updateWhereErr    error
	whereUpdates      []workflowmodel.WorkflowExecution
	claim             workflowmodel.WorkflowExecutionClaimResult
	claims            []workflowmodel.WorkflowExecutionClaimResult
	claimRequests     []workflowmodel.WorkflowExecutionClaimRequest
	claimErr          error
}

func (s *workflowCommittedIntentWorkerEdgeStub) UpdateExecutionWhere(_ context.Context, _ string, execution workflowmodel.WorkflowExecution, _ map[string]any) (bool, error) {
	s.whereUpdates = append(s.whereUpdates, execution)
	return s.updateWhereResult, s.updateWhereErr
}

func (s *workflowCommittedIntentWorkerEdgeStub) TryBeginExecution(_ context.Context, request workflowmodel.WorkflowExecutionClaimRequest) (workflowmodel.WorkflowExecutionClaimResult, error) {
	s.claimRequests = append(s.claimRequests, request)
	if len(s.claims) > 0 {
		claim := s.claims[0]
		s.claims = s.claims[1:]
		return claim, nil
	}
	return s.claim, s.claimErr
}

func workflowRecordExecutionService(worker *workflowCommittedIntentWorkerEdgeStub, workflows ...definitionmodel.WorkflowSchema) *WorkflowApplicationService {
	registry := &workflowRegistryStub{items: map[string]definitionmodel.WorkflowSchema{}}
	for _, workflow := range workflows {
		registry.items[workflow.Key] = workflow
	}
	processes := &workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{}}
	return NewWorkflowApplicationService(WorkflowDependencies{
		Processes: processes, Workers: worker, WorkflowRegistry: registry, Schema: workflowSchemaProviderEdgeStub{},
		ActionExists: func(context.Context, string) bool { return true },
		Audit: func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any) {
		},
		AuditMetadata: func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any) {
		},
	})
}

func workflowRecordTriggerSchema(key string) definitionmodel.WorkflowSchema {
	return definitionmodel.WorkflowSchema{Key: key, Name: key, Enabled: true, TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "record_updated", ObjectKey: "order"}, Graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "start", Type: "trigger"}}}}
}

func TestTriggeredWorkflowsAuthorizationFilteringPrincipalProjectionSortingAndFailure(t *testing.T) {
	worker := &workflowCommittedIntentWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, updateWhereResult: true}
	a, z := workflowRecordTriggerSchema("a"), workflowRecordTriggerSchema("z")
	disabled := workflowRecordTriggerSchema("disabled")
	disabled.Enabled = false
	unmatched := workflowRecordTriggerSchema("unmatched")
	unmatched.TriggerContract.ObjectKey = "other"
	service := workflowRecordExecutionService(worker, z, disabled, unmatched, a)
	record := recordmodel.Record{ID: "record", Data: map[string]any{"value": true}}
	if _, err := service.TriggeredWorkflows(t.Context(), "order", record, principalmodel.Principal{}, "record_updated:order"); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization=%v", err)
	}
	principal := workflowExecutionPrincipal()
	principal.RequestID = "request"
	summaries, err := service.TriggeredWorkflows(t.Context(), "order", record, principal, "record_updated:order")
	if err != nil || len(summaries) != 2 || summaries[0].WorkflowKey != "a" {
		t.Fatalf("summaries=%v err=%v", summaries, err)
	}
	if summaries, err := service.triggeredWorkflowsWithChange(t.Context(), "order", record, nil, principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace"}}, "record_updated:order"); err != nil || len(summaries) != 2 {
		t.Fatalf("minimal summaries=%v err=%v", summaries, err)
	}
	invalid := workflowRecordTriggerSchema("invalid")
	invalid.Graph = nil
	service.registry.Set(invalid.Key, invalid)
	if _, err := service.triggeredWorkflows(t.Context(), "order", record, principal, "record_updated:order"); apperror.CodeOf(err) != "backend.workflow.graph_v2_required" {
		t.Fatalf("execution error=%v", err)
	}
}

func TestExecuteCommittedWorkflowIntentsLifecycleClaimFailureMissingDefinitionAndOutcomes(t *testing.T) {
	principal := workflowExecutionPrincipal()
	valid := workflowRecordTriggerSchema("valid")
	invalid := workflowRecordTriggerSchema("invalid")
	invalid.Graph = nil
	intent := workflowmodel.WorkflowExecution{ID: "intent", WorkflowKey: "valid", Status: "pending", Trigger: "record_updated:order", Payload: map[string]any{"object_key": "order", "record_id": "record"}, Result: map[string]any{}, UpdatedAt: "updated"}
	worker := &workflowCommittedIntentWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, updateWhereResult: true}
	service := workflowRecordExecutionService(worker, valid, invalid)
	service.executeCommittedWorkflowIntents(nil, []workflowmodel.WorkflowExecution{intent}, principal)
	if len(worker.whereUpdates) != 0 {
		t.Fatal("nil context claimed intent")
	}
	service.executeCommittedWorkflowIntents(t.Context(), []workflowmodel.WorkflowExecution{{ID: "missing", WorkflowKey: "missing"}}, principal)
	if len(worker.whereUpdates) != 0 {
		t.Fatal("missing definition claimed")
	}
	worker.updateWhereErr = errors.New("claim")
	service.executeCommittedWorkflowIntents(t.Context(), []workflowmodel.WorkflowExecution{intent}, principal)
	worker.updateWhereErr = nil
	worker.updateWhereResult = false
	service.executeCommittedWorkflowIntents(t.Context(), []workflowmodel.WorkflowExecution{intent}, principal)
	if len(worker.updated) != 0 {
		t.Fatal("failed claims persisted")
	}
	worker.updateWhereResult = true
	failed := intent
	failed.WorkflowKey = "invalid"
	service.executeCommittedWorkflowIntents(t.Context(), []workflowmodel.WorkflowExecution{failed}, principal)
	if len(worker.updated) != 1 || worker.updated[0].Status != "failed" {
		t.Fatalf("failed updates=%v", worker.updated)
	}
	service.executeCommittedWorkflowIntents(t.Context(), []workflowmodel.WorkflowExecution{intent}, principal)
	if len(worker.updated) != 2 || worker.updated[1].Status != "skipped" || worker.updated[1].Result["continued_execution_id"] == nil {
		t.Fatalf("updates=%v", worker.updated)
	}
	service.ExecuteCommittedWorkflowIntents(t.Context(), []workflowmodel.WorkflowExecution{intent}, principalmodel.Principal{})
	if len(worker.updated) != 2 {
		t.Fatal("unauthorized wrapper executed intents")
	}
	service.ExecuteCommittedWorkflowIntents(t.Context(), nil, principal)
}

func TestExecuteWorkflowAuthorizationGraphValidationAndSuccess(t *testing.T) {
	worker := &workflowCommittedIntentWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, updateWhereResult: true}
	service := workflowRecordExecutionService(worker)
	valid := workflowRecordTriggerSchema("valid")
	if _, err := service.ExecuteWorkflow(t.Context(), valid, nil, principalmodel.Principal{}, "manual"); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization=%v", err)
	}
	invalid := valid
	invalid.Graph = nil
	if _, err := service.ExecuteWorkflow(t.Context(), invalid, nil, workflowExecutionPrincipal(), "manual"); apperror.CodeOf(err) != "backend.workflow.graph_v2_required" {
		t.Fatalf("graph error=%v", err)
	}
	invalid.Graph = &definitionmodel.WorkflowGraphSchema{Version: 1, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "start", Type: "trigger"}}}
	if _, err := service.ExecuteWorkflow(t.Context(), invalid, nil, workflowExecutionPrincipal(), "manual"); apperror.CodeOf(err) != "backend.workflow.graph_v2_required" {
		t.Fatalf("version error=%v", err)
	}
	invalid.Graph = &definitionmodel.WorkflowGraphSchema{Version: 2}
	if _, err := service.ExecuteWorkflow(t.Context(), invalid, nil, workflowExecutionPrincipal(), "manual"); apperror.CodeOf(err) != "backend.workflow.graph_v2_required" {
		t.Fatalf("empty graph error=%v", err)
	}
	execution, err := service.ExecuteWorkflow(t.Context(), valid, nil, workflowExecutionPrincipal(), "manual")
	if err != nil || execution.Status != "completed" {
		t.Fatalf("execution=%+v err=%v", execution, err)
	}
}
