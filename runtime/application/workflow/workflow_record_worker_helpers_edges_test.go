package workflow

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type workflowScheduledReaderEdgeStub struct {
	pages map[int]recordmodel.RecordPageResult
	err   error
}

func (s workflowScheduledReaderEdgeStub) GetWorkflowRecord(context.Context, string, definitionmodel.ObjectSchema, string, principalmodel.Principal) (recordmodel.Record, bool, error) {
	return recordmodel.Record{}, false, nil
}

func (s workflowScheduledReaderEdgeStub) ListWorkflowRecords(_ context.Context, _ string, _ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery, _ principalmodel.Principal) (recordmodel.RecordPageResult, error) {
	return s.pages[query.Page], s.err
}

func TestWorkflowRecordWorkerCASLookupAndNodeIDHelpers(t *testing.T) {
	worker := &workflowCommittedIntentWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, updateWhereResult: true}
	service := &WorkflowApplicationService{workerRepo: worker}
	execution := workflowmodel.WorkflowExecution{ID: "execution", WorkspaceID: "workspace", LeaseOwner: "owner", LeaseExpiresAt: "expires", FencingToken: 2}
	if err := service.commitClaimedWorkflowExecution(t.Context(), execution); err != nil || len(worker.whereUpdates) != 1 || worker.whereUpdates[0].LeaseOwner != "" {
		t.Fatalf("updates=%v err=%v", worker.whereUpdates, err)
	}
	worker.updateWhereResult = false
	if err := service.commitClaimedWorkflowExecution(t.Context(), execution); err == nil || err.Error() != "backend.workflow.execution_lease_lost" {
		t.Fatalf("lease error=%v", err)
	}
	worker.updateWhereErr = errors.New("update")
	if err := service.commitClaimedWorkflowExecution(t.Context(), execution); !errors.Is(err, worker.updateWhereErr) {
		t.Fatalf("update error=%v", err)
	}
	worker.updateWhereErr = nil
	worker.updateWhereResult = true
	updated, err := service.updateWorkflowExecutionIfCurrent(t.Context(), execution, map[string]any{"status": "running"})
	if err != nil || !updated {
		t.Fatalf("updated=%v err=%v", updated, err)
	}
	workflows := []definitionmodel.WorkflowSchema{{Key: "a"}, {Key: "b"}}
	if workflow, found := workflowByKey(workflows, "b"); !found || workflow.Key != "b" {
		t.Fatalf("workflow=%+v found=%v", workflow, found)
	}
	if _, found := workflowByKey(workflows, "missing"); found {
		t.Fatal("missing workflow found")
	}
	if values := workflowNodeIDsFromAny([]string{" b ", "", "a", "b"}); !reflect.DeepEqual(values, []string{"a", "b"}) {
		t.Fatalf("string nodes=%v", values)
	}
	if values := workflowNodeIDsFromAny([]any{" b ", nil, "a", ""}); !reflect.DeepEqual(values, []string{"a", "b"}) {
		t.Fatalf("any nodes=%v", values)
	}
	if values := workflowNodeIDsFromAny("invalid"); len(values) != 0 {
		t.Fatalf("invalid nodes=%v", values)
	}
}

func TestProcessScheduledWorkflowExecutionsFilteringPagingConditionsLimitsAndFailures(t *testing.T) {
	now := time.Now().UTC()
	principal := workflowExecutionPrincipal()
	scheduled := definitionmodel.WorkflowSchema{Key: "scheduled", Name: "Scheduled", Enabled: true, TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "scheduled", ObjectKey: "order"}, Graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "start", Type: "trigger"}}}}
	scheduled.TriggerContract.ObjectKeys = []string{"order-2"}
	scheduledB := scheduled
	scheduledB.Key = "scheduled-b"
	disabled := scheduled
	disabled.Key, disabled.Enabled = "disabled", false
	manual := scheduled
	manual.Key, manual.TriggerContract = "manual", &definitionmodel.WorkflowTriggerContract{Type: "manual", ObjectKey: "order"}
	noObject := scheduled
	noObject.Key, noObject.TriggerContract = "no-object", &definitionmodel.WorkflowTriggerContract{Type: "scheduled"}
	worker := &workflowCommittedIntentWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, updateWhereResult: true}
	registry := &workflowRegistryStub{items: map[string]definitionmodel.WorkflowSchema{scheduled.Key: scheduled, scheduledB.Key: scheduledB, disabled.Key: disabled, manual.Key: manual, noObject.Key: noObject}}
	reader := workflowScheduledReaderEdgeStub{pages: map[int]recordmodel.RecordPageResult{
		1: {Items: []recordmodel.Record{{ID: "one", Data: map[string]any{"include": true}}, {ID: "two", Data: map[string]any{"include": true}}, {ID: "limit", Data: map[string]any{"include": true}}}, HasNext: true},
		2: {Items: []recordmodel.Record{{ID: "three", Data: map[string]any{"include": true}}}},
	}}
	service := NewWorkflowApplicationService(WorkflowDependencies{
		Processes: &workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{}},
		Workers:   worker, WorkflowRegistry: registry, RecordReader: reader,
		ObjectMap: func(context.Context) map[string]definitionmodel.ObjectSchema {
			return map[string]definitionmodel.ObjectSchema{"order": {Key: "order"}}
		},
		Schema: workflowSchemaProviderEdgeStub{}, ActionExists: func(context.Context, string) bool { return true },
		Worker: workerplatform.NewDependencies("scheduled-test"),
		Audit: func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any) {
		},
		AuditMetadata: func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any) {
		},
	})
	if processed, err := service.processScheduledWorkflowExecutions(t.Context(), 0, principal, now); err != nil || len(processed) != 0 {
		t.Fatalf("zero processed=%v err=%v", processed, err)
	}
	processed, err := service.processScheduledWorkflowExecutionsForTarget(t.Context(), scheduledB.Key, 2, principal, now)
	if err != nil || len(processed) != 2 || processed[0].WorkflowKey != scheduledB.Key || processed[1].WorkflowKey != scheduledB.Key {
		t.Fatalf("targeted processed=%v err=%v", processed, err)
	}
	processed, err = service.processScheduledWorkflowExecutions(t.Context(), 2, principal, now)
	if err != nil || len(processed) != 2 {
		t.Fatalf("processed=%v err=%v", processed, err)
	}
	service.schemaMap = func(context.Context) map[string]definitionmodel.ObjectSchema {
		return map[string]definitionmodel.ObjectSchema{}
	}
	if processed, err := service.processScheduledWorkflowExecutions(t.Context(), 2, principal, now); err != nil || len(processed) != 1 || processed[0].WorkflowKey != noObject.Key {
		t.Fatalf("global workflow with unavailable record-scoped objects processed=%v err=%v", processed, err)
	}
	service.schemaMap = func(context.Context) map[string]definitionmodel.ObjectSchema {
		return map[string]definitionmodel.ObjectSchema{"order": {Key: "order"}}
	}
	service.recordReader = workflowScheduledReaderEdgeStub{err: errors.New("records")}
	if _, err := service.processScheduledWorkflowExecutions(t.Context(), 2, principal, now); err == nil {
		t.Fatal("expected record list error")
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.processScheduledWorkflowExecutions(cancelled, 2, principal, now); err != context.Canceled {
		t.Fatalf("cancel error=%v", err)
	}
	conditioned := scheduled
	conditioned.ConditionContract = &definitionmodel.WorkflowConditionContract{Type: "field_equals", Field: "include", Value: true}
	registry.items = map[string]definitionmodel.WorkflowSchema{conditioned.Key: conditioned}
	service.recordReader = workflowScheduledReaderEdgeStub{pages: map[int]recordmodel.RecordPageResult{1: {Items: []recordmodel.Record{{ID: "skip", Data: map[string]any{"include": false}}, {ID: "run", Data: map[string]any{"include": true}}}}}}
	processed, err = service.processScheduledWorkflowExecutions(t.Context(), 5, principal, now)
	if err != nil || len(processed) != 1 {
		t.Fatalf("condition processed=%v err=%v", processed, err)
	}
	invalidScheduled := scheduled
	invalidScheduled.Key, invalidScheduled.Graph, invalidScheduled.TriggerContract.ObjectKeys = "invalid", nil, nil
	registry.items = map[string]definitionmodel.WorkflowSchema{invalidScheduled.Key: invalidScheduled}
	service.recordReader = workflowScheduledReaderEdgeStub{pages: map[int]recordmodel.RecordPageResult{1: {Items: []recordmodel.Record{{ID: "record", Data: map[string]any{}}}}}}
	if _, err := service.processScheduledWorkflowExecutions(t.Context(), 1, principal, now); err == nil {
		t.Fatal("expected scheduled execution error")
	}
	duplicate := scheduled
	duplicate.Key, duplicate.IdempotencyKeys, duplicate.TriggerContract.ObjectKeys = "duplicate", []string{"record_id"}, nil
	registry.items = map[string]definitionmodel.WorkflowSchema{duplicate.Key: duplicate}
	worker.claim = workflowmodel.WorkflowExecutionClaimResult{Decision: idempotency.DecisionReplay, Receipt: workflowmodel.WorkflowExecutionReceipt{ID: "receipt", WorkspaceID: principal.WorkspaceID, ExecutionID: "duplicate-execution"}}
	worker.executions["duplicate-execution"] = workflowmodel.WorkflowExecution{ID: "duplicate-execution", Status: "duplicate"}
	processed, err = service.processScheduledWorkflowExecutions(t.Context(), 1, principal, now)
	if err != nil || len(processed) != 0 {
		t.Fatalf("duplicate processed=%v err=%v", processed, err)
	}

	isolated := scheduled
	isolated.Key, isolated.IdempotencyKeys, isolated.TriggerContract.ObjectKeys = "isolated", []string{"record_id", "scheduled_at"}, nil
	registry.items = map[string]definitionmodel.WorkflowSchema{isolated.Key: isolated}
	service.recordReader = workflowScheduledReaderEdgeStub{pages: map[int]recordmodel.RecordPageResult{1: {Items: []recordmodel.Record{{ID: "conflict", Data: map[string]any{}}, {ID: "fresh", Data: map[string]any{}}}}}}
	worker.claims = []workflowmodel.WorkflowExecutionClaimResult{
		{Decision: idempotency.DecisionFingerprintConflict},
		{Decision: idempotency.DecisionAcquired},
	}
	processed, err = service.processScheduledWorkflowExecutionsForTarget(t.Context(), isolated.Key, 1, principal, now)
	if err != nil || len(processed) != 1 || processed[0].RecordID != "fresh" {
		t.Fatalf("isolated processed=%v err=%v", processed, err)
	}
}

func TestScheduledWorkflowWindowMakesRetryReplayAndBusinessExecutionExactlyOnce(t *testing.T) {
	now := time.Date(2026, 8, 12, 8, 1, 0, 0, time.UTC)
	window := now.Add(-time.Minute)
	principal := workflowExecutionPrincipal()
	workflow := definitionmodel.WorkflowSchema{
		Key: "activate", Name: "Activate", Enabled: true,
		IdempotencyKeys: []string{"record_id", "scheduled_at"},
		TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "scheduled", ObjectKey: "candidate"},
		Graph:           &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "start", Type: "trigger"}}},
	}
	worker := &workflowCommittedIntentWorkerEdgeStub{
		workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}},
		updateWhereResult:           true,
		claim: workflowmodel.WorkflowExecutionClaimResult{Decision: idempotency.DecisionAcquired, Receipt: workflowmodel.WorkflowExecutionReceipt{
			ID: "receipt-1", WorkspaceID: principal.WorkspaceID, WorkflowKey: workflow.Key, LeaseOwner: "worker", FencingToken: 1,
		}},
	}
	service := workflowRecordExecutionService(worker, workflow)
	service.recordReader = workflowScheduledReaderEdgeStub{pages: map[int]recordmodel.RecordPageResult{1: {Items: []recordmodel.Record{{ID: "candidate-1", Data: map[string]any{"status": "approved"}}}}}}
	service.schemaMap = func(context.Context) map[string]definitionmodel.ObjectSchema {
		return map[string]definitionmodel.ObjectSchema{"candidate": {Key: "candidate"}}
	}

	first, err := service.processScheduledWorkflowExecutionsForTargetWindow(t.Context(), "scheduled:activate", 1, principal, now, window)
	if err != nil || len(first) != 1 || len(worker.inserted) != 1 {
		t.Fatalf("first=%+v inserted=%d err=%v", first, len(worker.inserted), err)
	}
	firstExecution := first[0]
	worker.claim = workflowmodel.WorkflowExecutionClaimResult{Decision: idempotency.DecisionReplay, Receipt: workflowmodel.WorkflowExecutionReceipt{
		ID: "receipt-1", WorkspaceID: principal.WorkspaceID, WorkflowKey: workflow.Key, ExecutionID: firstExecution.ID,
	}}
	replay, err := service.processScheduledWorkflowExecutionsForTargetWindow(t.Context(), "scheduled:activate", 1, principal, now, window)
	if err != nil || len(replay) != 0 || len(worker.inserted) != 1 {
		t.Fatalf("replay=%+v inserted=%d err=%v", replay, len(worker.inserted), err)
	}
	if len(worker.claimRequests) != 2 || worker.claimRequests[0].RequestFingerprint != worker.claimRequests[1].RequestFingerprint {
		t.Fatalf("retry fingerprints=%+v", worker.claimRequests)
	}

	worker.claim = workflowmodel.WorkflowExecutionClaimResult{Decision: idempotency.DecisionAcquired, Receipt: workflowmodel.WorkflowExecutionReceipt{
		ID: "receipt-2", WorkspaceID: principal.WorkspaceID, WorkflowKey: workflow.Key, LeaseOwner: "worker", FencingToken: 1,
	}}
	next, err := service.processScheduledWorkflowExecutionsForTargetWindow(t.Context(), "scheduled:activate", 1, principal, now, window.Add(time.Minute))
	if err != nil || len(next) != 1 || len(worker.inserted) != 2 {
		t.Fatalf("next=%+v inserted=%d err=%v", next, len(worker.inserted), err)
	}
	if worker.claimRequests[2].RequestFingerprint == worker.claimRequests[0].RequestFingerprint {
		t.Fatal("distinct interval windows collapsed to the same workflow idempotency fingerprint")
	}
}

func TestProcessScheduledWorkflowExecutionsRunsGlobalActionWorkflowOnceAndRejectsMissingTarget(t *testing.T) {
	now := time.Date(2026, 8, 12, 8, 0, 0, 0, time.UTC)
	principal := workflowExecutionPrincipal()
	workflow := definitionmodel.WorkflowSchema{
		Key: "business_config_activation", Name: "Business configuration activation", Enabled: true,
		TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "scheduled"},
		Graph: &definitionmodel.WorkflowGraphSchema{
			Version: 2,
			Nodes: []definitionmodel.WorkflowGraphNode{
				{ID: "scheduled", Type: "trigger"},
				{ID: "activate", Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{
					ActionKey: "business_config_version.activate_due_candidates", ObjectKey: "business_config_version", OnError: "fail",
				}}},
			},
			Edges: []definitionmodel.WorkflowGraphEdge{{ID: "scheduled-activate", Source: "scheduled", Target: "activate"}},
		},
	}
	worker := &workflowRuntimeWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}}
	service := workflowRecordWorkerService(now, worker, workflow)
	invocations := 0
	service.processEngine.runtime.dependencies.InvokeAction = func(_ context.Context, invocation WorkflowBusinessActionInvocation) (WorkflowBusinessActionInvocationResult, error) {
		invocations++
		if invocation.ActionKey != "business_config_version.activate_due_candidates" || invocation.ObjectKey != "business_config_version" || invocation.RecordID != "" {
			t.Fatalf("unexpected global Action invocation: %+v", invocation)
		}
		return WorkflowBusinessActionInvocationResult{InvocationID: "activate-due", Status: "success", Output: map[string]any{"activated_count": 1}}, nil
	}

	result, err := service.ProcessDueWorkflowExecutionsForTarget(t.Context(), "scheduled:"+workflow.Key, 10, principal)
	if err != nil || result.Processed != 1 || len(result.Executions) != 1 || result.Executions[0].Status != "completed" || invocations != 1 {
		t.Fatalf("global scheduled workflow result=%+v invocations=%d err=%v", result, invocations, err)
	}
	if _, err := service.ProcessDueWorkflowExecutionsForTarget(t.Context(), "scheduled:missing", 10, principal); apperror.CodeOf(err) != "backend.workflow.scheduled_target_not_found" {
		t.Fatalf("missing target error=%v", err)
	}

	invalid := workflow
	invalid.Graph = nil
	if _, err := workflowRecordWorkerService(now, worker, invalid).ProcessDueWorkflowExecutionsForTarget(t.Context(), "scheduled:"+invalid.Key, 10, principal); apperror.CodeOf(err) != "backend.workflow.graph_v2_required" {
		t.Fatalf("global graph error=%v", err)
	}

	idempotent := workflow
	idempotent.IdempotencyKeys = []string{"scheduled_at"}
	for _, decision := range []idempotency.Decision{idempotency.DecisionFingerprintConflict, idempotency.DecisionInProgress} {
		claimWorker := &workflowCommittedIntentWorkerEdgeStub{
			workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}},
			claim:                       workflowmodel.WorkflowExecutionClaimResult{Decision: decision},
		}
		claimService := workflowRecordExecutionService(claimWorker, idempotent)
		if execution, completed, err := claimService.executeGlobalScheduledWorkflow(t.Context(), idempotent, principal, now); err != nil || completed || execution.ID != "" {
			t.Fatalf("global idempotency decision=%s execution=%+v completed=%v err=%v", decision, execution, completed, err)
		}
	}
	replayWorker := &workflowCommittedIntentWorkerEdgeStub{
		workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{
			"global-replay": {ID: "global-replay", Status: "duplicate"},
		}},
		claim: workflowmodel.WorkflowExecutionClaimResult{Decision: idempotency.DecisionReplay, Receipt: workflowmodel.WorkflowExecutionReceipt{ID: "global-receipt", WorkspaceID: principal.WorkspaceID, ExecutionID: "global-replay"}},
	}
	replayService := workflowRecordExecutionService(replayWorker, idempotent)
	if replayed, err := replayService.processScheduledWorkflowExecutionsForTarget(t.Context(), idempotent.Key, 10, principal, now); err != nil || len(replayed) != 0 {
		t.Fatalf("global replay processed=%+v err=%v", replayed, err)
	}
}
