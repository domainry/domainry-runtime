package workflow

import (
	"context"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestScheduledCallbackKeyClosesWorkflowReceiptCrashWindow(t *testing.T) {
	now := time.Date(2026, time.September, 7, 4, 5, 6, 0, time.UTC)
	principal := workflowExecutionPrincipal()
	workflow := definitionmodel.WorkflowSchema{
		Key: "activate", Name: "Activate", Enabled: true,
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

	first, err := service.processScheduledWorkflowExecutionsForTargetWindowWithKey(t.Context(), "scheduled:activate", 1, principal, now, now, "callback-key-1")
	if err != nil || len(first) != 1 || len(worker.claimRequests) != 1 {
		t.Fatalf("first=%+v claims=%+v err=%v", first, worker.claimRequests, err)
	}
	firstKey := worker.claimRequests[0].Receipt.IdempotencyKey
	if firstKey == "" {
		t.Fatal("callback key did not produce a Workflow owner receipt key")
	}
	worker.claim = workflowmodel.WorkflowExecutionClaimResult{Decision: idempotency.DecisionReplay, Receipt: workflowmodel.WorkflowExecutionReceipt{
		ID: "receipt-1", WorkspaceID: principal.WorkspaceID, WorkflowKey: workflow.Key, ExecutionID: first[0].ID,
	}}
	replayed, err := service.processScheduledWorkflowExecutionsForTargetWindowWithKey(t.Context(), "scheduled:activate", 1, principal, now, now, "callback-key-1")
	if err != nil || len(replayed) != 1 || replayed[0].ID != first[0].ID || replayed[0].Status != "duplicate" || len(worker.inserted) != 1 || len(worker.claimRequests) != 2 || worker.claimRequests[1].Receipt.IdempotencyKey != firstKey || worker.claimRequests[1].RequestFingerprint != worker.claimRequests[0].RequestFingerprint {
		t.Fatalf("replayed=%+v claims=%+v err=%v", replayed, worker.claimRequests, err)
	}

	worker.claim = workflowmodel.WorkflowExecutionClaimResult{Decision: idempotency.DecisionAcquired, Receipt: workflowmodel.WorkflowExecutionReceipt{
		ID: "receipt-2", WorkspaceID: principal.WorkspaceID, WorkflowKey: workflow.Key, LeaseOwner: "worker", FencingToken: 1,
	}}
	second, err := service.processScheduledWorkflowExecutionsForTargetWindowWithKey(t.Context(), "scheduled:activate", 1, principal, now, now, "callback-key-2")
	if err != nil || len(second) != 1 || len(worker.claimRequests) != 3 || worker.claimRequests[2].Receipt.IdempotencyKey == firstKey {
		t.Fatalf("second=%+v claims=%+v err=%v", second, worker.claimRequests, err)
	}
}
