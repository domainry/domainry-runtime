package workflow

import (
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestRunCommittedWorkflowContinuationClaimLoadRunAndCommitOutcomes(t *testing.T) {
	principal := workflowAdminPrincipal()
	condition := definitionmodel.WorkflowGraphNode{ID: "condition", Type: "condition", Contract: &definitionmodel.WorkflowNodeContract{Condition: &definitionmodel.WorkflowConditionContract{Type: "always"}}}
	process := workflowEngineProcess([]definitionmodel.WorkflowGraphNode{condition}, nil)
	process.WorkspaceID = principal.WorkspaceID
	execution := workflowmodel.WorkflowExecution{ID: "execution", WorkspaceID: principal.WorkspaceID, ProcessID: process.ID, WorkflowKey: process.WorkflowKey, Status: "pending", UpdatedAt: "old", Result: map[string]any{"resume_node_ids": []string{"condition"}}}
	makeRuntime := func(worker *workflowRuntimeWorkerEdgeStub, store *workflowProcessStoreEdgeStub) *WorkflowProcessRuntime {
		return NewWorkflowProcessRuntime(WorkflowDependencies{Processes: store, Workers: worker, Schema: workflowSchemaProviderEdgeStub{}})
	}
	baseStore := func() *workflowProcessStoreEdgeStub {
		return &workflowProcessStoreEdgeStub{workflowExecutionProcessStub: workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{process.ID: process}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{}}}
	}
	worker := &workflowRuntimeWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{"execution": execution}}, whereResults: []bool{true}, continuation: process, continuationFound: true}
	continued, claimed, err := runCommittedWorkflowContinuation(t.Context(), makeRuntime(worker, baseStore()), execution, principal, process, nil, nil, workflowmodel.WorkflowTask{})
	if err != nil || !claimed || continued.Status != "completed" || len(worker.updated) != 1 {
		t.Fatalf("continued=%+v claimed=%v updates=%v err=%v", continued, claimed, worker.updated, err)
	}
	emptyResume := execution
	emptyResume.Result = map[string]any{}
	resumeProcess := process
	resumeProcess.CurrentNodeIDs = []string{"condition"}
	worker = &workflowRuntimeWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{"execution": emptyResume}}, whereResults: []bool{true}, continuation: resumeProcess, continuationFound: true}
	if actual, claimed, err := runCommittedWorkflowContinuation(t.Context(), makeRuntime(worker, baseStore()), emptyResume, principal, process, nil, nil, workflowmodel.WorkflowTask{}); err != nil || !claimed || actual.Status != "completed" {
		t.Fatalf("cursor fallback actual=%+v claimed=%v err=%v", actual, claimed, err)
	}
	worker = &workflowRuntimeWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, whereErrors: []error{errors.New("claim")}}
	if _, _, err := runCommittedWorkflowContinuation(t.Context(), makeRuntime(worker, baseStore()), execution, principal, process, nil, nil, workflowmodel.WorkflowTask{}); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("claim error=%v", err)
	}
	worker = &workflowRuntimeWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, whereResults: []bool{false}}
	if _, claimed, err := runCommittedWorkflowContinuation(t.Context(), makeRuntime(worker, baseStore()), execution, principal, process, nil, nil, workflowmodel.WorkflowTask{}); err != nil || claimed {
		t.Fatalf("lost claim=%v err=%v", claimed, err)
	}
	worker = &workflowRuntimeWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, whereResults: []bool{true}, continuationErr: errors.New("load")}
	if _, claimed, err := runCommittedWorkflowContinuation(t.Context(), makeRuntime(worker, baseStore()), execution, principal, process, nil, nil, workflowmodel.WorkflowTask{}); !claimed || apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("load claimed=%v err=%v", claimed, err)
	}
	worker = &workflowRuntimeWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, whereResults: []bool{true}, continuationFound: false}
	if _, claimed, err := runCommittedWorkflowContinuation(t.Context(), makeRuntime(worker, baseStore()), execution, principal, process, nil, nil, workflowmodel.WorkflowTask{}); !claimed || apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("missing continuation claimed=%v err=%v", claimed, err)
	}
	worker = &workflowRuntimeWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}, updateErr: errors.New("update")}, whereResults: []bool{true}, continuation: process, continuationFound: true}
	if _, claimed, err := runCommittedWorkflowContinuation(t.Context(), makeRuntime(worker, baseStore()), execution, principal, process, nil, nil, workflowmodel.WorkflowTask{}); !claimed || apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("update claimed=%v err=%v", claimed, err)
	}

	failing := process
	failing.DefinitionSnapshot.Graph.Nodes = []definitionmodel.WorkflowGraphNode{{ID: "bad", Type: "bad"}}
	execution.Result = map[string]any{"resume_node_ids": []string{"bad"}}
	decisions := &workflowStateDecisionEdgeStub{}
	store := baseStore()
	worker = &workflowRuntimeWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, whereResults: []bool{true}, continuation: failing, continuationFound: true}
	runtime := NewWorkflowProcessRuntime(WorkflowDependencies{Processes: store, Workers: worker, Decisions: decisions, WorkflowRegistry: &workflowRegistryStub{items: map[string]definitionmodel.WorkflowSchema{}}, Schema: workflowSchemaProviderEdgeStub{}})
	if _, claimed, err := runCommittedWorkflowContinuation(t.Context(), runtime, execution, principal, process, nil, nil, workflowmodel.WorkflowTask{}); !claimed || err == nil || len(decisions.states) != 1 {
		t.Fatalf("recovery claimed=%v states=%v err=%v", claimed, decisions.states, err)
	}
	waitingNode := definitionmodel.WorkflowGraphNode{ID: "approval", Type: "approval", Contract: &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "users", UserIDs: []string{"approver"}}}}}}
	waitingProcess := workflowEngineProcess([]definitionmodel.WorkflowGraphNode{waitingNode}, nil)
	waitingProcess.WorkspaceID = principal.WorkspaceID
	waitingExecution := execution
	waitingExecution.Result = map[string]any{"resume_node_ids": []string{"approval"}}
	worker = &workflowRuntimeWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{"execution": waitingExecution}}, whereResults: []bool{true}, continuation: waitingProcess, continuationFound: true}
	waitingStore := baseStore()
	waitingRuntime := NewWorkflowProcessRuntime(WorkflowDependencies{Processes: waitingStore, Workers: worker, Identity: workflowApprovalIdentityStub{users: map[string]identitysdk.User{}}, Schema: workflowSchemaProviderEdgeStub{}})
	if actual, claimed, err := runCommittedWorkflowContinuation(t.Context(), waitingRuntime, waitingExecution, principal, process, nil, nil, workflowmodel.WorkflowTask{}); err != nil || !claimed || actual.Status != "waiting" || worker.updated[0].NodeID != "approval" {
		t.Fatalf("waiting actual=%+v claimed=%v updated=%v err=%v", actual, claimed, worker.updated, err)
	}
}

func TestExecuteWorkflowGraphProcessReplayStartAndPersistenceOutcomes(t *testing.T) {
	principal := workflowAdminPrincipal()
	workflow := definitionmodel.WorkflowSchema{Key: "flow", Name: "Flow", Graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "start", Type: "trigger"}}}}
	makeService := func(store *workflowProcessStoreEdgeStub, worker *workflowIdempotencyWorkerEdgeStub) *WorkflowApplicationService {
		return NewWorkflowApplicationService(WorkflowDependencies{Processes: store, Workers: worker, Schema: workflowSchemaProviderEdgeStub{}})
	}
	baseStore := func() *workflowProcessStoreEdgeStub {
		return &workflowProcessStoreEdgeStub{workflowExecutionProcessStub: workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{}}}
	}
	worker := &workflowIdempotencyWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}}
	execution, err := makeService(baseStore(), worker).executeWorkflowGraphProcess(t.Context(), workflow, nil, principal, "manual", 1, true)
	if err != nil || execution.Status != "completed" || len(worker.inserted) != 1 {
		t.Fatalf("execution=%+v inserts=%v err=%v", execution, worker.inserted, err)
	}
	replay := workflowmodel.WorkflowExecution{ID: "replay", Status: "completed"}
	workflow.IdempotencyKeys = []string{"idempotency_key"}
	worker = &workflowIdempotencyWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{"replay": replay}}, claim: workflowmodel.WorkflowExecutionClaimResult{Decision: idempotency.DecisionReplay, Receipt: workflowmodel.WorkflowExecutionReceipt{ID: "receipt", WorkspaceID: principal.WorkspaceID, ExecutionID: "replay"}}}
	replayed, err := makeService(baseStore(), worker).executeWorkflowGraphProcess(t.Context(), workflow, map[string]any{"idempotency_key": "key"}, principal, "manual", 1, false)
	if err != nil || replayed.ID != "replay" {
		t.Fatalf("replayed=%+v err=%v", replayed, err)
	}
	worker.claimErr = errors.New("claim")
	if _, err := makeService(baseStore(), worker).executeWorkflowGraphProcess(t.Context(), workflow, map[string]any{"idempotency_key": "key"}, principal, "manual", 1, false); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("claim error=%v", err)
	}
	invalidWorkflow := definitionmodel.WorkflowSchema{Key: "invalid"}
	worker = &workflowIdempotencyWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}}
	if _, err := makeService(baseStore(), worker).executeWorkflowGraphProcess(t.Context(), invalidWorkflow, nil, principal, "manual", 1, true); err == nil {
		t.Fatal("expected start error without durable process")
	}

	store := baseStore()
	store.insertNodeErr = errors.New("node")
	worker = &workflowIdempotencyWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}}
	failed, err := makeService(store, worker).executeWorkflowGraphProcess(t.Context(), workflow, nil, principal, "manual", 1, true)
	if err != nil || failed.Status != "failed" || len(worker.inserted) != 1 {
		t.Fatalf("failed=%+v inserts=%v err=%v", failed, worker.inserted, err)
	}
	store = baseStore()
	store.insertNodeErr = errors.New("node")
	worker = &workflowIdempotencyWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}, insertErr: errors.New("execution")}}
	if _, err := makeService(store, worker).executeWorkflowGraphProcess(t.Context(), workflow, nil, principal, "manual", 1, true); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("failed insert=%v", err)
	}
	worker = &workflowIdempotencyWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}, insertErr: errors.New("execution")}}
	if _, err := makeService(baseStore(), worker).executeWorkflowGraphProcess(t.Context(), workflow, nil, principal, "manual", 1, true); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("success insert=%v", err)
	}
	claim := workflowmodel.WorkflowExecutionClaimResult{Decision: idempotency.DecisionAcquired, Receipt: workflowmodel.WorkflowExecutionReceipt{ID: "receipt", WorkspaceID: principal.WorkspaceID, WorkflowKey: workflow.Key, LeaseOwner: "owner", FencingToken: 1}}
	worker = &workflowIdempotencyWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, claim: claim, completionErr: errors.New("receipt")}
	if _, err := makeService(baseStore(), worker).executeWorkflowGraphProcess(t.Context(), workflow, map[string]any{"idempotency_key": "key"}, principal, "manual", 1, false); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("success receipt=%v", err)
	}
	store = baseStore()
	store.insertNodeErr = errors.New("node")
	worker = &workflowIdempotencyWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, claim: claim, completionErr: errors.New("receipt")}
	if _, err := makeService(store, worker).executeWorkflowGraphProcess(t.Context(), workflow, map[string]any{"idempotency_key": "key"}, principal, "manual", 1, false); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("failed receipt=%v", err)
	}
}
