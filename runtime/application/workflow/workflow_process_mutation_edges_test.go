package workflow

import (
	"context"
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type workflowStateDecisionEdgeStub struct {
	decisionErr error
	committed   bool
	commits     []transactionmodel.WorkflowDecisionCommit
	stateErr    error
	states      []transactionmodel.WorkflowStateCommit
}

type workflowDecisionRuntimeEdgeStub struct {
	process workflowmodel.WorkflowProcessInstance
	handled bool
	err     error
}

func (s workflowDecisionRuntimeEdgeStub) DecideTerminalTask(context.Context, string, workflowmodel.WorkflowTaskDecisionRequest, principalmodel.Principal) (workflowmodel.WorkflowProcessInstance, bool, error) {
	return s.process, s.handled, s.err
}

func (s *workflowStateDecisionEdgeStub) CommitWorkflowDecision(_ context.Context, commit transactionmodel.WorkflowDecisionCommit) (bool, error) {
	s.commits = append(s.commits, commit)
	return s.committed, s.decisionErr
}

func (s *workflowStateDecisionEdgeStub) CommitWorkflowState(_ context.Context, commit transactionmodel.WorkflowStateCommit) error {
	s.states = append(s.states, commit)
	return s.stateErr
}

func workflowProcessMutationService(store *workflowProcessStoreEdgeStub, worker *workflowExecutionWorkerStub, decisions *workflowStateDecisionEdgeStub) *WorkflowApplicationService {
	dependencies := WorkflowDependencies{
		Processes: store, Workers: worker, Schema: workflowSchemaProviderEdgeStub{},
		Audit: func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any) {
		},
		AuditMetadata: func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any) {
		},
	}
	if decisions != nil {
		dependencies.Decisions = decisions
	}
	return NewWorkflowApplicationService(dependencies)
}

func workflowMutationProcess(status string) workflowmodel.WorkflowProcessInstance {
	return workflowmodel.WorkflowProcessInstance{
		ID: "process", WorkspaceID: "workspace", InitiatorID: "user", Status: status, ErrorCode: "failure", Result: map[string]any{},
		DefinitionSnapshot: definitionmodel.WorkflowSchema{Key: "flow", Graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "failed", Type: "condition", Contract: &definitionmodel.WorkflowNodeContract{Condition: &definitionmodel.WorkflowConditionContract{Type: "always"}}}}}},
	}
}

func TestCancelWorkflowProcessAuthorizationLookupReplayStateTaskAndSyncOutcomes(t *testing.T) {
	principal := workflowProcessQueryPrincipal()
	process := workflowMutationProcess("waiting")
	base := func() (*workflowProcessStoreEdgeStub, *workflowExecutionWorkerStub) {
		candidate := process
		candidate.Result = map[string]any{}
		return &workflowProcessStoreEdgeStub{workflowExecutionProcessStub: workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{"process": candidate}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{}}, tasks: []workflowmodel.WorkflowTask{{ID: "open", Status: "open"}, {ID: "pending", Status: "pending"}, {ID: "done", Status: "completed"}}}, &workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}
	}
	store, worker := base()
	service := workflowProcessMutationService(store, worker, nil)
	if _, err := service.CancelWorkflowProcess(t.Context(), "process", principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization=%v", err)
	}
	store, worker = base()
	store.getProcessErr = errors.New("get")
	if _, err := workflowProcessMutationService(store, worker, nil).CancelWorkflowProcess(t.Context(), "process", principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("get error=%v", err)
	}
	store, worker = base()
	delete(store.processes, "process")
	if _, err := workflowProcessMutationService(store, worker, nil).CancelWorkflowProcess(t.Context(), "process", principal); apperror.CodeOf(err) != "backend.workflow.process_not_found" {
		t.Fatalf("not found=%v", err)
	}
	store, worker = base()
	other := principal
	other.UserID = "other"
	if _, err := workflowProcessMutationService(store, worker, nil).CancelWorkflowProcess(t.Context(), "process", other); apperror.CodeOf(err) != "backend.workflow.process_cancel_denied" {
		t.Fatalf("denied=%v", err)
	}
	admin := other
	admin = workflowPrincipalWithPermissions(admin, "workflow.process.operate")
	store.processes["process"] = workflowMutationProcess("completed")
	if _, err := workflowProcessMutationService(store, worker, nil).CancelWorkflowProcess(t.Context(), "process", admin); apperror.CodeOf(err) != "backend.workflow.process_not_cancellable" {
		t.Fatalf("state error=%v", err)
	}
	store, worker = base()
	replay := store.processes["process"]
	key := workflowCommandKey("process.cancel", replay.ID, "caller", map[string]any{"command": "cancel"})
	workflowRecordCommand(&replay, "process.cancel", key)
	store.processes["process"] = replay
	if actual, err := workflowProcessMutationService(store, worker, nil).CancelWorkflowProcessWithKey(t.Context(), "process", "caller", principal); err != nil || actual.Status != "waiting" {
		t.Fatalf("replay=%+v err=%v", actual, err)
	}
	store, worker = base()
	store.updateProcessErr = errors.New("update")
	if _, err := workflowProcessMutationService(store, worker, nil).CancelWorkflowProcess(t.Context(), "process", principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("update error=%v", err)
	}
	store, worker = base()
	running := store.processes["process"]
	running.Status = "running"
	store.processes["process"] = running
	if actual, err := workflowProcessMutationService(store, worker, nil).CancelWorkflowProcess(t.Context(), "process", principal); err != nil || actual.Status != "cancelled" {
		t.Fatalf("running cancel=%+v err=%v", actual, err)
	}
	store, worker = base()
	store.updateTaskErr = errors.New("ignored task update")
	cancelled, err := workflowProcessMutationService(store, worker, nil).CancelWorkflowProcess(t.Context(), " process ", principal)
	if err != nil || cancelled.Status != "cancelled" || len(store.updatedTasks) != 2 {
		t.Fatalf("cancelled=%+v tasks=%v err=%v", cancelled, store.updatedTasks, err)
	}
	store, worker = base()
	worker.getErr = errors.New("sync")
	if _, err := workflowProcessMutationService(store, worker, nil).CancelWorkflowProcess(t.Context(), "process", principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("sync error=%v", err)
	}
}

func TestRetryWorkflowProcessContractsReplayFailedNodePersistenceAndExecutionOutcomes(t *testing.T) {
	admin := workflowAdminPrincipal()
	process := workflowMutationProcess("failed")
	base := func() (*workflowProcessStoreEdgeStub, *workflowExecutionWorkerStub) {
		candidate := process
		candidate.Result = map[string]any{}
		return &workflowProcessStoreEdgeStub{workflowExecutionProcessStub: workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{"process": candidate}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{"process": {{NodeID: "ignored", Status: "completed"}, {NodeID: "failed", Status: "failed"}}}}}, &workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	store, worker := base()
	if _, err := workflowProcessMutationService(store, worker, nil).RetryWorkflowProcess(cancelled, "process", admin); err != context.Canceled {
		t.Fatalf("cancel error=%v", err)
	}
	denied := admin
	denied = workflowPrincipalWithPermissions(denied)
	if _, err := workflowProcessMutationService(store, worker, nil).RetryWorkflowProcess(t.Context(), "process", principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization=%v", err)
	}
	if _, err := workflowProcessMutationService(store, worker, nil).RetryWorkflowProcess(t.Context(), "process", denied); apperror.CodeOf(err) != "backend.workflow.process.operate_permission_required" {
		t.Fatalf("permission error=%v", err)
	}
	store.getProcessErr = errors.New("get")
	if _, err := workflowProcessMutationService(store, worker, nil).RetryWorkflowProcess(t.Context(), "process", admin); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("get error=%v", err)
	}
	store, worker = base()
	delete(store.processes, "process")
	if _, err := workflowProcessMutationService(store, worker, nil).RetryWorkflowProcess(t.Context(), "process", admin); apperror.CodeOf(err) != "backend.workflow.process_not_found" {
		t.Fatalf("not found=%v", err)
	}
	store, worker = base()
	nonRetryable := store.processes["process"]
	nonRetryable.Status = "completed"
	store.processes["process"] = nonRetryable
	if _, err := workflowProcessMutationService(store, worker, nil).RetryWorkflowProcess(t.Context(), "process", admin); apperror.CodeOf(err) != "backend.workflow.process_not_retryable" {
		t.Fatalf("state error=%v", err)
	}
	store, worker = base()
	replay := store.processes["process"]
	key := workflowCommandKey("process.retry", replay.ID, "caller", map[string]any{"command": "retry"})
	workflowRecordCommand(&replay, "process.retry", key)
	store.processes["process"] = replay
	if actual, err := workflowProcessMutationService(store, worker, nil).RetryWorkflowProcessWithKey(t.Context(), "process", "caller", admin); err != nil || actual.Status != "failed" {
		t.Fatalf("replay=%+v err=%v", actual, err)
	}
	store, worker = base()
	store.listNodesErr = errors.New("nodes")
	if _, err := workflowProcessMutationService(store, worker, nil).RetryWorkflowProcess(t.Context(), "process", admin); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("nodes error=%v", err)
	}
	store, worker = base()
	store.nodes["process"] = nil
	if _, err := workflowProcessMutationService(store, worker, nil).RetryWorkflowProcess(t.Context(), "process", admin); apperror.CodeOf(err) != "backend.workflow.process_failed_node_missing" {
		t.Fatalf("node missing=%v", err)
	}
	store, worker = base()
	configuration := store.processes["process"]
	configuration.Status = "configuration_error"
	store.processes["process"] = configuration
	store.nodes["process"] = []workflowmodel.WorkflowNodeInstance{{NodeID: "failed", Status: "configuration_error"}, {NodeID: "ignored", Status: "completed"}}
	if actual, err := workflowProcessMutationService(store, worker, nil).RetryWorkflowProcess(t.Context(), "process", admin); err != nil || actual.Status != "completed" {
		t.Fatalf("configuration retry=%+v err=%v", actual, err)
	}
	store, worker = base()
	store.updateProcessErr = errors.New("update")
	if _, err := workflowProcessMutationService(store, worker, nil).RetryWorkflowProcess(t.Context(), "process", admin); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("update error=%v", err)
	}
	store, worker = base()
	processWithNilResult := store.processes["process"]
	processWithNilResult.Result = nil
	store.processes["process"] = processWithNilResult
	retried, err := workflowProcessMutationService(store, worker, nil).RetryWorkflowProcess(t.Context(), "process", admin)
	if err != nil || retried.Status != "completed" || retried.Result["retry_count"] != 1 {
		t.Fatalf("retried=%+v err=%v", retried, err)
	}
}

func TestResolveWorkflowProcessFailureContractsStateStoreAndPersistenceOutcomes(t *testing.T) {
	admin := workflowAdminPrincipal()
	process := workflowMutationProcess("failed")
	base := func() *workflowProcessStoreEdgeStub {
		return &workflowProcessStoreEdgeStub{workflowExecutionProcessStub: workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{"process": process}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{}}}
	}
	store := base()
	if _, err := workflowProcessMutationService(store, &workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, nil).ResolveWorkflowProcessFailure(t.Context(), "process", "note", admin); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("state store error=%v", err)
	}
	denied := admin
	denied = workflowPrincipalWithPermissions(denied)
	decision := &workflowStateDecisionEdgeStub{}
	service := workflowProcessMutationService(store, &workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, decision)
	if _, err := service.ResolveWorkflowProcessFailure(t.Context(), "process", "note", principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization=%v", err)
	}
	if _, err := service.ResolveWorkflowProcessFailure(t.Context(), "process", "note", denied); apperror.CodeOf(err) != "backend.workflow.process.operate_permission_required" {
		t.Fatalf("permission=%v", err)
	}
	if _, err := service.ResolveWorkflowProcessFailure(t.Context(), "process", " ", admin); apperror.CodeOf(err) != "backend.workflow.process_resolution_note_required" {
		t.Fatalf("note=%v", err)
	}
	store.getProcessErr = errors.New("get")
	if _, err := service.ResolveWorkflowProcessFailure(t.Context(), "process", "note", admin); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("get error=%v", err)
	}
	store = base()
	delete(store.processes, "process")
	service = workflowProcessMutationService(store, &workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, decision)
	if _, err := service.ResolveWorkflowProcessFailure(t.Context(), "process", "note", admin); apperror.CodeOf(err) != "backend.workflow.process_not_found" {
		t.Fatalf("not found=%v", err)
	}
	store = base()
	nonResolvable := store.processes["process"]
	nonResolvable.Status = "completed"
	store.processes["process"] = nonResolvable
	service = workflowProcessMutationService(store, &workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, decision)
	if _, err := service.ResolveWorkflowProcessFailure(t.Context(), "process", "note", admin); apperror.CodeOf(err) != "backend.workflow.process_not_resolvable" {
		t.Fatalf("state=%v", err)
	}
	store = base()
	configuration := store.processes["process"]
	configuration.Status = "configuration_error"
	store.processes["process"] = configuration
	service = workflowProcessMutationService(store, &workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, decision)
	if actual, err := service.ResolveWorkflowProcessFailure(t.Context(), "process", "note", admin); err != nil || actual.Status != "resolved" {
		t.Fatalf("configuration resolve=%+v err=%v", actual, err)
	}
	store = base()
	decision.stateErr = errors.New("state")
	service = workflowProcessMutationService(store, &workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, decision)
	if _, err := service.ResolveWorkflowProcessFailure(t.Context(), "process", "note", admin); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("commit error=%v", err)
	}
	decision.stateErr = nil
	resolved, err := service.ResolveWorkflowProcessFailure(t.Context(), "process", " note ", admin)
	if err != nil || resolved.Status != "resolved" || resolved.CompletedAt == "" || len(decision.states) == 0 {
		t.Fatalf("resolved=%+v states=%v err=%v", resolved, decision.states, err)
	}
	completed := process
	completed.CompletedAt = "existing"
	store.processes["process"] = completed
	resolved, err = service.ResolveWorkflowProcessFailure(t.Context(), "process", "note", admin)
	if err != nil || resolved.CompletedAt != "existing" {
		t.Fatalf("completed resolved=%+v err=%v", resolved, err)
	}
}

func TestDecideTaskCancellationAuthorizationIdempotencyAndRuntimeOutcomes(t *testing.T) {
	principal := workflowAdminPrincipal()
	task := workflowmodel.WorkflowTask{ID: "task", ProcessID: "process", AssigneeUserID: principal.UserID}
	process := workflowMutationProcess("waiting")
	request := workflowmodel.WorkflowTaskDecisionRequest{Decision: " APPROVE ", Comment: " ok ", IdempotencyKey: "key"}
	base := func() *workflowProcessStoreEdgeStub {
		return &workflowProcessStoreEdgeStub{workflowExecutionProcessStub: workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{"process": process}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{}}, getTaskValue: task, getTaskFound: true}
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	service := workflowProcessQueryService(base(), nil)
	service.decisions = workflowDecisionRuntimeEdgeStub{handled: true}
	if _, err := service.DecideTask(cancelled, "task", request, principal); err != context.Canceled {
		t.Fatalf("cancel error=%v", err)
	}
	if _, err := service.DecideTask(t.Context(), "task", request, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization=%v", err)
	}
	store := base()
	store.getTaskErr = errors.New("task")
	service = workflowProcessQueryService(store, nil)
	service.decisions = workflowDecisionRuntimeEdgeStub{handled: true}
	if _, err := service.DecideTask(t.Context(), "task", request, principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("task error=%v", err)
	}
	store = base()
	store.getTaskValue.AssigneeUserID = "other"
	service = workflowProcessQueryService(store, nil)
	service.decisions = workflowDecisionRuntimeEdgeStub{handled: true}
	if _, err := service.DecideTask(t.Context(), "task", request, principal); apperror.CodeOf(err) != "backend.workflow.task_assignee_required" {
		t.Fatalf("assignee error=%v", err)
	}
	store = base()
	denied := principal
	denied = workflowPrincipalWithPermissions(denied)
	denied.UserID = task.AssigneeUserID
	service = workflowProcessQueryService(store, nil)
	service.decisions = workflowDecisionRuntimeEdgeStub{handled: true}
	if _, err := service.DecideTask(t.Context(), "task", request, denied); apperror.CodeOf(err) != "backend.workflow.task.act_permission_required" {
		t.Fatalf("permission error=%v", err)
	}
	store = base()
	store.getProcessErr = errors.New("process")
	service = workflowProcessQueryService(store, nil)
	service.decisions = workflowDecisionRuntimeEdgeStub{handled: true}
	if _, err := service.DecideTask(t.Context(), "task", request, principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("process error=%v", err)
	}
	store = base()
	replayed := store.processes["process"]
	key := workflowCommandKey("task.decision", task.ID, request.IdempotencyKey, map[string]any{"decision": "approve", "comment": "ok"})
	workflowRecordCommand(&replayed, "task.decision:"+task.ID, key)
	store.processes["process"] = replayed
	service = workflowProcessQueryService(store, nil)
	service.decisions = workflowDecisionRuntimeEdgeStub{handled: true}
	if actual, err := service.DecideTask(t.Context(), " task ", request, principal); err != nil || actual.ID != "process" {
		t.Fatalf("replay=%+v err=%v", actual, err)
	}
	store = base()
	store.getTaskFound = false
	service = workflowProcessQueryService(store, nil)
	service.decisions = workflowDecisionRuntimeEdgeStub{process: process, handled: true}
	if actual, err := service.DecideTask(t.Context(), "task", request, principal); err != nil || actual.ID != "process" {
		t.Fatalf("handled=%+v err=%v", actual, err)
	}
	store = base()
	fresh := store.processes["process"]
	fresh.Result = map[string]any{}
	store.processes["process"] = fresh
	service = workflowProcessQueryService(store, nil)
	service.decisions = workflowDecisionRuntimeEdgeStub{process: process, handled: true}
	if actual, err := service.DecideTask(t.Context(), "task", request, principal); err != nil || actual.ID != "process" {
		t.Fatalf("nonreplay handled=%+v err=%v", actual, err)
	}
	store = base()
	delete(store.processes, "process")
	service = workflowProcessQueryService(store, nil)
	runtimeErr := errors.New("decision")
	service.decisions = workflowDecisionRuntimeEdgeStub{process: process, handled: true, err: runtimeErr}
	if _, err := service.DecideTask(t.Context(), "task", request, principal); !errors.Is(err, runtimeErr) {
		t.Fatalf("runtime error=%v", err)
	}
	service.decisions = workflowDecisionRuntimeEdgeStub{handled: false}
	if _, err := service.DecideTask(t.Context(), "task", workflowmodel.WorkflowTaskDecisionRequest{}, principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("unhandled error=%v", err)
	}
}
