package workflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/requestcontext"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type workflowFixedClock struct{ now time.Time }

func (c workflowFixedClock) Now() time.Time { return c.now }

type workflowRuntimeWorkerEdgeStub struct {
	workflowExecutionWorkerStub
	list              []workflowmodel.WorkflowExecution
	listErr           error
	whereResults      []bool
	whereErrors       []error
	whereCalls        int
	whereUpdates      []workflowmodel.WorkflowExecution
	whereCalled       chan struct{}
	continuation      workflowmodel.WorkflowProcessInstance
	continuationFound bool
	continuationErr   error
	contextWorkspace  string
}

func (s *workflowRuntimeWorkerEdgeStub) ListExecutions(ctx context.Context, _ string, _ int) ([]workflowmodel.WorkflowExecution, error) {
	s.contextWorkspace = requestcontext.WorkspaceID(ctx)
	return s.list, s.listErr
}

func (s *workflowRuntimeWorkerEdgeStub) UpdateExecutionWhere(_ context.Context, _ string, execution workflowmodel.WorkflowExecution, _ map[string]any) (bool, error) {
	index := s.whereCalls
	s.whereCalls++
	s.whereUpdates = append(s.whereUpdates, execution)
	if s.whereCalled != nil {
		select {
		case s.whereCalled <- struct{}{}:
		default:
		}
	}
	if index < len(s.whereErrors) && s.whereErrors[index] != nil {
		return false, s.whereErrors[index]
	}
	if index < len(s.whereResults) {
		return s.whereResults[index], nil
	}
	return true, nil
}

func (s *workflowRuntimeWorkerEdgeStub) GetProcess(context.Context, string, string) (workflowmodel.WorkflowProcessInstance, bool, error) {
	return s.continuation, s.continuationFound, s.continuationErr
}

func workflowRecordWorkerService(now time.Time, worker *workflowRuntimeWorkerEdgeStub, workflows ...definitionmodel.WorkflowSchema) *WorkflowApplicationService {
	registry := &workflowRegistryStub{items: map[string]definitionmodel.WorkflowSchema{}}
	for _, workflow := range workflows {
		registry.items[workflow.Key] = workflow
	}
	processes := &workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{}}
	dependencies := workerplatform.NewDependencies("workflow-runtime-worker")
	dependencies.Clock = workflowFixedClock{now: now}
	return NewWorkflowApplicationService(WorkflowDependencies{
		Processes: processes, Workers: worker, WorkflowRegistry: registry, Schema: workflowSchemaProviderEdgeStub{},
		ObjectMap: func(context.Context) map[string]definitionmodel.ObjectSchema {
			return map[string]definitionmodel.ObjectSchema{}
		},
		RecordReader: workflowScheduledReaderEdgeStub{}, ActionExists: func(context.Context, string) bool { return true }, Worker: dependencies,
		Audit: func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any) {
		},
		AuditMetadata: func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any) {
		},
	})
}

func workflowDueExecution(now time.Time, workflowKey string) workflowmodel.WorkflowExecution {
	return workflowmodel.WorkflowExecution{ID: "execution", WorkspaceID: "workspace", WorkflowKey: workflowKey, Status: "failed", Attempt: 1, MaxAttempts: 10, NextRunAt: now.Add(-time.Minute).Format(time.RFC3339Nano), UpdatedAt: "updated", Result: map[string]any{}}
}

func workflowWorkerSchema(key string) definitionmodel.WorkflowSchema {
	return definitionmodel.WorkflowSchema{Key: key, Name: key, Enabled: true, Retry: &definitionmodel.WorkflowRetryPolicy{MaxAttempts: 5}, Graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "start", Type: "trigger"}}}}
}

func TestProcessWorkflowContinuationTargetsExactCommittedExecution(t *testing.T) {
	now := time.Now().UTC()
	execution := workflowDueExecution(now, "missing-definition")
	worker := &workflowRuntimeWorkerEdgeStub{
		workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{execution.ID: execution}},
		listErr:                     errors.New("exact continuation must not scan the workspace"),
		whereResults:                []bool{true, true},
	}
	service := workflowRecordWorkerService(now, worker)
	locator := WorkflowContinuationLocator{WorkspaceID: execution.WorkspaceID, ExecutionID: execution.ID}
	WakeWorkflowContinuation(service, locator)
	if got := <-WorkflowContinuationWakeups(service); got != locator {
		t.Fatalf("wakeup=%#v", got)
	}
	result, err := service.ProcessWorkflowContinuation(t.Context(), locator, WorkflowWorkerPrincipal())
	if err != nil || result.Processed != 0 || worker.whereCalls != 2 {
		t.Fatalf("result=%#v where_calls=%d err=%v", result, worker.whereCalls, err)
	}
}

func TestProcessDueWorkflowExecutionsAuthorizationLimitsNilRepositoryListSkipAndClaimOutcomes(t *testing.T) {
	now := time.Now().UTC()
	principal := workflowExecutionPrincipal()
	service := workflowRecordWorkerService(now, nil)
	service.workerRepo = nil
	if _, err := service.ProcessDueWorkflowExecutions(t.Context(), 1, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization=%v", err)
	}
	result, err := service.ProcessDueWorkflowExecutions(t.Context(), 1, principal)
	if err != nil || result.Processed != 0 {
		t.Fatalf("nil repository result=%+v err=%v", result, err)
	}
	worker := &workflowRuntimeWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, listErr: errors.New("list")}
	service = workflowRecordWorkerService(now, worker)
	if _, err := service.ProcessDueWorkflowExecutions(t.Context(), 0, principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("list error=%v", err)
	}
	worker.listErr = nil
	worker.list = []workflowmodel.WorkflowExecution{{ID: "completed", Status: "completed"}}
	if result, err := service.ProcessDueWorkflowExecutions(t.Context(), 600, principal); err != nil || result.Processed != 0 {
		t.Fatalf("skipped result=%+v err=%v", result, err)
	}
	worker.list = []workflowmodel.WorkflowExecution{workflowDueExecution(now, "flow")}
	worker.whereErrors = []error{errors.New("claim")}
	if _, err := service.ProcessDueWorkflowExecutions(t.Context(), 1, principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("claim error=%v", err)
	}
	worker.whereErrors = nil
	worker.whereResults = []bool{false}
	worker.whereCalls = 0
	if result, err := service.ProcessDueWorkflowExecutions(t.Context(), 1, principal); err != nil || result.Processed != 0 {
		t.Fatalf("lost claim result=%+v err=%v", result, err)
	}
}

func TestProcessDueWorkflowExecutionsMissingDefinitionAndMaxAttemptsDeadLetterOutcomes(t *testing.T) {
	now := time.Now().UTC()
	principal := workflowExecutionPrincipal()
	for name, testCase := range map[string]struct {
		workflow     *definitionmodel.WorkflowSchema
		previous     workflowmodel.WorkflowExecution
		whereResults []bool
		whereErrors  []error
		wantErr      bool
	}{
		"missing definition":              {previous: workflowDueExecution(now, "missing"), whereResults: []bool{true, true}},
		"missing definition lease lost":   {previous: workflowDueExecution(now, "missing"), whereResults: []bool{true, false}, wantErr: true},
		"missing definition commit error": {previous: workflowDueExecution(now, "missing"), whereResults: []bool{true}, whereErrors: []error{nil, errors.New("commit")}, wantErr: true},
		"max attempts": {workflow: func() *definitionmodel.WorkflowSchema {
			value := workflowWorkerSchema("flow")
			value.Retry.MaxAttempts = 2
			return &value
		}(), previous: func() workflowmodel.WorkflowExecution {
			value := workflowDueExecution(now, "flow")
			value.Attempt = 3
			value.LastError = ""
			return value
		}(), whereResults: []bool{true, true}},
		"max attempts commit failure": {workflow: func() *definitionmodel.WorkflowSchema {
			value := workflowWorkerSchema("flow")
			value.Retry.MaxAttempts = 2
			return &value
		}(), previous: func() workflowmodel.WorkflowExecution {
			value := workflowDueExecution(now, "flow")
			value.Attempt = 3
			return value
		}(), whereResults: []bool{true, false}, wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			worker := &workflowRuntimeWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, list: []workflowmodel.WorkflowExecution{testCase.previous}, whereResults: testCase.whereResults, whereErrors: testCase.whereErrors}
			workflows := []definitionmodel.WorkflowSchema{}
			if testCase.workflow != nil {
				workflows = append(workflows, *testCase.workflow)
			}
			result, err := workflowRecordWorkerService(now, worker, workflows...).ProcessDueWorkflowExecutions(t.Context(), 1, principal)
			if testCase.wantErr && err == nil {
				t.Fatalf("result=%+v expected error", result)
			}
			if !testCase.wantErr && err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestProcessDueWorkflowExecutionsRetryAndContinuationOutcomes(t *testing.T) {
	now := time.Now().UTC()
	principal := workflowExecutionPrincipal()
	workflow := workflowWorkerSchema("flow")
	previous := workflowDueExecution(now, workflow.Key)
	worker := &workflowRuntimeWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, list: []workflowmodel.WorkflowExecution{previous}, whereResults: []bool{true, true}}
	result, err := workflowRecordWorkerService(now, worker, workflow).ProcessDueWorkflowExecutions(t.Context(), 1, principal)
	if err != nil || result.Processed != 1 || len(result.Executions) != 1 {
		t.Fatalf("retry result=%+v err=%v", result, err)
	}
	invalid := workflow
	invalid.Graph = nil
	worker = &workflowRuntimeWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, list: []workflowmodel.WorkflowExecution{previous}, whereResults: []bool{true}}
	if _, err := workflowRecordWorkerService(now, worker, invalid).ProcessDueWorkflowExecutions(t.Context(), 1, principal); apperror.CodeOf(err) != "backend.workflow.graph_v2_required" {
		t.Fatalf("retry error=%v", err)
	}
	worker = &workflowRuntimeWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, list: []workflowmodel.WorkflowExecution{previous}, whereResults: []bool{true, false}}
	if _, err := workflowRecordWorkerService(now, worker, workflow).ProcessDueWorkflowExecutions(t.Context(), 1, principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("retry commit error=%v", err)
	}

	continuation := workflowMutationProcess("running")
	continuation.CurrentNodeIDs = []string{"failed"}
	resume := workflowDueExecution(now, workflow.Key)
	resume.Result = map[string]any{"resume_process_id": "process", "resume_node_ids": []any{" failed ", nil}}
	worker = &workflowRuntimeWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, list: []workflowmodel.WorkflowExecution{resume}, whereResults: []bool{true, true}, continuation: continuation, continuationFound: true}
	result, err = workflowRecordWorkerService(now, worker, workflow).ProcessDueWorkflowExecutions(t.Context(), 1, principal)
	if err != nil || result.Processed != 1 {
		t.Fatalf("continuation result=%+v err=%v", result, err)
	}
	worker = &workflowRuntimeWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, list: []workflowmodel.WorkflowExecution{resume}, whereResults: []bool{true}, continuationErr: errors.New("process")}
	if _, err := workflowRecordWorkerService(now, worker, workflow).ProcessDueWorkflowExecutions(t.Context(), 1, principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("continuation load error=%v", err)
	}
	worker = &workflowRuntimeWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, list: []workflowmodel.WorkflowExecution{resume}, whereResults: []bool{true, true}}
	if _, err := workflowRecordWorkerService(now, worker, workflow).ProcessDueWorkflowExecutions(t.Context(), 1, principal); err != nil {
		t.Fatal(err)
	}
	worker = &workflowRuntimeWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, list: []workflowmodel.WorkflowExecution{resume}, whereResults: []bool{true, false}}
	if _, err := workflowRecordWorkerService(now, worker, workflow).ProcessDueWorkflowExecutions(t.Context(), 1, principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("missing continuation commit error=%v", err)
	}
	resume.Result = map[string]any{"resume_process_id": "process"}
	worker = &workflowRuntimeWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, list: []workflowmodel.WorkflowExecution{resume}, whereResults: []bool{true, true}, continuation: continuation, continuationFound: true}
	if result, err := workflowRecordWorkerService(now, worker, workflow).ProcessDueWorkflowExecutions(t.Context(), 1, principal); err != nil || result.Processed != 1 {
		t.Fatalf("fallback nodes result=%+v err=%v", result, err)
	}
	failingContinuation := continuation
	failingContinuation.CurrentNodeIDs = []string{"missing"}
	worker = &workflowRuntimeWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, list: []workflowmodel.WorkflowExecution{resume}, whereResults: []bool{true, true}, continuation: failingContinuation, continuationFound: true}
	if result, err := workflowRecordWorkerService(now, worker, workflow).ProcessDueWorkflowExecutions(t.Context(), 1, principal); err != nil || result.Processed != 1 || result.Executions[0].Status != "failed" {
		t.Fatalf("failed continuation result=%+v err=%v", result, err)
	}
	worker = &workflowRuntimeWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, list: []workflowmodel.WorkflowExecution{resume}, whereResults: []bool{true, false}, continuation: failingContinuation, continuationFound: true}
	if _, err := workflowRecordWorkerService(now, worker, workflow).ProcessDueWorkflowExecutions(t.Context(), 1, principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("failed continuation commit error=%v", err)
	}
}

func TestProcessDueWorkflowExecutionsScheduledEarlyReturnPendingAttemptAndLoopLimit(t *testing.T) {
	now := time.Now().UTC()
	principal := workflowExecutionPrincipal()
	scheduled := definitionmodel.WorkflowSchema{Key: "scheduled", Enabled: true, TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "scheduled", ObjectKey: "order"}, Graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "start", Type: "trigger"}}}}
	worker := &workflowRuntimeWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}}
	service := workflowRecordWorkerService(now, worker, scheduled)
	service.schemaMap = func(context.Context) map[string]definitionmodel.ObjectSchema {
		return map[string]definitionmodel.ObjectSchema{"order": {Key: "order"}}
	}
	service.recordReader = workflowScheduledReaderEdgeStub{pages: map[int]recordmodel.RecordPageResult{1: {Items: []recordmodel.Record{{ID: "record", Data: map[string]any{}}}}}}
	result, err := service.ProcessDueWorkflowExecutions(t.Context(), 1, principal)
	if err != nil || result.Processed != 1 {
		t.Fatalf("scheduled result=%+v err=%v", result, err)
	}
	service.recordReader = workflowScheduledReaderEdgeStub{err: errors.New("scheduled")}
	if _, err := service.ProcessDueWorkflowExecutions(t.Context(), 1, principal); err == nil {
		t.Fatal("expected scheduled error")
	}
	workflow := workflowWorkerSchema("flow")
	pending := workflowDueExecution(now, workflow.Key)
	pending.Status, pending.Attempt, pending.NextRunAt = "pending", 0, ""
	continuationWorker := &workflowRuntimeWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, list: []workflowmodel.WorkflowExecution{pending}, whereResults: []bool{true, true}}
	continuationService := workflowRecordWorkerService(now, continuationWorker, scheduled, workflow)
	continuationService.schemaMap = service.schemaMap
	continuationService.recordReader = workflowScheduledReaderEdgeStub{err: errors.New("scheduled definitions must be skipped")}
	if result, err := continuationService.ProcessDueWorkflowContinuations(t.Context(), 1, principal); err != nil || result.Processed != 1 {
		t.Fatalf("continuation-only result=%+v err=%v", result, err)
	}
	if continuationWorker.contextWorkspace != principal.WorkspaceID {
		t.Fatalf("continuation context workspace=%q want %q", continuationWorker.contextWorkspace, principal.WorkspaceID)
	}
	second := pending
	second.ID = "second"
	worker = &workflowRuntimeWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, list: []workflowmodel.WorkflowExecution{pending, second}, whereResults: []bool{true, true}}
	result, err = workflowRecordWorkerService(now, worker, workflow).ProcessDueWorkflowExecutions(t.Context(), 1, principal)
	if err != nil || result.Processed != 1 {
		t.Fatalf("pending result=%+v err=%v", result, err)
	}
	pending.Attempt = 1
	pending.Result["resume_process_id"] = ""
	worker = &workflowRuntimeWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, list: []workflowmodel.WorkflowExecution{pending}, whereResults: []bool{true, true}}
	if result, err := workflowRecordWorkerService(now, worker, workflow).ProcessDueWorkflowExecutions(t.Context(), 1, principal); err != nil || result.Processed != 1 {
		t.Fatalf("pending retry result=%+v err=%v", result, err)
	}
}

func TestWorkflowExecutionHeartbeatRefreshSuccessRepositoryFailureAndLeaseLoss(t *testing.T) {
	now := time.Now().UTC()
	execution := workflowmodel.WorkflowExecution{ID: "execution", WorkspaceID: "workspace", Status: "running", LeaseOwner: "owner", FencingToken: 2}
	for name, testCase := range map[string]struct {
		result  bool
		err     error
		wantErr bool
	}{
		"success":          {result: true},
		"repository error": {err: errors.New("heartbeat"), wantErr: true},
		"lease lost":       {wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			called := make(chan struct{}, 1)
			worker := &workflowRuntimeWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, whereResults: []bool{testCase.result}, whereErrors: []error{testCase.err}, whereCalled: called}
			service := workflowRecordWorkerService(now, worker)
			_, stop := service.workflowExecutionHeartbeatWithInterval(t.Context(), execution, time.Millisecond)
			select {
			case <-called:
			case <-time.After(time.Second):
				t.Fatal("heartbeat not called")
			}
			err := stop()
			if testCase.wantErr && err == nil {
				t.Fatal("expected heartbeat error")
			}
			if !testCase.wantErr && err != nil {
				t.Fatal(err)
			}
			if len(worker.whereUpdates) != 1 || worker.whereUpdates[0].LeaseExpiresAt == "" {
				t.Fatalf("updates=%v", worker.whereUpdates)
			}
			if second := stop(); (second != nil) != (err != nil) {
				t.Fatalf("idempotent stop first=%v second=%v", err, second)
			}
		})
	}
}

func TestProcessDueWorkflowExecutionsContinuationWaitingNodeCommitFailureAndHeartbeatLeaseLoss(t *testing.T) {
	now := time.Now().UTC()
	principal := workflowExecutionPrincipal()
	workflow := workflowWorkerSchema("flow")
	resume := workflowDueExecution(now, workflow.Key)
	resume.Result = map[string]any{"resume_process_id": "process", "resume_node_ids": []string{"approval"}}
	approvalContract := definitionmodel.WorkflowApprovalNodeContract{Mode: "any", Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "users", UserIDs: []string{"approver"}}}}
	continuation := workflowmodel.WorkflowProcessInstance{
		ID: "process", WorkspaceID: "workspace", Status: "running",
		DefinitionSnapshot: definitionmodel.WorkflowSchema{
			Key: "flow",
			Graph: &definitionmodel.WorkflowGraphSchema{
				Version: 2,
				Nodes: []definitionmodel.WorkflowGraphNode{
					{ID: "approval", Type: "approval", Contract: &definitionmodel.WorkflowNodeContract{Approval: &approvalContract}},
				},
			},
		},
	}
	worker := &workflowRuntimeWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, list: []workflowmodel.WorkflowExecution{resume}, whereResults: []bool{true, true}, continuation: continuation, continuationFound: true}
	service := workflowRecordWorkerService(now, worker, workflow)
	processStore := &workflowProcessStoreEdgeStub{workflowExecutionProcessStub: workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{"process": continuation}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{}}}
	service.processEngine.runtime.dependencies.Processes = processStore
	service.processEngine.runtime.dependencies.Identity = workflowIdentityEdgeStub{}
	result, err := service.ProcessDueWorkflowExecutions(t.Context(), 1, principal)
	if err != nil || result.Processed != 1 || result.Executions[0].NodeID != "approval" {
		t.Fatalf("waiting result=%+v err=%v", result, err)
	}

	completed := workflowMutationProcess("running")
	completed.CurrentNodeIDs = nil
	resume.Result = map[string]any{"resume_process_id": "process"}
	worker = &workflowRuntimeWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, list: []workflowmodel.WorkflowExecution{resume}, whereResults: []bool{true, false}, continuation: completed, continuationFound: true}
	if _, err := workflowRecordWorkerService(now, worker, workflow).ProcessDueWorkflowExecutions(t.Context(), 1, principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("continuation commit error=%v", err)
	}

	delayedStore := &workflowProcessStoreEdgeStub{workflowExecutionProcessStub: workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{"process": completed}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{}}, updateProcessDelay: 10 * time.Millisecond}
	worker = &workflowRuntimeWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, list: []workflowmodel.WorkflowExecution{resume}, whereResults: []bool{true, false}, continuation: completed, continuationFound: true}
	service = workflowRecordWorkerService(now, worker, workflow)
	service.workflowHeartbeatInterval = time.Millisecond
	service.processEngine.runtime.dependencies.Processes = delayedStore
	if _, err := service.ProcessDueWorkflowExecutions(t.Context(), 1, principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("continuation heartbeat error=%v", err)
	}

	previous := workflowDueExecution(now, workflow.Key)
	worker = &workflowRuntimeWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, list: []workflowmodel.WorkflowExecution{previous}, whereResults: []bool{true, false}}
	service = workflowRecordWorkerService(now, worker, workflow)
	service.workflowHeartbeatInterval = time.Millisecond
	service.processEngine.runtime.dependencies.Processes = &workflowProcessStoreEdgeStub{workflowExecutionProcessStub: workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{}}, updateProcessDelay: 10 * time.Millisecond}
	if _, err := service.ProcessDueWorkflowExecutions(t.Context(), 1, principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("execution heartbeat error=%v", err)
	}
}
